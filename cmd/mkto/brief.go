// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/canonical/snap-mkto/internal/llm"
	"github.com/canonical/snap-mkto/internal/marketo"
	"github.com/canonical/snap-mkto/internal/output"
	"github.com/canonical/snap-mkto/internal/redact"
)

func newBriefCmd(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "brief <email|value>",
		Short: "LLM-written lead brief (--show-payload previews what would be sent)",
		Long: "Write a short brief about a lead from a redacted activity timeline.\n\n" +
			"--show-payload prints exactly what would be sent to the LLM and sends nothing. It works with [llm] enabled = false and needs no API key.\n\n" +
			"\"How Canonical could help\" comes only from playbook.md in the config directory; without it that step is skipped. " +
			"--save also writes the brief to briefs/ there (mode 0600).\n\n" +
			"Real lead data needs data-protection sign-off before you enable this; see docs/LLM_LAYER.md.",
		Args: func(c *cobra.Command, a []string) error { return usageErr(cobra.ExactArgs(1)(c, a)) },
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBrief(cmd, env, strings.TrimSpace(args[0]))
		},
	}
	f := cmd.Flags()
	f.Int("days", 90, "window size in days (at least 1)")
	f.Int("lead-id", 0, "pick this lead when several match")
	f.String("by", "email", byHelp)
	f.Bool("show-payload", false, "print what would be sent to the LLM and send nothing")
	f.Bool("no-research", false, "skip the company research step")
	f.Bool("save", false, "also write the brief to briefs/ in the config directory (mode 0600)")
	f.Bool("web", false, "include Visit Webpage and Click Link (slow on large Marketo instances)")
	return cmd
}

func runBrief(cmd *cobra.Command, env Env, value string) error {
	f := cmd.Flags()
	by, _ := f.GetString("by")
	days, _ := f.GetInt("days")
	leadID, _ := f.GetInt("lead-id")
	show, _ := f.GetBool("show-payload")
	if err := checkKey(by, value); err != nil {
		return err
	}
	if days < 1 {
		return exitError{2, errors.New("--days must be at least 1")}
	}
	if save, _ := f.GetBool("save"); save && show {
		return exitError{2, errors.New("--save can't be used with --show-payload")}
	}
	cfg, cfgPath, client, err := newClient(cmd, env)
	if err != nil {
		return err
	}
	// Gates and the provider come before any Marketo call, so a refused run costs no quota.
	var prov llm.Provider
	if !show {
		if !cfg.LLM.Enabled {
			return fmt.Errorf("mkto brief is off. To turn it on, set [llm] enabled = true and api_key (or MKTO_LLM_API_KEY) in %s. "+
				"Preview what would be sent first: mkto brief <email> --show-payload. "+
				"Data-protection sign-off: https://github.com/canonical/snap-mkto/blob/main/docs/LLM_LAYER.md", cfgPath)
		}
		if cfg.LLM.APIKey == "" {
			return fmt.Errorf("llm.api_key is not set (or set MKTO_LLM_API_KEY) in %s", cfgPath)
		}
		if v := cfg.LLM.Provider; v != "" && v != "anthropic" {
			return fmt.Errorf("llm.provider %q is not supported (only \"anthropic\") in %s", v, cfgPath)
		}
		if strings.TrimSpace(cfg.LLM.Model) == "" {
			return fmt.Errorf("llm.model is not set: set it in the [llm] section of %s", cfgPath)
		}
		var log io.Writer
		if v, _ := f.GetBool("verbose"); v {
			log = env.Stderr
		}
		prov, err = llm.NewAnthropic(llm.Options{APIKey: cfg.LLM.APIKey, Model: cfg.LLM.Model, BaseURL: env.LLMBaseURL,
			HTTP: env.HTTP, PromptDir: filepath.Join(filepath.Dir(cfgPath), "prompts"), Log: log})
		if err != nil {
			return err
		}
	}

	ctx := cmd.Context()
	leads, err := leadsBy(ctx, cmd, env, client, cfg, by, value,
		[]string{"id", "updatedAt", "firstName", "lastName", "email", "title", "company", "industry", "country", "numberOfEmployees"})
	if err != nil {
		return mapErr(err, cfg, cfgPath)
	}
	lead, notes, err := pickLead(leads, leadID, by, value)
	if err != nil {
		return err
	}
	for _, n := range notes {
		fmt.Fprintln(env.Stderr, output.Clean(n))
	}
	all, err := client.ActivityTypes(ctx)
	if err != nil {
		return mapErr(err, cfg, cfgPath)
	}
	web, _ := f.GetBool("web")
	names, note, err := dropWebTypes(cfg.Marketo.ActivityTypes, web)
	if err != nil {
		return err
	}
	if note != "" {
		fmt.Fprintln(env.Stderr, note)
	}
	typeIDs, unknown := marketo.ResolveTypes(all, names)
	warnUnknownTypes(cmd, env, cfg, cfgPath, unknown, func(n string) {
		if s := closestType(all, n); s != "" {
			fmt.Fprintf(env.Stderr, "warning: unknown activity type %q skipped; did you mean %q? (check activity_types in %s; see mkto types)\n", output.Clean(n), output.Clean(s), cfgPath)
			return
		}
		fmt.Fprintf(env.Stderr, "warning: unknown activity type %q skipped; check activity_types in %s\n", output.Clean(n), cfgPath)
	})
	if len(typeIDs) == 0 {
		return fmt.Errorf("no known activity types to fetch; check activity_types in %s", cfgPath)
	}
	typeName := map[int]string{}
	for _, t := range all {
		typeName[t.ID] = t.Name
	}
	acts, err := client.Activities(ctx, lead.ID(), typeIDs, env.Now().Add(-time.Duration(days)*24*time.Hour).UTC())
	if err != nil {
		return mapErr(err, cfg, cfgPath)
	}

	if sf := cfg.Why.StatusField; sf != "" && redact.PersonalLabel(sf) {
		fmt.Fprintf(env.Stderr, "note: [why] status_field %q looks like personal data, so its changes are not sent to the LLM\n", output.Clean(sf))
	}
	p, err := redact.Build(lead, acts, typeName, redact.Options{StatusField: cfg.Why.StatusField})
	if err != nil {
		return err // names the field, never the value
	}
	if !show {
		if v, _ := f.GetBool("verbose"); v {
			fmt.Fprintln(env.Stderr, redactedLine(p))
		}
		return writeBrief(cmd, env, prov, p, days, lead.ID(), filepath.Dir(cfgPath))
	}
	var buf bytes.Buffer
	buf.WriteString("# " + redactedLine(p) + "\n# Narrate payload (would be sent to the LLM)\n")
	buf.Write(indent(p.JSON()))
	if noResearch, _ := f.GetBool("no-research"); !noResearch {
		buf.WriteString("\n\n# Research query (would be sent to the LLM with web search)\n")
		buf.Write(indent(p.Company().JSON()))
	}
	buf.WriteString("\n")
	_, err = env.Stdout.Write(buf.Bytes())
	return err
}

// redactedLine lists the kinds and paths Build replaced, never the values.
func redactedLine(p redact.Payload) string {
	var parts []string
	for _, r := range p.Redactions() {
		parts = append(parts, r.Kind+" in "+r.Path)
	}
	return "Redacted before sending: " + cmp.Or(strings.Join(parts, ", "), "nothing")
}

// removedClaim is a sentence dropped by the citation check or by verify.
type removedClaim struct {
	llm.Sentence
	Reason string `json:"reason"`
}

// inlineCite matches ids the model wrote into a sentence, e.g. " [E3]" or "[E2, E3]".
// They are stripped so the brief shows exactly the checked cites.
var inlineCite = regexp.MustCompile(`\s*\[E[0-9]+(\s*,\s*E[0-9]+)*\]`)

// strayID is an event id left in a sentence after inlineCite: "[E3 ]", "(E3)" or a bare E3.
var strayID = regexp.MustCompile(`\bE[0-9]+\b`)

// writeBrief runs narrate, the citation check, verify, research and pitch, then renders the brief.
func writeBrief(cmd *cobra.Command, env Env, prov llm.Provider, p redact.Payload, days, leadID int, cfgDir string) error {
	ctx := cmd.Context()
	f := cmd.Flags()
	asJSON, _ := f.GetBool("json")
	verbose, _ := f.GetBool("verbose")
	events := p.Events()
	ids := map[string]bool{}
	for _, e := range events {
		ids[e.ID] = true
	}

	var note, declined string // R32: a refused step is reported, not fatal
	kept, removed := []llm.Sentence{}, []removedClaim{}
	n, nu, err := prov.Narrate(ctx, p)
	if errors.Is(err, llm.ErrRefused) {
		note, declined = "_The model declined to write this section._", "narrate"
	} else if err != nil {
		return err
	}
	for _, s := range n.Sentences {
		s.Text = output.Clean(strings.TrimSpace(inlineCite.ReplaceAllString(s.Text, "")))
		if len(s.Cites) == 0 || slices.ContainsFunc(s.Cites, func(c string) bool { return !ids[c] }) || strayID.MatchString(s.Text) {
			removed = append(removed, removedClaim{s, "no valid citation"})
		} else {
			kept = append(kept, s)
		}
	}
	var vu llm.Usage
	if len(kept) > 0 {
		var bad []int
		bad, vu, err = prov.Verify(ctx, p, llm.Narrative{Sentences: kept})
		if errors.Is(err, llm.ErrRefused) {
			note, declined = "_Not verified: the model declined the check._", "verify"
		} else if err != nil {
			return err
		}
		ok := []llm.Sentence{}
		for i, s := range kept {
			if slices.Contains(bad, i) {
				removed = append(removed, removedClaim{s, "unsupported"})
			} else {
				ok = append(ok, s)
			}
		}
		kept = ok
	}

	// Research runs last; its failure never fails the brief.
	noResearch, _ := f.GetBool("no-research")
	var res *researchOut // nil: skipped with --no-research
	var ru llm.Usage
	noCompany := false
	if !noResearch {
		res = &researchOut{Research: llm.Research{Summary: []llm.Claim{}, Signals: []llm.Claim{}, News: []llm.Claim{}, Sources: []llm.Source{}}}
		if q := p.Company(); q.Name() == "" {
			noCompany, res.Unavailable = true, "no company name"
		} else {
			var r llm.Research
			r, ru, err = prov.Research(ctx, q)
			switch {
			case err != nil && ctx.Err() != nil:
				return err // interrupted: stop, don't print a partial brief
			case errors.Is(err, llm.ErrResearchUnavailable):
				res.Unavailable = output.Clean(strings.TrimPrefix(err.Error(), llm.ErrResearchUnavailable.Error()+": "))
			case errors.Is(err, llm.ErrRefused):
				res.Unavailable = "the model declined"
			case err != nil:
				res.Unavailable = output.Clean(err.Error())
			default:
				res.Research = cleanResearch(r)
			}
		}
	}
	researchNote := ""
	switch {
	case res == nil:
	case noCompany:
		researchNote = "_No company name to research._"
	case res.Unavailable != "":
		researchNote = unavailableNote(res.Unavailable)
	case len(res.Summary)+len(res.Signals)+len(res.News) == 0 && res.Size == nil && res.Industry == nil:
		researchNote = "_No claim had a verified source._"
	}

	// Pitch runs after research, with or without it; like research, its failure never fails the brief.
	pitch := &pitchOut{Pitch: llm.Pitch{Offerings: []llm.Offering{}, Unknowns: []string{}}}
	var pu llm.Usage
	pitchNote := ""
	switch playbook, err := readPlaybook(cfgDir); {
	case errors.Is(err, fs.ErrNotExist):
		pitch.Unavailable = "no playbook.md in " + output.Clean(cfgDir)
		pitchNote = "_No playbook.md in " + output.Clean(cfgDir) + "; skipped._"
	case errors.Is(err, errPlaybookTooLarge):
		pitch.Unavailable = "playbook.md is larger than 64 KiB"
		pitchNote = "_playbook.md is larger than 64 KiB; skipped._"
	case err != nil:
		pitch.Unavailable = output.Clean(err.Error())
		pitchNote = unavailable("Pitch", pitch.Unavailable)
	default:
		in := llm.PitchInput{Sentences: kept, Claims: []llm.Claim{}, Sources: []llm.Source{}, Playbook: playbook}
		if res != nil && res.Unavailable == "" {
			in.Claims, in.Sources = res.claims(), res.Sources
		}
		var pr llm.Pitch
		pr, pu, err = prov.Pitch(ctx, p, in)
		switch {
		case err != nil && ctx.Err() != nil:
			return err
		case errors.Is(err, llm.ErrRefused):
			pitch.Unavailable = "the model declined"
		case err != nil:
			pitch.Unavailable = output.Clean(err.Error())
		default:
			pitch.Pitch = cleanPitch(pr)
		}
		if pitch.Unavailable != "" {
			pitchNote = unavailable("Pitch", pitch.Unavailable)
		}
	}
	total := nu.Add(vu).Add(ru).Add(pu)

	if verbose {
		if len(removed) > 0 {
			fmt.Fprintln(env.Stderr, "Removed claims:")
			for _, r := range removed {
				fmt.Fprintf(env.Stderr, "- %s (%s)\n", cited(r.Sentence), r.Reason)
			}
		}
		fmt.Fprintf(env.Stderr, "llm total %d/%d tokens\n", total.InputTokens, total.OutputTokens)
	}

	company := p.Company().Name()
	mdCompany := md(output.Clean(cmp.Or(company, "unknown company")))
	var b strings.Builder
	fmt.Fprintf(&b, "# Lead brief · %s · last %d days\n\n## What happened\n", mdCompany, days)
	para := make([]string, len(kept))
	for i, s := range kept {
		para[i] = marked(s.Text, s.Cites, true)
	}
	switch text := strings.Join(para, " "); {
	case text != "" && note != "":
		b.WriteString(text + "\n\n" + note + "\n")
	case text != "":
		b.WriteString(text + "\n")
	case note != "":
		b.WriteString(note + "\n")
	default:
		b.WriteString("_No sentence passed the citation checks._\n")
	}
	if res != nil {
		fmt.Fprintf(&b, "\n## About %s\n", mdCompany)
		if researchNote != "" {
			b.WriteString(researchNote + "\n")
		} else {
			writeResearch(&b, res.Research)
		}
	}
	b.WriteString("\n## How Canonical could help\n")
	switch {
	case pitchNote != "":
		b.WriteString(pitchNote + "\n")
	case pitch.NoFit:
		b.WriteString("_Nothing in the playbook fits these signals._\n")
	default:
		for i, o := range pitch.Offerings {
			fmt.Fprintf(&b, "%d. **%s** — %s\n", i+1, md(o.Name), marked(o.Reason, o.Cites, true))
		}
		if pitch.Opener != "" {
			fmt.Fprintf(&b, "\nSuggested opener: \"%s\"\n", md(pitch.Opener))
		}
	}
	if len(pitch.Unknowns) > 0 {
		b.WriteString("\n## What we don't know\n")
		for _, u := range pitch.Unknowns {
			b.WriteString("- " + md(u) + "\n")
		}
	}
	b.WriteString("\n## Events\n")
	rows := make([][]string, len(events))
	for i, e := range events {
		rows[i] = []string{e.ID, e.Date.Format("2006-01-02 15:04 UTC"), e.Type, strings.TrimSpace(cmp.Or(e.Asset, e.URL) + " " + e.Change)}
	}
	var tbl bytes.Buffer
	if err := output.Table(&tbl, rows); err != nil {
		return err
	}
	var evs strings.Builder
	for line := range strings.Lines(tbl.String()) {
		evs.WriteString(strings.TrimRight(line, " \n") + "\n") // an empty last cell leaves padding
	}
	// Asset names and URL paths are Marketo text: a code fence longer than any backtick run keeps them inert.
	fence := "```"
	for strings.Contains(evs.String(), fence) {
		fence += "`"
	}
	b.WriteString(fence + "\n" + evs.String() + fence + "\n")
	if res != nil && len(res.Sources) > 0 {
		b.WriteString("\n## Sources\n")
		for i, s := range res.Sources {
			// An autolink: llm keeps only http(s) URLs without <, > or spaces, so a URL can't become link text.
			if s.Title == "" {
				fmt.Fprintf(&b, "%d. <%s>\n", i+1, s.URL)
			} else {
				fmt.Fprintf(&b, "%d. <%s> — %s\n", i+1, s.URL, md(s.Title))
			}
		}
	}
	if asJSON {
		for _, n := range []string{note, researchNote, pitchNote} {
			if n != "" {
				fmt.Fprintln(env.Stderr, strings.Trim(n, "_"))
			}
		}
		type usage struct {
			Narrate  llm.Usage `json:"narrate"`
			Verify   llm.Usage `json:"verify"`
			Research llm.Usage `json:"research"`
			Pitch    llm.Usage `json:"pitch"`
			Total    llm.Usage `json:"total"`
		}
		err = output.WriteJSON(env.Stdout, struct {
			Company   string         `json:"company"`
			Days      int            `json:"days"`
			Verified  bool           `json:"verified"` // no step declined: every sentence shown passed verify (none shown also counts)
			Declined  string         `json:"declined"` // "", "narrate" or "verify"
			Sentences []llm.Sentence `json:"sentences"`
			Removed   []removedClaim `json:"removed"`
			Research  *researchOut   `json:"research"` // null with --no-research
			Pitch     *pitchOut      `json:"pitch"`
			Events    []redact.Event `json:"events"`
			Usage     usage          `json:"usage"`
		}{company, days, declined == "", declined, kept, removed, res, pitch, events, usage{nu, vu, ru, pu, total}})
	} else {
		_, err = io.WriteString(env.Stdout, b.String())
	}
	if err != nil {
		return err
	}
	if save, _ := f.GetBool("save"); save {
		path, err := saveBrief(env.Stderr, cfgDir, env.Now().UTC().Format("20060102T150405Z")+"-"+strconv.Itoa(leadID), b.String())
		if err != nil {
			return err
		}
		fmt.Fprintln(env.Stderr, "Saved "+output.Clean(path))
	}
	return nil
}

const playbookMax = 64 << 10

var errPlaybookTooLarge = errors.New("playbook.md is larger than 64 KiB")

// readPlaybook reads <dir>/playbook.md, at most playbookMax bytes.
func readPlaybook(dir string) (string, error) {
	f, err := os.Open(filepath.Join(dir, "playbook.md"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		return "", fmt.Errorf("cannot read playbook.md: %w", errors.Unwrap(err)) // the path is in the note already
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, playbookMax+1))
	if err != nil {
		return "", fmt.Errorf("cannot read playbook.md: %w", errors.Unwrap(err))
	}
	if len(b) > playbookMax {
		return "", errPlaybookTooLarge
	}
	return string(b), nil
}

// saveBrief writes text to <dir>/briefs/<name>.md (or <name>-2.md, ... if taken) with mode 0600,
// creating briefs/ with mode 0700. It never overwrites a file and never saves through a symlinked briefs/.
func saveBrief(warn io.Writer, dir, name, text string) (string, error) {
	briefs := filepath.Join(dir, "briefs")
	switch fi, err := os.Lstat(briefs); {
	case errors.Is(err, fs.ErrNotExist):
		if err := os.Mkdir(briefs, 0o700); err != nil {
			return "", fmt.Errorf("cannot create %s: %w", briefs, errors.Unwrap(err))
		}
		if err := os.Chmod(briefs, 0o700); err != nil { // Mkdir's mode is masked by the umask
			return "", err
		}
	case err != nil:
		return "", fmt.Errorf("cannot save the brief: %w", err)
	case fi.Mode()&fs.ModeSymlink != 0:
		return "", fmt.Errorf("briefs/ in %s is a symlink; refusing to save", dir)
	case fi.IsDir() && fi.Mode().Perm()&0o077 != 0:
		fmt.Fprintf(warn, "warning: %s is accessible by other users; run: chmod 700 %s\n", briefs, briefs)
	}
	for i := 1; ; i++ {
		path := filepath.Join(briefs, name+".md")
		if i > 1 {
			path = filepath.Join(briefs, name+"-"+strconv.Itoa(i)+".md")
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if errors.Is(err, fs.ErrExist) && i < 100 {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("cannot save the brief: %w", err)
		}
		_, err = f.WriteString(text)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return "", fmt.Errorf("cannot save the brief to %s: %w", path, err)
		}
		return path, nil
	}
}

// pitchOut is the pitch section of --json: the pitch, or why it is missing.
type pitchOut struct {
	llm.Pitch
	Unavailable string `json:"unavailable"`
}

// cleanPitch strips control characters from every model string.
func cleanPitch(p llm.Pitch) llm.Pitch {
	out := llm.Pitch{Offerings: make([]llm.Offering, len(p.Offerings)), Opener: output.Clean(p.Opener), Unknowns: make([]string, len(p.Unknowns)), NoFit: p.NoFit}
	for i, o := range p.Offerings {
		out.Offerings[i] = llm.Offering{Name: output.Clean(o.Name), Reason: output.Clean(o.Reason), Cites: o.Cites}
	}
	for i, u := range p.Unknowns {
		out.Unknowns[i] = output.Clean(u)
	}
	return out
}

// claims lists every research claim in the order the brief shows them.
func (r *researchOut) claims() []llm.Claim {
	cs := slices.Clone(r.Summary)
	for _, c := range []*llm.Claim{r.Size, r.Industry} {
		if c != nil {
			cs = append(cs, *c)
		}
	}
	return append(append(cs, r.Signals...), r.News...)
}

// mdEscape backslash-escapes markdown metacharacters, so model and web text cannot add links, images or HTML.
var mdEscape = strings.NewReplacer(`\`, `\\`, "`", "\\`", "[", `\[`, "]", `\]`, "(", `\(`, ")", `\)`,
	"!", `\!`, "<", `\<`, ">", `\>`, "*", `\*`, "_", `\_`, "#", `\#`, "~", `\~`)

// md escapes s for the markdown brief. s is already output.Clean'ed, so it holds no newlines.
func md(s string) string { return mdEscape.Replace(s) }

// unavailableNote is the About section's line when research failed for reason.
func unavailableNote(reason string) string { return unavailable("Research", reason) }

// unavailable is a section's line when step failed for reason (mkto's or the API's own words, not model text).
func unavailable(step, reason string) string {
	return "_" + step + " unavailable: " + strings.TrimSuffix(reason, ".") + "._"
}

// researchOut is the research section of --json: the research, or why it is missing.
type researchOut struct {
	llm.Research
	Unavailable string `json:"unavailable"`
}

// cleanResearch strips control characters from every web or model string.
func cleanResearch(r llm.Research) llm.Research {
	claims := func(cs []llm.Claim) []llm.Claim {
		out := make([]llm.Claim, len(cs))
		for i, c := range cs {
			out[i] = llm.Claim{Text: output.Clean(c.Text), Sources: c.Sources}
		}
		return out
	}
	one := func(c *llm.Claim) *llm.Claim {
		if c == nil {
			return nil
		}
		return &claims([]llm.Claim{*c})[0]
	}
	src := make([]llm.Source, len(r.Sources))
	for i, s := range r.Sources {
		src[i] = llm.Source{URL: output.Clean(s.URL), Title: output.Clean(s.Title)}
	}
	return llm.Research{Summary: claims(r.Summary), Size: one(r.Size), Industry: one(r.Industry),
		Signals: claims(r.Signals), News: claims(r.News), Sources: src}
}

// writeResearch writes the summary, size and industry as one paragraph, then the signals and news as lists.
func writeResearch(b *strings.Builder, r llm.Research) {
	var para, blocks []string
	for _, c := range r.Summary {
		para = append(para, sourced(c))
	}
	for _, c := range []*llm.Claim{r.Size, r.Industry} {
		if c != nil {
			para = append(para, sourced(*c))
		}
	}
	if len(para) > 0 {
		blocks = append(blocks, strings.Join(para, " "))
	}
	for _, l := range []struct {
		name   string
		claims []llm.Claim
	}{{"Signals", r.Signals}, {"News", r.News}} {
		if len(l.claims) > 0 {
			lines := []string{l.name + ":"}
			for _, c := range l.claims {
				lines = append(lines, "- "+sourced(c))
			}
			blocks = append(blocks, strings.Join(lines, "\n"))
		}
	}
	b.WriteString(strings.Join(blocks, "\n\n") + "\n")
}

// sourced returns the claim, markdown-escaped, with its source numbers before the final punctuation: "Text [1][2]."
func sourced(c llm.Claim) string {
	ids := make([]string, len(c.Sources))
	for i, n := range c.Sources {
		ids[i] = strconv.Itoa(n)
	}
	return marked(c.Text, ids, true)
}

// cited returns the sentence as plain text with its cites before the final punctuation: "Text [E1][E2]."
func cited(s llm.Sentence) string { return marked(s.Text, s.Cites, false) }

// marked puts [mark] cites before t's final punctuation. With escape, t is markdown-escaped
// (the punctuation stays bare, so a final "!" cannot start an image: nothing follows it).
func marked(t string, marks []string, escape bool) string {
	end := ""
	if i := len(t) - 1; i >= 0 && len(marks) > 0 && strings.ContainsRune(".!?", rune(t[i])) {
		t, end = t[:i], t[i:]
	}
	if escape {
		t = md(t)
	}
	if len(marks) == 0 {
		return t
	}
	ids := "[" + strings.Join(marks, "][") + "]"
	if end != "" {
		return t + " " + ids + end
	}
	return strings.TrimSpace(t + " " + ids)
}

func indent(b []byte) []byte {
	var out bytes.Buffer
	if err := json.Indent(&out, b, "", "  "); err != nil {
		panic(err) // redact emits valid JSON
	}
	return out.Bytes()
}

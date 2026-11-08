// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/canonical/snap-mkto/internal/config"
	"github.com/canonical/snap-mkto/internal/marketo"
	"github.com/canonical/snap-mkto/internal/output"
	"github.com/canonical/snap-mkto/internal/story"
)

const maxProgramLookups, maxCampaignLookups = 25, 25

func newActivityCmd(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "activity <email|value>",
		Short: "Show a lead's activity timeline and programs",
		Long: "Show a lead's activity timeline and program memberships.\n\n" +
			"Marketo purges some high-volume activity types after a limited period, so an empty result for an older window isn't necessarily an error.",
		Args: func(c *cobra.Command, a []string) error { return usageErr(cobra.ExactArgs(1)(c, a)) },
		RunE: func(cmd *cobra.Command, args []string) error {
			return runActivity(cmd, env, strings.TrimSpace(args[0]))
		},
	}
	f := cmd.Flags()
	f.Int("days", 14, "window size in days (at least 1)")
	f.String("types", "", "comma-separated activity type names (overrides activity_types)")
	f.Bool("all-types", false, "fetch every activity type (uses more API quota)")
	f.Bool("web", false, "include Visit Webpage and Click Link (slow on large Marketo instances)")
	f.Bool("all-changes", false, "show every Change Data Value field, ignoring change_fields")
	f.Bool("all-programs", false, "show every program membership, not only those in the window")
	f.Int("lead-id", 0, "pick this lead when several match")
	f.String("by", "email", byHelp)
	f.String("out", "", "write the output to FILE (mode 0600) instead of stdout")
	f.Bool("force", false, "overwrite an existing --out file")
	return cmd
}

func runActivity(cmd *cobra.Command, env Env, value string) (err error) {
	f := cmd.Flags()
	by, _ := f.GetString("by")
	if err := checkKey(by, value); err != nil {
		return err
	}
	days, _ := f.GetInt("days")
	typesFlag, _ := f.GetString("types")
	allTypes, _ := f.GetBool("all-types")
	allPrograms, _ := f.GetBool("all-programs")
	leadID, _ := f.GetInt("lead-id")
	outPath, _ := f.GetString("out")
	asJSON, _ := f.GetBool("json")
	switch {
	case days < 1:
		return exitError{2, errors.New("--days must be at least 1")}
	case f.Changed("types") && allTypes:
		return exitError{2, errors.New("--types and --all-types can't be used together")}
	}
	warn := func(format string, a ...any) {
		fmt.Fprintf(env.Stderr, "warning: %s\n", output.Clean(fmt.Sprintf(format, a...)))
	}
	if days > 90 {
		warn("--days above 90 uses more API quota")
	}
	if allTypes {
		warn("--all-types fetches every activity type and uses more API quota")
	}
	var out *os.File
	if outPath != "" {
		force, _ := f.GetBool("force")
		if out, err = openOut(env, outPath, force); err != nil {
			return err
		}
		defer func() {
			if err != nil { // leave no empty or partial file of mkto's own behind
				out.Close()
				os.Remove(out.Name())
			}
		}()
	}

	cfg, cfgPath, client, err := newClient(cmd, env)
	if err != nil {
		return err
	}
	loc, err := cfg.Location()
	if err != nil {
		return err
	}
	ctx := cmd.Context()

	leads, err := leadsBy(ctx, cmd, env, client, cfg, by, value, cardExtras(cfg))
	if err != nil {
		return mapErr(err, cfg, cfgPath)
	}
	lead, notes, err := pickLead(leads, leadID, by, value)
	if err != nil {
		return err
	}
	if !asJSON { // ahead of stdout, so it can't scroll away under a long timeline
		for _, n := range notes {
			fmt.Fprintf(env.Stderr, "note: %s\n", output.Clean(n))
		}
		notes = nil
	}

	all, err := client.ActivityTypes(ctx)
	if err != nil {
		return mapErr(err, cfg, cfgPath)
	}
	var typeIDs []int
	if allTypes {
		for _, t := range all {
			typeIDs = append(typeIDs, t.ID)
		}
	} else {
		names := strings.Split(typesFlag, ",")
		if !f.Changed("types") { // an explicit --types wins over the web filter
			web, _ := f.GetBool("web")
			var note string
			if names, note, err = dropWebTypes(cfg.Marketo.ActivityTypes, web); err != nil {
				return err
			}
			if note != "" {
				fmt.Fprintln(env.Stderr, note)
			}
		}
		var unknown []string
		typeIDs, unknown = marketo.ResolveTypes(all, names)
		warnType := func(n string) {
			if s := closestType(all, n); s != "" {
				warn("unknown activity type %q skipped; did you mean %q? (check activity_types in %s or --types; see mkto types)", n, s, cfgPath)
				return
			}
			warn("unknown activity type %q skipped; check activity_types in %s or --types", n, cfgPath)
		}
		if f.Changed("types") { // an explicit list always warns
			for _, n := range unknown {
				warnType(strings.TrimSpace(n))
			}
		} else {
			warnUnknownTypes(cmd, env, cfg, cfgPath, unknown, warnType)
		}
	}
	if len(typeIDs) == 0 {
		return fmt.Errorf("no known activity types to fetch; check activity_types in %s or --types", cfgPath)
	}
	typeName := map[int]string{}
	for _, t := range all {
		typeName[t.ID] = t.Name
	}

	since := env.Now().Add(-time.Duration(days) * 24 * time.Hour).UTC()
	acts, err := client.Activities(ctx, lead.ID(), typeIDs, since)
	if err != nil {
		return mapErr(err, cfg, cfgPath)
	}
	members, err := client.Memberships(ctx, lead.ID())
	if err != nil {
		return mapErr(err, cfg, cfgPath)
	}
	members, older := windowPrograms(members, since, allPrograms)
	ids := make([]int, len(members))
	for i, m := range members {
		ids[i] = m.ProgramID
	}
	// Each program costs a name lookup and a token lookup; both share the one cap.
	names, failed, err := lookupNames(ctx, ids, maxProgramLookups, func(id int) (string, error) {
		p, err := client.Program(ctx, id)
		return p.Name, err
	})
	if lookupFatal(ctx, err) {
		return mapErr(err, cfg, cfgPath)
	}
	if failed > 0 {
		warn("could not look up %d of %d program names (%s)", failed, len(names)+failed, lookupHint(err))
	}
	tokens := map[int]map[string]string{}
	if len(cfg.Marketo.NameTokens)+len(cfg.Marketo.LinkTokens) > 0 {
		// Only programs Marketo named: one whose name lookup failed would fail here too.
		named := slices.DeleteFunc(slices.Clone(ids), func(id int) bool { _, ok := names[id]; return !ok })
		tokens, failed, err = lookupNames(ctx, named, maxProgramLookups, func(id int) (map[string]string, error) {
			return client.ProgramTokens(ctx, id)
		})
		if lookupFatal(ctx, err) {
			return mapErr(err, cfg, cfgPath)
		}
		if failed > 0 {
			warn("could not read program tokens for %d of %d programs (%s)", failed, len(tokens)+failed, lookupHint(err))
		}
	}
	programs := make([]output.MembershipJSON, len(members))
	for i, m := range members {
		programs[i] = output.MembershipJSON{ID: m.ProgramID, Name: names[m.ProgramID], Status: m.Status, Date: m.Date,
			AcquiredBy: m.AcquiredBy, Success: m.Success,
			Title: tokenValue(tokens[m.ProgramID], cfg.Marketo.NameTokens), Link: tokenValue(tokens[m.ProgramID], cfg.Marketo.LinkTokens)}
	}
	distinct := map[int]bool{}
	for _, m := range members {
		distinct[m.ProgramID] = true
	}
	if len(distinct) > maxProgramLookups {
		notes = append(notes, fmt.Sprintf("showing names for %d of %d programs", maxProgramLookups, len(distinct)))
	}
	olderNote := ""
	if older > 0 {
		olderNote = fmt.Sprintf("+ %d older programs (use --all-programs)", older)
	}

	shown := acts // text hides Change Data Value rows outside change_fields; --json keeps them
	if allChanges, _ := f.GetBool("all-changes"); !asJSON && len(cfg.Marketo.ChangeFields) > 0 && !allChanges {
		shown = slices.DeleteFunc(slices.Clone(acts), func(a marketo.Activity) bool {
			return strings.EqualFold(typeName[a.TypeID], "Change Data Value") && !slices.ContainsFunc(cfg.Marketo.ChangeFields,
				func(n string) bool { return strings.EqualFold(strings.TrimSpace(n), story.ChangeField(a)) })
		})
	}
	var campaignIDs []int
	for _, a := range shown {
		if isChange(a, typeName) {
			campaignIDs = append(campaignIDs, a.CampaignID)
		}
	}
	campaigns, err := campaignNames(ctx, client, campaignIDs, warn)
	if err != nil {
		return mapErr(err, cfg, cfgPath)
	}

	var buf bytes.Buffer
	if asJSON {
		res := output.ActivityJSON{
			Lead:     leadJSON(cfg, lead, loc),
			Since:    since,
			Events:   []output.EventJSON{},
			Programs: programs,
			Notes:    append([]string{}, notes...),
		}
		if olderNote != "" {
			res.Notes = append(res.Notes, olderNote)
		}
		for _, a := range acts {
			res.Events = append(res.Events, output.EventJSON{ID: a.ID, Date: a.Date.UTC(), Type: label(typeName[a.TypeID], "type", a.TypeID), Primary: a.Primary, PrimaryID: a.PrimaryID, CampaignID: a.CampaignID,
				Campaign: campaigns[a.CampaignID], Detail: story.Detail(typeName[a.TypeID], a), Attributes: a.Attributes})
		}
		err = output.WriteJSON(&buf, res)
	} else {
		if n := len(acts) - len(shown); n > 0 {
			notes = append(notes, fmt.Sprintf("Change Data Value rows hidden by change_fields: %d (use --all-changes)", n))
		}
		if verbose, _ := f.GetBool("verbose"); verbose && cfg.Output.LeadURL != "" && !strings.Contains(cfg.Output.LeadURL, "{id}") {
			fmt.Fprintln(env.Stderr, "note: output.lead_url has no {id}; not showing a record link")
		}
		err = writeActivity(&buf, callCard(cfg, loc, lead, acts, typeName, days, env.Now()), days, loc, shown, len(acts)-len(shown), typeName, campaigns, programs, olderNote, notes)
	}
	if err != nil {
		return err
	}
	return finishOut(env, out, outPath, buf.Bytes())
}

// openOut opens --out before any Marketo call, so a bad or taken path costs no quota.
// The file it returns is always mkto's own (mode 0600), so a failed run removes it. With
// force and an existing target it is a temp file in the same directory, renamed over the
// target on success, so a failed run leaves the target as it was.
func openOut(env Env, path string, force bool) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err == nil {
		return file, nil
	}
	if errors.Is(err, fs.ErrExist) {
		if !force {
			return nil, exitError{2, fmt.Errorf("%s exists; use --force to overwrite", path)}
		}
		// Not a dot name: the snap's home plug denies hidden files at the top of $HOME.
		if file, err = os.CreateTemp(filepath.Dir(path), "mkto-out-*.tmp"); err == nil {
			return file, nil
		}
	}
	if env.Getenv("SNAP_NAME") != "" && errors.Is(err, fs.ErrPermission) {
		return nil, fmt.Errorf("%w; the mkto snap can write only to non-hidden paths in your home folder (home plug), e.g. ~/%s",
			err, strings.TrimLeft(filepath.Base(path), "."))
	}
	return nil, err
}

// snapTmpNote explains where a /tmp file really went: a strict snap gets a private /tmp.
func snapTmpNote(snap, abs string) string {
	if snap == "" || !strings.HasPrefix(abs, "/tmp/") {
		return ""
	}
	return fmt.Sprintf("note: /tmp is private to the %s snap; the file is at /tmp/snap-private-tmp/snap.%s%s (readable with sudo)", snap, snap, abs)
}

// windowPrograms returns the memberships to show, newest first (undated last), and how
// many older ones it hid. Without all, those dated before since are hidden; undated ones
// are always shown, since the date field is unverified on a real instance.
func windowPrograms(members []marketo.Membership, since time.Time, all bool) ([]marketo.Membership, int) {
	shown := slices.Clone(members)
	if !all {
		shown = slices.DeleteFunc(shown, func(m marketo.Membership) bool { return !m.Date.IsZero() && m.Date.Before(since) })
	}
	slices.SortStableFunc(shown, func(a, b marketo.Membership) int { return b.Date.Compare(a.Date) })
	return shown, len(members) - len(shown)
}

// lookupHint says what to do about a failed program lookup, by the error's class.
func lookupHint(err error) string {
	ae, isAPI := errors.AsType[*marketo.APIError](err)
	he, isHTTP := errors.AsType[*marketo.HTTPError](err)
	switch {
	case isAPI && ae.Code == "603", isHTTP && (he.Status == http.StatusUnauthorized || he.Status == http.StatusForbidden):
		return "check the API user's asset permissions"
	case errors.Is(err, marketo.ErrDecode):
		return "unexpected response format from Marketo; please report it"
	}
	return err.Error() // warn cleans it
}

// isChange: a is a score, data-value or progression change made by a smart campaign.
func isChange(a marketo.Activity, typeName map[int]string) bool {
	return a.CampaignID > 0 && slices.ContainsFunc(whyTypes, func(w string) bool { return strings.EqualFold(w, strings.TrimSpace(typeName[a.TypeID])) })
}

// campaignNames looks up smart campaign names, at most maxCampaignLookups. A failed
// lookup only warns, once for the run; a quota or ctx error is returned.
func campaignNames(ctx context.Context, client *marketo.Client, ids []int, warn func(string, ...any)) (map[int]string, error) {
	names, failed, err := lookupNames(ctx, ids, maxCampaignLookups, func(id int) (string, error) { return client.SmartCampaign(ctx, id) })
	if lookupFatal(ctx, err) {
		return nil, err
	}
	if failed > 0 {
		warn("could not look up %d of %d campaign names (%s)", failed, len(names)+failed, lookupHint(err))
	}
	if d := len(distinctInts(ids)); d > maxCampaignLookups {
		warn("showing names for %d of %d campaigns; the rest show their id", maxCampaignLookups, d)
	}
	return names, nil
}

// detail is a's timeline detail, plus the smart campaign behind a change: its quoted
// name, or "campaign <id>" when it wasn't looked up.
func detail(a marketo.Activity, typeName map[int]string, campaigns map[int]string) string {
	d := story.Detail(typeName[a.TypeID], a)
	if !isChange(a, typeName) {
		return d
	}
	via := `"` + campaigns[a.CampaignID] + `"`
	if campaigns[a.CampaignID] == "" {
		via = label("", "campaign", a.CampaignID)
	}
	return strings.TrimSpace(d + " via " + via)
}

// pickLead chooses the lead to show and returns the duplicates note, if any.
func pickLead(leads []marketo.Lead, leadID int, by, value string) (marketo.Lead, []string, error) {
	if len(leads) == 0 {
		return nil, nil, exitError{3, fmt.Errorf("no lead found for %s", lookupName(by, value))}
	}
	if leadID != 0 {
		for _, l := range leads {
			if l.ID() == leadID {
				return l, nil, nil
			}
		}
		return nil, nil, exitError{3, fmt.Errorf("lead %d not found for %s", leadID, lookupName(by, value))}
	}
	best := leads[0]
	for _, l := range leads[1:] {
		a, _ := l["updatedAt"].(string) // missing counts as oldest
		b, _ := best["updatedAt"].(string)
		if a > b { // RFC 3339 UTC sorts lexically
			best = l
		}
	}
	if len(leads) == 1 {
		return best, nil, nil
	}
	var others []string
	for _, l := range leads {
		if l.ID() != best.ID() {
			others = append(others, strconv.Itoa(l.ID()))
		}
	}
	return best, []string{fmt.Sprintf("%d leads share this %s; showing %d (most recently updated). Others: %s. Use --lead-id to pick one.",
		len(leads), by, best.ID(), strings.Join(others, ", "))}, nil
}

// tokenValue returns the trimmed value of the first of names that tokens has, matching
// case-insensitively with or without a leading "my.", or "" if none has a value.
func tokenValue(tokens map[string]string, names []string) string {
	bare := func(s string) string {
		s = strings.TrimSpace(s)
		if len(s) > 3 && strings.EqualFold(s[:3], "my.") {
			return s[3:]
		}
		return s
	}
	for _, n := range names {
		for _, k := range slices.Sorted(maps.Keys(tokens)) { // sorted: "Name" and "my.Name" both present picks one stably
			if v := strings.TrimSpace(tokens[k]); v != "" && strings.EqualFold(bare(k), bare(n)) {
				return v
			}
		}
	}
	return ""
}

func label(name, prefix string, id int) string {
	if name != "" {
		return name
	}
	return fmt.Sprintf("%s %d", prefix, id)
}

// cardFields are the profile fields on the call card's second line, in order, with their
// prefixes; the owner follows them. The names are UNVERIFIED on a real instance; correct them here.
var cardFields = []struct{ key, prefix string }{
	{"leadScore", "score "},
	{"leadStatus", ""},
}

// owner is the lead's owner for the card, leads and account: the owner's email where the
// instance has it, else the Salesforce owner's user id.
func owner(l marketo.Lead, loc *time.Location) string {
	if s := display(l["leadOwnerEmailAddress"], loc); s != "" {
		return s
	}
	return display(l["sfdcLeadOwnerId"], loc)
}

// countLabels are the card's singular and plural labels by lower-cased type name; other
// types are counted under their own name.
var countLabels = map[string][2]string{
	"fill out form":                {"form", "forms"},
	"open email":                   {"open", "opens"},
	"click email":                  {"click", "clicks"},
	"email bounced":                {"bounce", "bounces"},
	"email bounced soft":           {"soft bounce", "soft bounces"},
	"unsubscribe email":            {"unsubscribe", "unsubscribes"},
	"change score":                 {"score change", "score changes"},
	"change data value":            {"field change", "field changes"},
	"change status in progression": {"status change", "status changes"},
	"new lead":                     {"new lead", "new leads"},
	"visit webpage":                {"page visit", "page visits"},
	"click link":                   {"link click", "link clicks"},
}

// countOrder breaks ties between equal counts: what a rep acts on first.
var countOrder = []string{"fill out form", "click email", "open email", "change score", "change status in progression",
	"change data value", "visit webpage", "click link", "email bounced", "email bounced soft", "unsubscribe email", "new lead"}

// maxCardCounts keeps the counts line short; the timeline below has the rest.
const maxCardCounts = 5

// cardExtras are the lead fields the call card needs beyond lead_fields.
func cardExtras(cfg *config.Config) []string {
	return append([]string{"id", "updatedAt", "firstName", "lastName", "title", "company"}, cfg.Marketo.ProfileFields...)
}

// leadJSON is the --json lead: lead_fields, plus the card's name, job and profile values when present.
func leadJSON(cfg *config.Config, lead marketo.Lead, loc *time.Location) output.LeadJSON {
	l := output.LeadJSON{ID: lead.ID(), Fields: map[string]any{}}
	for _, k := range cfg.Marketo.LeadFields {
		l.Fields[k] = lead[k]
	}
	for _, k := range []string{"firstName", "lastName", "title", "company"} { // shown on the text card too
		if v := display(lead[k], loc); v != "" {
			l.Fields[k] = lead[k]
		}
	}
	for _, k := range cfg.Marketo.ProfileFields {
		if lead[k] != nil {
			l.Fields[k] = lead[k]
		}
	}
	return l
}

// callCard returns the lines above the timeline: who, profile values, the window's touches
// and the record link. A line with nothing to show is left out.
func callCard(cfg *config.Config, loc *time.Location, lead marketo.Lead, acts []marketo.Activity, typeName map[int]string, days int, now time.Time) []string {
	var card []string
	add := func(sep string, parts ...string) {
		if parts = slices.DeleteFunc(parts, func(p string) bool { return p == "" }); len(parts) > 0 {
			card = append(card, strings.Join(parts, sep))
		}
	}
	v := func(k string) string { return display(lead[k], loc) }
	add(" · ", strings.TrimSpace(v("firstName")+" "+v("lastName")), strings.Join(slices.DeleteFunc([]string{v("title"), v("company")},
		func(p string) bool { return p == "" }), " @ "), fmt.Sprintf("lead %d", lead.ID()))
	var profile []string
	for _, f := range cardFields {
		if s := v(f.key); s != "" {
			profile = append(profile, f.prefix+s)
		}
	}
	if s := owner(lead, loc); s != "" {
		profile = append(profile, "owner "+s)
	}
	add(" · ", profile...)

	sum := story.Summarize(acts, typeName, now)
	var line []string
	if !sum.LastTouch.IsZero() {
		line = append(line, "last touch "+story.Ago(sum.LastTouch.In(loc), now))
	}
	if parts := countParts(sum.Counts); len(parts) > 0 {
		if len(parts) > maxCardCounts {
			parts = append(parts[:maxCardCounts], fmt.Sprintf("+%d more", len(parts)-maxCardCounts))
		}
		line = append(line, fmt.Sprintf("%dd: %s", days, strings.Join(parts, ", ")))
	}
	add(" · ", append(line, "times in "+zoneName(cfg, now))...)
	if u := cfg.Output.LeadURL; strings.Contains(u, "{id}") {
		card = append(card, "open: "+strings.ReplaceAll(u, "{id}", strconv.Itoa(lead.ID())))
	}
	return card
}

func writeActivity(w *bytes.Buffer, card []string, days int, loc *time.Location, acts []marketo.Activity, hidden int,
	typeName, campaigns map[int]string, programs []output.MembershipJSON, olderNote string, notes []string) error {
	for _, l := range card {
		fmt.Fprintln(w, output.Clean(l))
	}
	if len(card) > 0 {
		fmt.Fprintln(w)
	}

	if len(acts) == 0 && hidden > 0 {
		fmt.Fprintf(w, "Nothing to show: every activity in the last %d days is a field change hidden by change_fields.\n", days)
	} else if len(acts) == 0 {
		fmt.Fprintf(w, "No activity in the last %d days.\nMarketo purges some high-volume activity types after a limited period.\n", days)
	} else {
		rows := make([][]string, len(acts))
		for i, a := range acts {
			rows[i] = []string{a.Date.In(loc).Format("2006-01-02 15:04"), label(typeName[a.TypeID], "type", a.TypeID), a.Primary,
				detail(a, typeName, campaigns)}
		}
		var tb bytes.Buffer
		if err := output.Table(&tb, rows); err != nil {
			return err
		}
		for l := range strings.Lines(tb.String()) { // an empty detail would leave padding
			fmt.Fprintln(w, strings.TrimRight(l, " \n"))
		}
	}

	fmt.Fprint(w, "\nPrograms\n")
	if len(programs) == 0 {
		fmt.Fprint(w, "  (none)\n")
	}
	rows := make([][]string, len(programs))
	for i, p := range programs {
		n := label(p.Name, "program", p.ID)
		if p.Title != "" {
			n = p.Title + " (" + n + ")"
		}
		status := p.Status
		if p.AcquiredBy {
			status += " ★ acquired"
		}
		if p.Success {
			status += " ✓ success"
		}
		rows[i] = []string{"  " + n, status}
	}
	var tb bytes.Buffer
	if err := output.Table(&tb, rows); err != nil {
		return err
	}
	i := 0
	for l := range strings.Lines(tb.String()) { // one line per row; the link goes under it, outside the columns
		w.WriteString(l)
		if programs[i].Link != "" {
			fmt.Fprintln(w, "    "+output.Clean(programs[i].Link))
		}
		i++
	}
	if olderNote != "" {
		fmt.Fprintln(w, olderNote)
	}
	if len(notes) > 0 {
		fmt.Fprintln(w)
		for _, n := range notes {
			fmt.Fprintln(w, n)
		}
	}
	return nil
}

func distinctInts(ids []int) map[int]bool {
	m := map[int]bool{}
	for _, id := range ids {
		m[id] = true
	}
	return m
}

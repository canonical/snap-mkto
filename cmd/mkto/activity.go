// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/canonical/snap-mkto/internal/marketo"
	"github.com/canonical/snap-mkto/internal/output"
)

const maxProgramLookups = 25

func newActivityCmd(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "activity <email>",
		Short: "Show a lead's activity timeline and programs",
		Long: "Show a lead's activity timeline and program memberships.\n\n" +
			"Marketo purges some high-volume activity types after a limited period, so an empty result for an older window isn't necessarily an error.",
		Args: func(c *cobra.Command, a []string) error { return usageErr(cobra.ExactArgs(1)(c, a)) },
		RunE: func(cmd *cobra.Command, args []string) error {
			return runActivity(cmd, env, strings.TrimSpace(args[0]))
		},
	}
	f := cmd.Flags()
	f.Int("days", 30, "window size in days (at least 1)")
	f.String("types", "", "comma-separated activity type names (overrides activity_types)")
	f.Bool("all-types", false, "fetch every activity type (uses more API quota)")
	f.Int("lead-id", 0, "pick this lead when several share the email")
	f.String("out", "", "write the output to FILE (mode 0600) instead of stdout")
	return cmd
}

func runActivity(cmd *cobra.Command, env Env, email string) error {
	f := cmd.Flags()
	days, _ := f.GetInt("days")
	typesFlag, _ := f.GetString("types")
	allTypes, _ := f.GetBool("all-types")
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

	cfg, cfgPath, client, err := newClient(cmd, env)
	if err != nil {
		return err
	}
	loc, err := cfg.Location()
	if err != nil {
		return err
	}
	ctx := cmd.Context()

	fields := slices.Clone(cfg.Marketo.LeadFields)
	for _, k := range []string{"id", "updatedAt", "title", "company"} {
		if !slices.Contains(fields, k) {
			fields = append(fields, k)
		}
	}
	leads, err := client.LeadsByEmail(ctx, email, fields)
	if err != nil {
		return mapErr(err, cfg, cfgPath)
	}
	lead, notes, err := pickLead(leads, leadID, email)
	if err != nil {
		return err
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
		names := cfg.Marketo.ActivityTypes
		if f.Changed("types") {
			names = strings.Split(typesFlag, ",")
		}
		var unknown []string
		typeIDs, unknown = marketo.ResolveTypes(all, names)
		for _, n := range unknown {
			warn("unknown activity type %q skipped; check activity_types in %s or --types", strings.TrimSpace(n), cfgPath)
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
	names := map[int]string{} // successful lookups, cached per run
	attempted := map[int]bool{}
	for _, m := range members {
		if attempted[m.ProgramID] {
			continue
		}
		if len(attempted) == maxProgramLookups {
			break
		}
		attempted[m.ProgramID] = true
		p, err := client.Program(ctx, m.ProgramID)
		switch {
		case err == nil:
			names[m.ProgramID] = p.Name
		case errors.Is(err, marketo.ErrQuotaExhausted) || ctx.Err() != nil:
			return mapErr(err, cfg, cfgPath)
		default: // names are cosmetic: degrade to "program <id>"
			warn("could not look up program %d (%v); check the API user's asset permissions", m.ProgramID, err)
		}
	}
	distinct := map[int]bool{}
	for _, m := range members {
		distinct[m.ProgramID] = true
	}
	if len(distinct) > maxProgramLookups {
		notes = append(notes, fmt.Sprintf("showing names for %d of %d programs", maxProgramLookups, len(distinct)))
	}

	var buf bytes.Buffer
	if asJSON {
		res := output.ActivityJSON{
			Lead:     output.LeadJSON{ID: lead.ID(), Fields: map[string]any{}},
			Since:    since,
			Events:   []output.EventJSON{},
			Programs: []output.MembershipJSON{},
			Notes:    append([]string{}, notes...),
		}
		for _, k := range cfg.Marketo.LeadFields {
			res.Lead.Fields[k] = lead[k]
		}
		for _, k := range []string{"title", "company"} { // shown in the text header too
			if v := display(lead[k], loc); v != "" {
				res.Lead.Fields[k] = lead[k]
			}
		}
		for _, a := range acts {
			res.Events = append(res.Events, output.EventJSON{ID: a.ID, Date: a.Date.UTC(), Type: label(typeName[a.TypeID], "type", a.TypeID), Primary: a.Primary, Attributes: a.Attributes})
		}
		for _, m := range members {
			res.Programs = append(res.Programs, output.MembershipJSON{ID: m.ProgramID, Name: names[m.ProgramID], Status: m.Status})
		}
		err = output.WriteJSON(&buf, res)
	} else {
		err = writeActivity(&buf, lead, days, loc, acts, typeName, members, names, notes)
	}
	if err != nil {
		return err
	}
	if outPath == "" {
		_, err = env.Stdout.Write(buf.Bytes())
		return err
	}
	// Tighten the mode before any lead data lands: OpenFile keeps the mode of an existing file.
	file, err := os.OpenFile(outPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(buf.Bytes()); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

// pickLead chooses the lead to show and returns the duplicates note, if any.
func pickLead(leads []marketo.Lead, leadID int, email string) (marketo.Lead, []string, error) {
	if len(leads) == 0 {
		return nil, nil, exitError{3, fmt.Errorf("no lead found for %s", email)}
	}
	if leadID != 0 {
		for _, l := range leads {
			if l.ID() == leadID {
				return l, nil, nil
			}
		}
		return nil, nil, exitError{3, fmt.Errorf("lead %d not found for %s", leadID, email)}
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
	return best, []string{fmt.Sprintf("%d leads share this email; showing %d (most recently updated). Others: %s. Use --lead-id to pick one.",
		len(leads), best.ID(), strings.Join(others, ", "))}, nil
}

func label(name, prefix string, id int) string {
	if name != "" {
		return name
	}
	return fmt.Sprintf("%s %d", prefix, id)
}

func writeActivity(w *bytes.Buffer, lead marketo.Lead, days int, loc *time.Location, acts []marketo.Activity,
	typeName map[int]string, members []marketo.Membership, names map[int]string, notes []string) error {
	head := []string{fmt.Sprintf("Lead %d", lead.ID())}
	for _, k := range []string{"title", "company"} {
		if v := display(lead[k], loc); v != "" {
			head = append(head, v)
		}
	}
	head = append(head, fmt.Sprintf("last %d days", days))
	fmt.Fprintf(w, "%s\n\n", output.Clean(strings.Join(head, " · ")))

	if len(acts) == 0 {
		fmt.Fprintf(w, "No activity in the last %d days.\nMarketo purges some high-volume activity types after a limited period.\n", days)
	} else {
		rows := make([][]string, len(acts))
		for i, a := range acts {
			rows[i] = []string{a.Date.In(loc).Format("2006-01-02 15:04"), label(typeName[a.TypeID], "type", a.TypeID), a.Primary}
		}
		if err := output.Table(w, rows); err != nil {
			return err
		}
	}

	fmt.Fprint(w, "\nPrograms\n")
	if len(members) == 0 {
		fmt.Fprint(w, "  (none)\n")
	}
	rows := make([][]string, len(members))
	for i, m := range members {
		n, ok := names[m.ProgramID]
		if !ok {
			n = fmt.Sprintf("program %d", m.ProgramID)
		}
		rows[i] = []string{"  " + n, m.Status}
	}
	if err := output.Table(w, rows); err != nil {
		return err
	}
	if len(notes) > 0 {
		fmt.Fprintln(w)
		for _, n := range notes {
			fmt.Fprintln(w, n)
		}
	}
	return nil
}

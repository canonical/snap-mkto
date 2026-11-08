// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"os"
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

const (
	maxAccountLeads = 200 // the roster, and with it the activity calls
	accountPages    = 10  // leads.json pages, at up to 300 leads each
)

func newAccountCmd(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "account <sfdcAccountId>",
		Short: "Show everyone at an account: score, status, owner and last touch",
		Long: "Show the leads at an account, highest score first, with each lead's last touch and\n" +
			"touch counts over the window. Leads are found by sfdcAccountId, or by another searchable\n" +
			"field with --by. At most 200 leads are shown, and more than 3000 is an error. Each batch of\n" +
			"30 leads and 4 activity types costs a paging-token call plus one call per page; the 10\n" +
			"default types make 3 batches, run at the same time; --web adds 2 types.",
		Args: func(c *cobra.Command, a []string) error { return usageErr(cobra.ExactArgs(1)(c, a)) },
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAccount(cmd, env, strings.TrimSpace(args[0]))
		},
	}
	f := cmd.Flags()
	f.String("by", "sfdcAccountId", "find the account's leads by this field instead, e.g. a custom domain field")
	f.Int("days", 14, "window size in days (at least 1)")
	f.Bool("web", false, "include Visit Webpage and Click Link (slow on large Marketo instances)")
	f.Bool("csv", false, "output CSV")
	f.String("out", "", "write the output to FILE (mode 0600) instead of stdout")
	f.Bool("force", false, "overwrite an existing --out file")
	return cmd
}

func runAccount(cmd *cobra.Command, env Env, value string) (err error) {
	f := cmd.Flags()
	by, _ := f.GetString("by")
	days, _ := f.GetInt("days")
	asCSV, _ := f.GetBool("csv")
	asJSON, _ := f.GetBool("json")
	outPath, _ := f.GetString("out")
	if err := checkKey(by, value); err != nil {
		return err
	}
	switch {
	case days < 1:
		return exitError{2, errors.New("--days must be at least 1")}
	case asCSV && asJSON:
		return exitError{2, errors.New("--csv and --json can't be used together")}
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
	leads, err := leadsUpTo(cmd.Context(), cmd, env, client, cfg, by, value, cardExtras(cfg), accountPages)
	if errors.Is(err, marketo.ErrTooManyLeads) {
		return exitError{2, fmt.Errorf("more than %d leads match %s; narrow --by (each 300 leads costs one API call)", accountPages*300, lookupName(by, value))}
	}
	if err != nil {
		return mapErr(err, cfg, cfgPath)
	}
	if len(leads) == 0 {
		return exitError{3, fmt.Errorf("no leads found for %s", lookupName(by, value))}
	}
	slices.SortStableFunc(leads, byScore)
	total := len(leads)
	if total > maxAccountLeads {
		leads = leads[:maxAccountLeads]
		fmt.Fprintf(env.Stderr, "note: showing the %d highest-scored of %d leads\n", maxAccountLeads, total)
	}
	ids := make([]int, len(leads))
	for i, l := range leads {
		ids[i] = l.ID()
	}
	web, _ := f.GetBool("web")
	now := env.Now()
	since := now.Add(-time.Duration(days) * 24 * time.Hour).UTC()
	sums, err := leadTouches(cmd, env, cfg, cfgPath, client, ids, since, web)
	if err != nil {
		return err
	}
	slices.SortStableFunc(leads, func(a, b marketo.Lead) int {
		return cmp.Or(byScore(a, b), sums[b.ID()].LastTouch.Compare(sums[a.ID()].LastTouch))
	})

	var buf bytes.Buffer
	if asJSON {
		res := output.AccountJSON{Account: value, By: by, Since: since, Total: total, Leads: []output.LeadJSON{}}
		for _, l := range leads {
			lj := leadJSON(cfg, l, loc)
			lj.LastTouch, lj.Touches = sums[l.ID()].LastTouch.UTC(), sums[l.ID()].Counts
			res.Leads = append(res.Leads, lj)
		}
		err = output.WriteJSON(&buf, res)
	} else {
		if !asCSV {
			fmt.Fprintln(&buf, output.Clean(fmt.Sprintf("Account %s · %s · last %s · times in %s", value,
				count(total, "lead", "leads"), count(days, "day", "days"), zoneName(cfg, now))))
			fmt.Fprintln(&buf)
		}
		rows := [][]string{{"NAME", "TITLE", "SCORE", "STATUS", "OWNER", "LAST TOUCH", strconv.Itoa(days) + "D"}}
		for _, l := range leads {
			name := strings.TrimSpace(display(l["firstName"], loc) + " " + display(l["lastName"], loc))
			if name == "" {
				name = fmt.Sprintf("lead %d", l.ID())
			}
			rows = append(rows, append([]string{name, display(l["title"], loc), display(l["leadScore"], loc),
				display(l["leadStatus"], loc), owner(l, loc)}, touchCells(sums[l.ID()], loc, now)...))
		}
		err = writeRows(&buf, rows, asCSV)
	}
	if err != nil {
		return err
	}
	return finishOut(env, out, outPath, buf.Bytes())
}

// byScore orders leads by leadScore, highest first; a missing or non-numeric score is last.
func byScore(a, b marketo.Lead) int {
	sa, errA := strconv.ParseFloat(fmt.Sprint(a["leadScore"]), 64)
	sb, errB := strconv.ParseFloat(fmt.Sprint(b["leadScore"]), 64)
	if (errA == nil) != (errB == nil) {
		if errA == nil {
			return -1
		}
		return 1
	}
	return cmp.Compare(sb, sa)
}

// leadTouches fetches the configured story types (web types only with web) for the leads
// since a time, and summarises each lead's touches by lead id. Each batch of 30 leads and
// 4 types costs a paging token call plus one call per page. No leads, no calls.
func leadTouches(cmd *cobra.Command, env Env, cfg *config.Config, cfgPath string, client *marketo.Client, ids []int, since time.Time, web bool) (map[int]story.Summary, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	ctx := cmd.Context()
	all, err := client.ActivityTypes(ctx)
	if err != nil {
		return nil, mapErr(err, cfg, cfgPath)
	}
	names, note, err := dropWebTypes(cfg.Marketo.ActivityTypes, web)
	if err != nil {
		return nil, err
	}
	if note != "" {
		fmt.Fprintln(env.Stderr, note)
	}
	typeIDs, unknown := marketo.ResolveTypes(all, names)
	warnUnknownTypes(cmd, env, cfg, cfgPath, unknown, func(n string) {
		fmt.Fprintf(env.Stderr, "warning: %s\n", output.Clean(fmt.Sprintf("unknown activity type %q skipped; check activity_types in %s", n, cfgPath)))
	})
	if len(typeIDs) == 0 {
		return nil, fmt.Errorf("no known activity types to fetch; check activity_types in %s", cfgPath)
	}
	typeName := map[int]string{}
	for _, t := range all {
		typeName[t.ID] = t.Name
	}
	acts, err := client.ActivitiesForLeads(ctx, ids, typeIDs, since)
	if err != nil {
		return nil, mapErr(err, cfg, cfgPath)
	}
	byLead := map[int][]marketo.Activity{}
	for _, a := range acts {
		byLead[a.LeadID] = append(byLead[a.LeadID], a)
	}
	sums := map[int]story.Summary{}
	for _, id := range ids {
		sums[id] = story.Summarize(byLead[id], typeName, env.Now())
	}
	return sums, nil
}

// touchCells are the LAST TOUCH and window columns: "2h ago" and the top three counts,
// e.g. "2 forms, 1 click".
func touchCells(s story.Summary, loc *time.Location, now time.Time) []string {
	last := ""
	if !s.LastTouch.IsZero() {
		last = story.Ago(s.LastTouch.In(loc), now)
	}
	parts := countParts(s.Counts)
	return []string{last, strings.Join(parts[:min(3, len(parts))], ", ")}
}

// countParts labels touch counts ("2 forms"), largest first, ties in countOrder.
func countParts(counts map[string]int) []string {
	type count struct {
		n, rank int
		label   string
	}
	var cs []count
	for name, n := range counts {
		l, ok := countLabels[strings.ToLower(name)]
		if !ok {
			l = [2]string{name, name}
		}
		rank := slices.Index(countOrder, strings.ToLower(name))
		if rank < 0 {
			rank = len(countOrder)
		}
		cs = append(cs, count{n, rank, l[min(n-1, 1)]})
	}
	slices.SortFunc(cs, func(a, b count) int { return cmp.Or(b.n-a.n, a.rank-b.rank, strings.Compare(a.label, b.label)) })
	parts := make([]string, len(cs))
	for i, c := range cs {
		parts[i] = fmt.Sprintf("%d %s", c.n, c.label)
	}
	return parts
}

// zoneName names the display zone for "times in …" lines.
func zoneName(cfg *config.Config, now time.Time) string {
	if z := cfg.Output.Timezone; z != "" && z != "Local" {
		return z
	}
	z, _ := now.In(time.Local).Zone() // time.Local's name is "Local": show the zone in effect instead
	return z
}

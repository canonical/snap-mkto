// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/canonical/snap-mkto/internal/config"
	"github.com/canonical/snap-mkto/internal/marketo"
	"github.com/canonical/snap-mkto/internal/output"
	"github.com/canonical/snap-mkto/internal/story"
)

// whyTypes are always fetched: the ledger reads Change Score and Change Data Value, and
// progression changes show up as "near" events (as do the story types fetched with them).
var whyTypes = []string{"Change Score", "Change Data Value", "Change Status in Progression"}

func newWhyCmd(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "why <email|value>",
		Short: "Show the score and status changes behind a lead's MQL or DQ",
		Long: "Show a lead's score and status changes, judged by the [why] rule in the config, and mark\n" +
			"threshold crossings and MQL/DQ changes. \"near\" lists other activity within near_hours of a\n" +
			"change: it is timing only, not proof of what moved the score.",
		Args: func(c *cobra.Command, a []string) error { return usageErr(cobra.ExactArgs(1)(c, a)) },
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWhy(cmd, env, strings.TrimSpace(args[0]))
		},
	}
	f := cmd.Flags()
	f.Int("days", 90, "window size in days (at least 1)")
	f.Bool("web", false, "include Visit Webpage and Click Link (slow on large Marketo instances)")
	f.Int("lead-id", 0, "pick this lead when several match")
	f.String("by", "email", byHelp)
	return cmd
}

// whyRules checks the [why] section, naming every key to fix, before any quota is spent.
func whyRules(w config.Why, path string) (story.Rules, error) {
	var keys []string
	if strings.TrimSpace(w.ScoreField) == "" {
		keys = append(keys, "score_field")
	}
	if w.MQLThreshold <= 0 {
		keys = append(keys, "mql_threshold (a whole number above 0)")
	}
	if strings.TrimSpace(w.StatusField) == "" {
		keys = append(keys, "status_field")
	}
	if len(w.MQLValues) == 0 {
		keys = append(keys, "mql_values")
	}
	if w.NearHours < 0 {
		keys = append(keys, "near_hours (a whole number of hours; 0 or unset means 2)")
	}
	if len(keys) > 0 {
		return story.Rules{}, exitError{2, fmt.Errorf("mkto why needs the [why] section in %s: set %s. Marketing owns these values; "+
			"`mkto fields` (LABEL column) gives the field labels, and the template `mkto init` writes has a commented example", path, strings.Join(keys, ", "))}
	}
	return story.Rules{ScoreField: w.ScoreField, Threshold: w.MQLThreshold, StatusField: w.StatusField,
		MQLValues: w.MQLValues, DQValues: w.DQValues, Near: time.Duration(w.NearHours) * time.Hour}, nil
}

func runWhy(cmd *cobra.Command, env Env, value string) error {
	f := cmd.Flags()
	by, _ := f.GetString("by")
	if err := checkKey(by, value); err != nil {
		return err
	}
	days, _ := f.GetInt("days")
	leadID, _ := f.GetInt("lead-id")
	web, _ := f.GetBool("web")
	asJSON, _ := f.GetBool("json")
	if days < 1 {
		return exitError{2, errors.New("--days must be at least 1")}
	}
	cfg, cfgPath, client, err := newClient(cmd, env)
	if err != nil {
		return err
	}
	rules, err := whyRules(cfg.Why, cfgPath)
	if err != nil {
		return err
	}
	loc, err := cfg.Location()
	if err != nil {
		return err
	}
	if days > 90 {
		fmt.Fprintln(env.Stderr, "note: Marketo may purge score and data-value changes after a retention period; older changes can be missing")
	}
	warn := func(format string, a ...any) {
		fmt.Fprintf(env.Stderr, "warning: %s\n", output.Clean(fmt.Sprintf(format, a...)))
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
	if !asJSON {
		for _, n := range notes {
			fmt.Fprintf(env.Stderr, "note: %s\n", output.Clean(n))
		}
		notes = nil
	}

	all, err := client.ActivityTypes(ctx)
	if err != nil {
		return mapErr(err, cfg, cfgPath)
	}
	// The change types come first, so the web filter never leaves nothing to fetch.
	names, note, err := dropWebTypes(append(slices.Clone(whyTypes), cfg.Marketo.ActivityTypes...), web)
	if err != nil {
		return err
	}
	if note != "" {
		fmt.Fprintln(env.Stderr, note)
	}
	typeIDs, unknown := marketo.ResolveTypes(all, names)
	var others []string
	seen := map[string]bool{} // a change type also listed in activity_types warns once
	for _, n := range unknown {
		if k := strings.ToLower(strings.TrimSpace(n)); seen[k] {
			continue
		} else {
			seen[k] = true
		}
		if slices.ContainsFunc(whyTypes, func(w string) bool { return strings.EqualFold(w, strings.TrimSpace(n)) }) {
			warn("activity type %q is not on this instance; mkto why can't show those changes (see mkto types)", n)
		} else {
			others = append(others, n)
		}
	}
	warnUnknownTypes(cmd, env, cfg, cfgPath, others, func(n string) {
		if s := closestType(all, n); s != "" {
			warn("unknown activity type %q skipped; did you mean %q? (check activity_types in %s; see mkto types)", n, s, cfgPath)
			return
		}
		warn("unknown activity type %q skipped; check activity_types in %s", n, cfgPath)
	})
	if len(typeIDs) == 0 {
		return fmt.Errorf("no known activity types to fetch; check activity_types in %s", cfgPath)
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
	steps, marks := story.Build(acts, typeName, rules)
	var campaignIDs []int // the ledger's, plus the near events' that only --json shows
	for _, s := range steps {
		if s.CampaignID > 0 {
			campaignIDs = append(campaignIDs, s.CampaignID)
		}
	}
	for _, m := range marks {
		for _, a := range append(slices.Clone(m.Near), m.Before...) {
			if asJSON && isChange(a, typeName) {
				campaignIDs = append(campaignIDs, a.CampaignID)
			}
		}
	}
	campaigns, err := campaignNames(ctx, client, campaignIDs, warn)
	if err != nil {
		return mapErr(err, cfg, cfgPath)
	}

	var buf bytes.Buffer
	if asJSON {
		res := output.WhyJSON{
			Lead: leadJSON(cfg, lead, loc),
			Rule: output.RuleJSON{ScoreField: rules.ScoreField, Threshold: rules.Threshold, StatusField: rules.StatusField,
				MQLValues: append([]string{}, rules.MQLValues...), DQValues: append([]string{}, rules.DQValues...), NearHours: cfg.Why.NearHours},
			Steps: []output.StepJSON{},
			Marks: []output.MarkJSON{},
			Notes: append([]string{}, notes...),
		}
		for _, s := range steps {
			res.Steps = append(res.Steps, stepJSON(s, campaigns))
		}
		for _, m := range marks {
			mj := output.MarkJSON{Label: m.Label, Step: stepJSON(m.Step, campaigns), Near: []output.EventJSON{}}
			event := func(a marketo.Activity) output.EventJSON { // no raw attributes: they hold IPs, user agents and form fields; activity --json has them
				return output.EventJSON{ID: a.ID, Date: a.Date.UTC(), Type: label(typeName[a.TypeID], "type", a.TypeID), Primary: a.Primary,
					PrimaryID: a.PrimaryID, CampaignID: a.CampaignID, Campaign: campaigns[a.CampaignID], Detail: story.Detail(typeName[a.TypeID], a), Attributes: map[string]any{}}
			}
			for _, a := range m.Near {
				mj.Near = append(mj.Near, event(a))
			}
			for _, a := range m.Before {
				mj.Before = append(mj.Before, event(a))
			}
			res.Marks = append(res.Marks, mj)
		}
		err = output.WriteJSON(&buf, res)
	} else {
		err = writeWhy(&buf, callCard(cfg, loc, lead, changeFiltered(cfg, acts, typeName), typeName, days, env.Now()), rules, days, loc, steps, marks, typeName, campaigns)
	}
	if err != nil {
		return err
	}
	_, err = env.Stdout.Write(buf.Bytes())
	return err
}

func stepJSON(s story.Step, campaigns map[int]string) output.StepJSON {
	return output.StepJSON{Date: s.Date.UTC(), Kind: s.Kind, Field: s.Field, Old: s.Old, New: s.New, Delta: s.Delta,
		Reason: s.Reason, CampaignID: s.CampaignID, Campaign: campaigns[s.CampaignID], ActivityID: s.ActivityID}
}

// writeWhy renders the card, the rule, the ledger, the marks with what happened near them,
// and the latest values. Every line is cleaned: most of it is Marketo text.
func writeWhy(w *bytes.Buffer, card []string, r story.Rules, days int, loc *time.Location, steps []story.Step, marks []story.Mark, typeName, campaigns map[int]string) error {
	line := func(s string) { fmt.Fprintln(w, output.Clean(s)) }
	for _, l := range card {
		line(l)
	}
	rule := fmt.Sprintf("rule: %s ≥ %d · MQL when %s in [%s]", r.ScoreField, r.Threshold, r.StatusField, strings.Join(r.MQLValues, ", "))
	if len(r.DQValues) > 0 {
		rule += fmt.Sprintf(" · DQ in [%s]", strings.Join(r.DQValues, ", "))
	}
	fmt.Fprintln(w)
	line(rule)
	fmt.Fprintln(w)

	date := func(t time.Time) string { return t.In(loc).Format("2006-01-02 15:04") }
	var score, status string
	if len(steps) == 0 {
		line(fmt.Sprintf("No score or %s changes in the last %d days.", r.StatusField, days))
	} else {
		rows := [][]string{{"DATE", "FIELD", "CHANGE", "Δ", "REASON", "CAMPAIGN"}}
		for _, s := range steps {
			d, campaign := "", "-"
			if s.Kind == "score" {
				score = s.New
				if s.Delta != 0 {
					d = fmt.Sprintf("%+d", s.Delta)
				}
			} else {
				status = s.New
			}
			if s.CampaignID != 0 {
				campaign = label(campaigns[s.CampaignID], "campaign", s.CampaignID)
			}
			rows = append(rows, []string{date(s.Date), s.Field, s.Old + "→" + s.New, d, story.Cut(s.Reason, maxWhyText), story.Cut(campaign, maxWhyText)})
		}
		if err := output.Table(w, rows); err != nil {
			return err
		}
	}
	for i, m := range marks {
		if i == 0 {
			fmt.Fprintln(w)
		}
		line(date(m.Step.Date) + "  " + m.Label)
		for _, a := range m.Near {
			line(strings.TrimRight("  near: "+date(a.Date)+" "+label(typeName[a.TypeID], "type", a.TypeID)+" "+a.Primary, " "))
		}
		for _, a := range m.Before {
			line(strings.TrimRight("  before: "+date(a.Date)+" "+label(typeName[a.TypeID], "type", a.TypeID)+" "+a.Primary, " ") +
				" (" + before(m.Step.Date.Sub(a.Date)) + " before)")
		}
	}
	fmt.Fprintln(w)
	line(fmt.Sprintf("now: %s %s, %s %s", r.ScoreField, or(score), r.StatusField, or(status)))
	return nil
}

// maxWhyText caps REASON and CAMPAIGN in the text ledger (Marketo reasons run to about
// 110 characters); --json keeps them whole.
const maxWhyText = 60

// before says how long before a mark an activity was: whole hours under a day, else days.
func before(d time.Duration) string {
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", d/time.Hour)
	}
	return fmt.Sprintf("%dd", d/(24*time.Hour))
}

// or shows an empty latest value as "?".
func or(s string) string {
	if s == "" {
		return "?"
	}
	return s
}

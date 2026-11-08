// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/canonical/snap-mkto/internal/marketo"
	"github.com/canonical/snap-mkto/internal/marketotest"
	"github.com/canonical/snap-mkto/internal/output"
	"github.com/canonical/snap-mkto/internal/story"
)

// whyConf is a [why] section matching the labels in activities_story_page.json.
const whyConf = "[why]\nscore_field = \"Behavior Score\"\nmql_threshold = 80\nstatus_field = \"Lead Status\"\n" +
	"mql_values = [\"MQL\"]\ndq_values = [\"Disqualified\", \"Recycled\"]\n"

func whyServer(t *testing.T) *marketotest.Server {
	t.Helper()
	s := storyServer(t)
	s.Handle(leadsPath, marketotest.Response{Body: "leads_one_scored.json"})
	// No "Lead Status" label, so why reads status from the Change Data Value rows.
	return s
}

func why(t *testing.T, s *marketotest.Server, conf string, args ...string) (string, string, int) {
	t.Helper()
	return fakeCLIFull(t, s, fixedNow, "", conf, nil, append([]string{"why", "jane.doe@example.com"}, args...)...)
}

func TestWhyNoConfigExit2(t *testing.T) {
	for name, c := range map[string]struct{ conf, want string }{
		"absent":     {"", "set score_field, mql_threshold (a whole number above 0), status_field, mql_values. "},
		"partial":    {"[why]\nscore_field = \"Behavior Score\"\nmql_values = [\"MQL\"]\n", "set mql_threshold (a whole number above 0), status_field. "},
		"zero":       {strings.Replace(whyConf, "= 80", "= 0", 1), "set mql_threshold (a whole number above 0)"},
		"negative":   {strings.Replace(whyConf, "= 80", "= -5", 1), "set mql_threshold (a whole number above 0)"},
		"near hours": {whyConf + "near_hours = -1\n", "set near_hours (a whole number of hours; 0 or unset means 2)"},
	} {
		s := whyServer(t)
		out, errOut, code := why(t, s, c.conf)
		if code != 2 || out != "" || !strings.Contains(errOut, c.want) || !strings.Contains(errOut, "config.toml") ||
			!strings.Contains(errOut, "[why]") {
			t.Errorf("%s: exit %d\nstdout:\n%s\nstderr:\n%s", name, code, out, errOut)
		}
		if m := s.Methods(); len(m) != 0 {
			t.Errorf("%s: spent quota before failing: %v", name, m)
		}
	}
}

func TestWhyGolden(t *testing.T) {
	s := whyServer(t)
	out, errOut, code := why(t, s, whyConf)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	golden(t, "why.txt", out)
	// Change Score, Change Data Value and Change Status in Progression plus the default story types, in 3 batches.
	if ids := actTypeIDs(s); len(ids) != 10 || !slices.Contains(ids, "100") || !slices.Contains(ids, "13") || !slices.Contains(ids, "22") || !slices.Contains(ids, "2") {
		t.Errorf("activityTypeIds = %v", ids)
	}
	if s.Hits(actsPath) != 3 {
		t.Errorf("activities hits = %d, want 3", s.Hits(actsPath))
	}
	for _, w := range []string{"caused", "because"} {
		if strings.Contains(strings.ToLower(out), w) {
			t.Errorf("output says %q:\n%s", w, out)
		}
	}
	for _, p := range storyPII {
		if strings.Contains(out, p) {
			t.Errorf("text output contains PII value %q", p)
		}
	}

	js, _, code := why(t, whyServer(t), whyConf, "--json")
	if code != 0 {
		t.Fatalf("--json exit %d", code)
	}
	golden(t, "why.json", js)
	for _, p := range storyPII {
		if strings.Contains(js, p) {
			t.Errorf("--json contains PII value %q", p)
		}
	}
	var res output.WhyJSON
	if err := json.Unmarshal([]byte(js), &res); err != nil {
		t.Fatal(err)
	}
	var labels []string
	for _, m := range res.Marks {
		labels = append(labels, m.Label)
	}
	if !slices.Equal(labels, []string{"MQL", "crossed 80", "dropped below 80", "DQ"}) || len(res.Steps) != 4 {
		t.Errorf("labels = %v, steps = %d", labels, len(res.Steps))
	}
	if js2, _, _ := why(t, whyServer(t), whyConf, "--json", "--days", "1"); strings.Contains(js2, "null") {
		t.Errorf("null in --json:\n%s", js2)
	}
}

func TestWhyNoChanges(t *testing.T) {
	s := whyServer(t)
	s.Handle(actsPath, marketotest.Response{Body: `{"requestId":"r","success":true,"result":[],"moreResult":false}`})
	out, _, code := why(t, s, whyConf, "--days", "30")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "\nNo score or Lead Status changes in the last 30 days.\n") || !strings.HasSuffix(out, "\nnow: Behavior Score ?, Lead Status ?\n") ||
		strings.Contains(out, "DATE") {
		t.Errorf("out:\n%s", out)
	}
	js, _, _ := why(t, s, whyConf, "--json")
	var res output.WhyJSON
	if err := json.Unmarshal([]byte(js), &res); err != nil || res.Steps == nil || res.Marks == nil || res.Notes == nil {
		t.Errorf("empty slices must be [], got:\n%s", js)
	}
}

func TestWhyRetentionNote(t *testing.T) {
	const note = "note: Marketo may purge score and data-value changes after a retention period; older changes can be missing\n"
	out, errOut, code := why(t, whyServer(t), whyConf, "--days", "120")
	if code != 0 || !strings.Contains(errOut, note) || strings.Contains(out, "purge") {
		t.Errorf("exit %d\nstderr:\n%s", code, errOut)
	}
	_, errOut, _ = why(t, whyServer(t), whyConf)
	if strings.Contains(errOut, "purge") {
		t.Errorf("note at the default 90 days:\n%s", errOut)
	}
}

func TestDoctorWhyLabels(t *testing.T) {
	// Labels on this instance (describe has "Behavior Score" and "Lead Status"): no why check.
	out, _, code := fakeCLIFull(t, doctorServer(t), time.Now, doctorFields, strings.ToLower(whyConf), nil, "doctor")
	if code != 0 || strings.Contains(out, "why:") || !strings.Contains(out, "✓ names: ") {
		t.Errorf("good labels: exit %d\n%s", code, out)
	}
	// API names instead of labels: a warning with the fix.
	bad := strings.NewReplacer(`"Behavior Score"`, `"behaviorScore__c"`, `"Lead Status"`, `"leadStatus"`).Replace(whyConf)
	out, _, code = fakeCLIFull(t, doctorServer(t), time.Now, doctorFields, bad, nil, "doctor")
	path, _ := tail(t, out)
	want := "! why: no field has this label on this instance: behaviorScore__c, leadStatus. score_field and status_field are labels, " +
		"as in the LABEL column of `mkto fields`; fix them in [why] in " + path + "\n"
	if code != 0 || !strings.HasSuffix(out, want) {
		t.Errorf("bad labels: exit %d\n%s\nwant suffix %q", code, out, want)
	}
	// Without [why], doctor says nothing about it.
	if out, _, _ := doctor(t, doctorServer(t), doctorFields); strings.Contains(out, "why:") {
		t.Errorf("no [why]:\n%s", out)
	}
}

// TestWhyCutsLongText: the text ledger cuts REASON and CAMPAIGN to 60 characters; --json keeps them whole.
func TestWhyCutsLongText(t *testing.T) {
	reason := "Changed by Smart Campaign Run Action Change Data Value 2026-10-05 13:00:00 action Example Nurture Program step"
	campaign := strings.Repeat("Example Very Long Campaign Name ", 3)
	s := story.Step{Date: fixedNow(), Kind: "status", Field: "Lead Status", Old: "Nurture", New: "MQL", Reason: reason, CampaignID: 7}
	var buf bytes.Buffer
	if err := writeWhy(&buf, nil, story.Rules{ScoreField: "Behavior Score", StatusField: "Lead Status"}, 90, time.UTC,
		[]story.Step{s}, nil, nil, map[int]string{7: campaign}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, reason) || !strings.Contains(out, string([]rune(reason)[:59])+"…") ||
		strings.Contains(out, campaign) || !strings.Contains(out, string([]rune(campaign)[:59])+"…") {
		t.Errorf("not cut to 60:\n%s", out)
	}
	if j := stepJSON(s, map[int]string{7: campaign}); j.Reason != reason || j.Campaign != campaign {
		t.Errorf("stepJSON cut: %+v", j)
	}
}

// TestWhyBeforeLines: a mark with nothing near lists what came before it, with how long before.
func TestWhyBeforeLines(t *testing.T) {
	mark := story.Mark{Label: "MQL", Step: story.Step{Date: fixedNow(), Kind: "status", Field: "Lead Status", Old: "Nurture", New: "MQL"},
		Before: []marketo.Activity{
			{ID: 2, Date: fixedNow().Add(-3 * time.Hour), TypeID: 2, Primary: "Contact Us Form"},
			{ID: 1, Date: fixedNow().Add(-50 * time.Hour), TypeID: 10, Primary: "Example\x1b[31m Newsletter"},
		}}
	var buf bytes.Buffer
	if err := writeWhy(&buf, nil, story.Rules{ScoreField: "Behavior Score", StatusField: "Lead Status"}, 90, time.UTC,
		[]story.Step{mark.Step}, []story.Mark{mark}, map[int]string{2: "Fill Out Form", 10: "Open Email"}, nil); err != nil {
		t.Fatal(err)
	}
	d := func(h int) string {
		return fixedNow().Add(-time.Duration(h) * time.Hour).UTC().Format("2006-01-02 15:04")
	}
	out := buf.String()
	for _, w := range []string{"  before: " + d(3) + " Fill Out Form Contact Us Form (3h before)\n",
		"  before: " + d(50) + " Open Email Example\uFFFD[31m Newsletter (2d before)\n"} {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q in:\n%s", w, out)
		}
	}
	if strings.Contains(out, "\x1b") {
		t.Errorf("control character not cleaned:\n%q", out)
	}
}

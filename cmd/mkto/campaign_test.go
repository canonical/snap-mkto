// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/canonical/snap-mkto/internal/marketotest"
	"github.com/canonical/snap-mkto/internal/output"
)

func campaignHits(s *marketotest.Server, ids ...int) (n int) {
	for _, id := range ids {
		n += s.Hits(fmt.Sprintf(campaignFmt, id))
	}
	return n
}

func TestCampaignNameShown(t *testing.T) {
	out, errOut, code := act(t, storyServer(t), "--days", "60")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	for _, w := range []string{
		`Lead Status            Nurture→MQL via "Score Threshold"`,
		`+25 (60→85) Filled out form via "Behavior Scoring"`,
		`→ Attended ✓ success via "Webinar Attendance"`,
		`MQL→Disqualified via "Disqualify Bounced"`,
	} {
		if !strings.Contains(out, w) {
			t.Errorf("activity lacks %q:\n%s", w, out)
		}
	}
	if strings.Contains(errOut, "warning") {
		t.Errorf("stderr: %s", errOut)
	}

	js, _, _ := act(t, storyServer(t), "--days", "60", "--json")
	var res output.ActivityJSON
	if err := json.Unmarshal([]byte(js), &res); err != nil {
		t.Fatal(err)
	}
	got := map[int]string{}
	for _, e := range res.Events {
		got[e.ID] = e.Campaign
	}
	if got[6002] != "Score Threshold" || got[6004] != "Behavior Scoring" || got[6007] != "Webinar Attendance" || got[6012] != "Disqualify Bounced" || got[6003] != "" {
		t.Errorf("event campaigns = %v", got)
	}

	out, _, code = why(t, whyServer(t), whyConf)
	if code != 0 || !strings.Contains(out, "Changed by smart campaign Score Threshold     Score Threshold\n") ||
		!strings.Contains(out, "Filled out form                               Behavior Scoring\n") {
		t.Errorf("why CAMPAIGN column, exit %d:\n%s", code, out)
	}
	js, _, _ = why(t, whyServer(t), whyConf, "--json")
	var w output.WhyJSON
	if err := json.Unmarshal([]byte(js), &w); err != nil {
		t.Fatal(err)
	}
	if len(w.Steps) != 4 || w.Steps[0].Campaign != "Score Threshold" || w.Steps[0].CampaignID != 3001 || w.Steps[3].Campaign != "Disqualify Bounced" {
		t.Errorf("why steps = %+v", w.Steps)
	}
}

// changeScoreRows is an activities page of Change Score rows with these campaign ids.
func changeScoreRows(campaigns ...int) string {
	var rows []string
	for i, c := range campaigns {
		rows = append(rows, fmt.Sprintf(`{"id":%d,"marketoGUID":"%d","leadId":1001,"activityDate":"2026-11-01T09:00:00Z","activityTypeId":100,"campaignId":%d,"primaryAttributeValue":"Behavior Score","attributes":[]}`, 7000+i, 7000+i, c))
	}
	return `{"success":true,"result":[` + strings.Join(rows, ",") + `],"moreResult":false}`
}

func TestCampaignLookupCached(t *testing.T) {
	// 3002 is on two rows (6004, 6011): one lookup.
	s := storyServer(t)
	if _, _, code := act(t, s, "--days", "60"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if n := campaignHits(s, 3002); n != 1 {
		t.Errorf("3002 lookups = %d, want 1", n)
	}

	// 30 distinct campaigns: 25 lookups, the rest show their id.
	s = storyServer(t)
	var ids []int
	for i := range 30 {
		ids = append(ids, 5000+i)
		s.Handle(fmt.Sprintf(campaignFmt, 5000+i), campaignBody(5000+i, fmt.Sprint("C", 5000+i)))
	}
	s.Handle(actsPath, marketotest.Response{Body: changeScoreRows(append(ids, 5000)...)})
	out, errOut, code := act(t, s)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	if n := campaignHits(s, ids...); n != 25 || s.Hits(fmt.Sprintf(campaignFmt, 5025)) != 0 {
		t.Errorf("lookups = %d, want 25", n)
	}
	if !strings.Contains(out, `via "C5024"`) || !strings.Contains(out, "via campaign 5025") || !strings.Contains(errOut, "warning: showing names for 25 of 30 campaigns; the rest show their id\n") {
		t.Errorf("out:\n%s\nstderr:\n%s", out, errOut)
	}
}

func TestCampaign603FailsSoft(t *testing.T) {
	s := storyServer(t)
	s.Handle(fmt.Sprintf(campaignFmt, 3002), marketotest.Response{Body: "error_603.json"})
	s.Handle(fmt.Sprintf(campaignFmt, 3004), marketotest.Response{Body: "error_603.json"})
	out, errOut, code := act(t, s, "--days", "60")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	if !strings.Contains(out, "Filled out form via campaign 3002") || !strings.Contains(out, `via "Score Threshold"`) {
		t.Errorf("out:\n%s", out)
	}
	if want := "warning: could not look up 2 of 4 campaign names (check the API user's asset permissions)\n"; strings.Count(errOut, "warning") != 1 || !strings.Contains(errOut, want) {
		t.Errorf("stderr:\n%s", errOut)
	}

	s = whyServer(t)
	s.Handle(fmt.Sprintf(campaignFmt, 3002), marketotest.Response{Body: "error_603.json"})
	out, errOut, code = why(t, s, whyConf)
	// The ledger's campaigns only: 3003 (progression, near no mark) isn't looked up.
	if code != 0 || !strings.Contains(out, "Filled out form                               campaign 3002\n") ||
		!strings.Contains(errOut, "warning: could not look up 1 of 3 campaign names (") || campaignHits(s, 3003) != 0 {
		t.Errorf("why exit %d:\n%s\nstderr:\n%s", code, out, errOut)
	}

	// Quota ends the run.
	s = storyServer(t)
	s.Handle(fmt.Sprintf(campaignFmt, 3001), marketotest.Response{Body: "error_607.json"})
	if _, _, code := act(t, s, "--days", "60"); code != 4 {
		t.Errorf("quota: exit %d, want 4", code)
	}
}

func TestCampaignNoIDsNoCalls(t *testing.T) {
	// activities_final_page.json has no campaignId; a Fill Out Form row with one isn't a change.
	s := activityServer(t)
	if _, _, code := act(t, s); code != 0 {
		t.Fatalf("exit %d", code)
	}
	s = storyServer(t)
	s.Handle(actsPath, marketotest.Response{Body: `{"success":true,"result":[{"id":7001,"marketoGUID":"7001","leadId":1001,"activityDate":"2026-11-01T09:00:00Z","activityTypeId":2,"campaignId":3001,"primaryAttributeValue":"Contact Us Form","attributes":[]}],"moreResult":false}`})
	if _, _, code := act(t, s); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if n := campaignHits(s, 3001, 3002, 3003, 3004); n != 0 {
		t.Errorf("campaign lookups = %d, want 0", n)
	}
}

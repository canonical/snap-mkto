// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/canonical/snap-mkto/internal/marketotest"
)

const acct = "001EXAMPLE000001AAA"

// accountPage1 and accountPage2 are one account over two leads.json pages: equal scores
// (tie broken by last touch), a non-numeric score and a lead with no name.
const (
	accountPage1 = `{"success":true,"moreResult":true,"nextPageToken":"EXAMPLELEADPAGE2====","result":[
{"id":1001,"email":"jane.doe@example.com","firstName":"Jane","lastName":"Doe","title":"CTO","leadScore":40,"leadStatus":"MQL","leadOwnerEmailAddress":"owner@example.com"},
{"id":1002,"email":"john.roe@example.com","firstName":"John","lastName":"Roe","title":"=HYPERLINK(1)","leadScore":85,"leadStatus":"Nurture"}]}`
	accountPage2 = `{"success":true,"moreResult":false,"result":[
{"id":1003,"email":"sam.poe@example.com","firstName":"Sam","lastName":"Poe","leadScore":40,"leadStatus":"New"},
{"id":1004,"email":"no.name@example.com","leadScore":"n/a"}]}`
	// 1003 touched after 1001, so it ranks above it on an equal score; 1002's Email Delivered is newer than its
	// form fill but is not a touch.
	accountActs = `{"success":true,"moreResult":false,"result":[
{"id":1,"leadId":1001,"activityDate":"2026-11-07T11:00:00Z","activityTypeId":11,"primaryAttributeValue":"Newsletter"},
{"id":2,"leadId":1001,"activityDate":"2026-11-05T09:00:00Z","activityTypeId":2,"primaryAttributeValue":"Contact Us"},
{"id":3,"leadId":1001,"activityDate":"2026-11-04T09:00:00Z","activityTypeId":2,"primaryAttributeValue":"Webinar"},
{"id":4,"leadId":1001,"activityDate":"2026-11-03T09:00:00Z","activityTypeId":10,"primaryAttributeValue":"Newsletter"},
{"id":5,"leadId":1001,"activityDate":"2026-11-02T09:00:00Z","activityTypeId":100,"primaryAttributeValue":"Behavior Score"},
{"id":6,"leadId":1002,"activityDate":"2026-10-20T09:00:00Z","activityTypeId":2,"primaryAttributeValue":"Contact Us"},
{"id":7,"leadId":1002,"activityDate":"2026-11-06T09:00:00Z","activityTypeId":7,"primaryAttributeValue":"Newsletter"},
{"id":8,"leadId":1003,"activityDate":"2026-11-07T11:30:00Z","activityTypeId":10,"primaryAttributeValue":"Newsletter"}]}`
)

func accountServer(t *testing.T, leads ...marketotest.Response) *marketotest.Server {
	t.Helper()
	s := marketotest.New(t)
	s.Handle(leadsPath, leads...)
	s.Handle(typesPath, marketotest.Response{Body: "activity_types.json"})
	s.Handle(tokenPath, marketotest.Response{Body: "pagingtoken_ok.json"})
	s.Handle(actsPath, marketotest.Response{Body: accountActs})
	return s
}

func account(t *testing.T, s *marketotest.Server, args ...string) (string, string, int) {
	t.Helper()
	return fakeCLINow(t, s, fixedNow, append([]string{"account", acct}, args...)...)
}

// manyLeads is a leads.json page of n leads, ids 2001…, scores 1…n, emails a0@example.com….
func manyLeads(n int) string {
	rows := make([]string, n)
	for i := range n {
		rows[i] = fmt.Sprintf(`{"id":%d,"email":"a%d@example.com","firstName":"A%d","leadScore":%d}`, 2001+i, i, i, i+1)
	}
	return `{"success":true,"result":[` + strings.Join(rows, ",") + `]}`
}

func TestAccountRosterGolden(t *testing.T) {
	s := accountServer(t, marketotest.Response{Body: accountPage1}, marketotest.Response{Body: accountPage2})
	out, errOut, code := account(t, s)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	golden(t, "account.txt", out)
	if q := s.Query(leadsPath); q.Get("filterType") != "sfdcAccountId" || q.Get("filterValues") != acct || q.Get("nextPageToken") == "" {
		t.Errorf("leads query: %v", q)
	}
	if got := actLeadIDs(s); !slices.Equal(got, []string{"1002,1001,1003,1004"}) {
		t.Errorf("leadIds = %q", got)
	}
	if s.Hits(actsPath) != 3 { // 10 default types in batches of at most 4
		t.Errorf("activities hits = %d, want 3", s.Hits(actsPath))
	}

	s = accountServer(t, marketotest.Response{Body: accountPage1}, marketotest.Response{Body: accountPage2})
	out, _, _ = account(t, s, "--csv")
	golden(t, "account.csv", out)

	s = accountServer(t, marketotest.Response{Body: accountPage1}, marketotest.Response{Body: accountPage2})
	out, _, _ = account(t, s, "--json")
	golden(t, "account.json", out)
	var js struct {
		Total int `json:"total"`
		Leads []struct {
			ID int `json:"id"`
		} `json:"leads"`
	}
	if err := json.Unmarshal([]byte(out), &js); err != nil || js.Total != 4 || len(js.Leads) != 4 || js.Leads[0].ID != 1002 {
		t.Errorf("json: %v, %+v", err, js)
	}

	s = accountServer(t, marketotest.Response{Body: accountPage1}, marketotest.Response{Body: accountPage2})
	outPath := filepath.Join(t.TempDir(), "account.txt")
	if _, errOut, code = account(t, s, "--out", outPath); code != 0 || !strings.Contains(errOut, "wrote ") {
		t.Fatalf("--out: exit %d: %s", code, errOut)
	}
	if b, _ := os.ReadFile(outPath); !strings.HasPrefix(string(b), "Account "+acct) {
		t.Errorf("--out file:\n%s", b)
	}
	if fi, err := os.Stat(outPath); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("--out mode: %v %v", fi.Mode(), err)
	}
}

func TestAccountActivitiesBatched(t *testing.T) {
	for _, c := range []struct {
		args []string
		want int // 31 leads: 2 lead batches per type batch; 10 types (12 with --web) make 3 type batches
	}{{nil, 6}, {[]string{"--web"}, 6}} {
		s := accountServer(t, marketotest.Response{Body: manyLeads(31)})
		_, errOut, code := account(t, s, c.args...)
		if code != 0 {
			t.Fatalf("%v: exit %d: %s", c.args, code, errOut)
		}
		if s.Hits(actsPath) != c.want || s.Hits(tokenPath) != c.want {
			t.Errorf("%v: activities hits %d, pagingtoken hits %d, want %d", c.args, s.Hits(actsPath), s.Hits(tokenPath), c.want)
		}
		if got := actLeadIDs(s); len(got) != 2 || !slices.Contains(got, "2001") { // the lowest score is alone in the last batch
			t.Errorf("%v: lead batches = %q", c.args, got)
		}
		if ids := actTypeIDs(s); slices.Contains(ids, "1") != (c.args != nil) {
			t.Errorf("%v: activityTypeIds = %v", c.args, ids)
		}
	}
}

func TestAccountCapNote(t *testing.T) {
	s := accountServer(t, marketotest.Response{Body: manyLeads(201)})
	out, errOut, code := account(t, s)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(errOut, "note: showing the 200 highest-scored of 201 leads") {
		t.Errorf("stderr:\n%s", errOut)
	}
	if !strings.HasPrefix(out, "Account "+acct+" · 201 leads · ") {
		t.Errorf("header: %q", strings.SplitN(out, "\n", 2)[0])
	}
	if n := strings.Count(out, "@example.com"); n != 0 {
		t.Errorf("roster prints %d emails", n)
	}
	if rows := strings.Count(out, "\n") - 3; rows != 200 { // header, blank, column titles
		t.Errorf("roster rows = %d, want 200", rows)
	}
	if strings.Contains(out, "A0 ") { // score 1, the one left out
		t.Error("the lowest-scored lead is shown")
	}
	if s.Hits(actsPath) != 21 { // 7 lead batches × 3 type batches
		t.Errorf("activities hits = %d, want 21", s.Hits(actsPath))
	}
}

func TestAccountNotFound(t *testing.T) {
	s := marketotest.New(t)
	s.Handle(leadsPath, marketotest.Response{Body: "leads_zero.json"})
	out, errOut, code := account(t, s)
	if code != 3 || out != "" || errOut != "no leads found for sfdcAccountId "+acct+"\n" {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	_, errOut, _ = fakeCLINow(t, s, fixedNow, "account", "example.com", "--by", "domain")
	if !strings.Contains(errOut, "no leads found for domain example.com") {
		t.Errorf("--by: stderr %q", errOut)
	}
	if q := s.Query(leadsPath); q.Get("filterType") != "domain" {
		t.Errorf("--by: filterType %q", q.Get("filterType"))
	}
}

func TestAccountTooManyLeads(t *testing.T) {
	s := marketotest.New(t)
	pages := make([]marketotest.Response, 11)
	for i := range pages {
		pages[i] = marketotest.Response{Body: fmt.Sprintf(`{"success":true,"moreResult":true,"nextPageToken":"EXAMPLEPAGE%d","result":[{"id":%d,"leadScore":1}]}`, i, 3001+i)}
	}
	s.Handle(leadsPath, pages...)
	out, errOut, code := account(t, s)
	if code != 2 || out != "" || errOut != "more than 3000 leads match sfdcAccountId "+acct+"; narrow --by (each 300 leads costs one API call)\n" {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	if s.Hits(leadsPath) != 10 {
		t.Errorf("leads.json hits = %d, want 10", s.Hits(leadsPath))
	}
}

func TestAccountOutRemovedOnFailure(t *testing.T) {
	for _, force := range []bool{false, true} {
		s := marketotest.New(t)
		s.Handle(leadsPath, marketotest.Response{Body: "leads_zero.json"})
		p := filepath.Join(t.TempDir(), "out.txt")
		args := []string{"--out", p}
		if force {
			args = append(args, "--force")
		}
		if _, _, code := account(t, s, args...); code != 3 {
			t.Errorf("force=%v: exit %d", force, code)
		}
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("force=%v: file left behind: %v", force, err)
		}
	}
}

func TestAccountOutForce(t *testing.T) {
	p := filepath.Join(t.TempDir(), "out.txt")
	if err := os.WriteFile(p, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A failed --force run leaves the target as it was, mode included, and no temp file.
	s := accountServer(t, marketotest.Response{Body: accountPage1}, marketotest.Response{Body: accountPage2})
	s.Handle(actsPath, marketotest.Response{Status: http.StatusInternalServerError})
	if _, _, code := account(t, s, "--out", p, "--force"); code == 0 {
		t.Fatal("exit 0")
	}
	if b, _ := os.ReadFile(p); string(b) != "old" {
		t.Errorf("file changed on failure: %q", b)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o644 {
		t.Errorf("mode changed on failure: %v", fi.Mode())
	}
	if ents, _ := os.ReadDir(filepath.Dir(p)); len(ents) != 1 {
		t.Errorf("temp file left behind: %v", ents)
	}

	s = accountServer(t, marketotest.Response{Body: accountPage1}, marketotest.Response{Body: accountPage2})
	if _, errOut, code := account(t, s, "--out", p, "--force"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	s = accountServer(t, marketotest.Response{Body: accountPage1}, marketotest.Response{Body: accountPage2})
	want, _, _ := account(t, s)
	if b, _ := os.ReadFile(p); string(b) != want {
		t.Errorf("file differs from stdout:\n%s", b)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", fi.Mode())
	}
	if ents, _ := os.ReadDir(filepath.Dir(p)); len(ents) != 1 {
		t.Errorf("temp file left behind: %v", ents)
	}
}

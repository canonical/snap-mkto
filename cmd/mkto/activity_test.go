// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/canonical/snap-mkto/internal/marketotest"
)

const (
	typesPath  = "/rest/v1/activities/types.json"
	tokenPath  = "/rest/v1/activities/pagingtoken.json"
	actsPath   = "/rest/v1/activities.json"
	membPath   = "/rest/v1/leads/1001/programMembership.json"
	programFmt = "/rest/asset/v1/program/%d.json"
)

var fixedNow = func() time.Time { return time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC) }

func activityServer(t *testing.T) *marketotest.Server {
	t.Helper()
	s := marketotest.New(t)
	s.Handle(leadsPath, marketotest.Response{Body: "leads_one.json"})
	s.Handle(typesPath, marketotest.Response{Body: "activity_types.json"})
	s.Handle(tokenPath, marketotest.Response{Body: "pagingtoken_ok.json"})
	s.Handle(actsPath, marketotest.Response{Body: "activities_final_page.json"})
	s.Handle(membPath, marketotest.Response{Body: "program_membership_one.json"})
	s.Handle(fmt.Sprintf(programFmt, 1001), marketotest.Response{Body: "program_by_id_one.json"})
	return s
}

func act(t *testing.T, s *marketotest.Server, args ...string) (string, string, int) {
	t.Helper()
	return fakeCLINow(t, s, fixedNow, append([]string{"activity", "jane.doe@example.com"}, args...)...)
}

func TestActivityTimelineGolden(t *testing.T) {
	s := activityServer(t)
	out, _, code := act(t, s)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	golden(t, "activity_one.txt", out)
	out, _, _ = act(t, s, "--json")
	golden(t, "activity_one.json", out)
	if m := s.Methods(); slices.ContainsFunc(m, func(x string) bool { return x != http.MethodGet }) {
		t.Errorf("non-GET request: %v", m)
	}
	if got := s.Query(tokenPath).Get("sinceDatetime"); got != "2026-10-08T12:00:00Z" {
		t.Errorf("sinceDatetime = %q", got)
	}
	f := strings.Split(s.Query(leadsPath).Get("fields"), ",")
	for _, k := range []string{"id", "updatedAt", "title", "company", "email"} {
		if !slices.Contains(f, k) {
			t.Errorf("fields %v lack %s", f, k)
		}
	}
}

func TestActivityNoEventsNoPrograms(t *testing.T) {
	s := activityServer(t)
	s.Handle(actsPath, marketotest.Response{Body: "activities_page_empty_more.json"}, marketotest.Response{Body: `{"success":true,"result":[],"moreResult":false}`})
	s.Handle(membPath, marketotest.Response{Body: `{"success":true,"result":[]}`})
	out, _, code := act(t, s, "--days", "7")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	want := "Lead 1001 · last 7 days\n\nNo activity in the last 7 days.\nMarketo purges some high-volume activity types after a limited period.\n\nPrograms\n  (none)\n"
	if out != want {
		t.Errorf("got:\n%s", out)
	}
}

func TestActivityDuplicatesPicksLatest(t *testing.T) {
	s := activityServer(t)
	s.Handle(leadsPath, marketotest.Response{Body: "leads_two_duplicates.json"})
	out, _, code := act(t, s)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	want := "2 leads share this email; showing 1001 (most recently updated). Others: 1002. Use --lead-id to pick one.\n"
	if !strings.HasSuffix(out, want) {
		t.Errorf("out:\n%s", out)
	}
	if got := s.Query(actsPath).Get("leadIds"); got != "1001" {
		t.Errorf("leadIds = %q", got)
	}
}

func TestActivityLeadIDFlag(t *testing.T) {
	s := activityServer(t)
	s.Handle(leadsPath, marketotest.Response{Body: "leads_two_duplicates.json"})
	s.Handle("/rest/v1/leads/1002/programMembership.json", marketotest.Response{Body: `{"success":true,"result":[]}`})
	if _, _, code := act(t, s, "--lead-id", "1002"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if got := s.Query(actsPath).Get("leadIds"); got != "1002" {
		t.Errorf("leadIds = %q", got)
	}
	_, errOut, code := act(t, s, "--lead-id", "9")
	if code != 3 || !strings.Contains(errOut, "lead 9 not found for jane.doe@example.com") {
		t.Errorf("exit %d, stderr %s", code, errOut)
	}
}

func TestActivityDaysOver90Warns(t *testing.T) {
	_, errOut, code := act(t, activityServer(t), "--days", "91")
	if code != 0 || !strings.Contains(errOut, "warning: --days above 90 uses more API quota\n") {
		t.Errorf("exit %d, stderr %s", code, errOut)
	}
}

func TestActivityDaysZeroExit2(t *testing.T) {
	_, _, code := act(t, activityServer(t), "--days", "0")
	if code != 2 {
		t.Errorf("exit %d", code)
	}
}

func TestActivityTypesAndAllTypesExit2(t *testing.T) {
	_, _, code := act(t, activityServer(t), "--types", "Open Email", "--all-types")
	if code != 2 {
		t.Errorf("exit %d", code)
	}
}

func TestActivityAllTypesWarns(t *testing.T) {
	s := activityServer(t)
	_, errOut, code := act(t, s, "--all-types")
	if code != 0 || !strings.Contains(errOut, "warning: --all-types fetches every activity type and uses more API quota\n") {
		t.Errorf("exit %d, stderr %s", code, errOut)
	}
	if got := s.Hits(tokenPath); got != 3 { // 25 types in 3 batches of 10
		t.Errorf("token hits = %d", got)
	}
}

func TestActivityTypesFlagAndUnknownWarns(t *testing.T) {
	s := activityServer(t)
	_, errOut, code := act(t, s, "--types", " Open Email , Nope")
	if code != 0 || !strings.Contains(errOut, `warning: unknown activity type "Nope" skipped; check activity_types in `) {
		t.Errorf("exit %d, stderr %s", code, errOut)
	}
	if got := s.Query(actsPath).Get("activityTypeIds"); got != "10" {
		t.Errorf("activityTypeIds = %q", got)
	}
	_, errOut, code = act(t, s, "--types", "Nope")
	if code != 1 || !strings.Contains(errOut, "no known activity types to fetch; check activity_types in ") {
		t.Errorf("exit %d, stderr %s", code, errOut)
	}
}

func membershipBody(ids ...int) string {
	rows := make([]string, len(ids))
	for i, id := range ids {
		rows[i] = fmt.Sprintf(`{"id":%d,"progressionStatus":"Member"}`, id)
	}
	return `{"success":true,"result":[` + strings.Join(rows, ",") + `]}`
}

func TestMembershipCap25(t *testing.T) {
	s := activityServer(t)
	ids := make([]int, 30)
	for i := range ids {
		ids[i] = 2000 + i
		s.Handle(fmt.Sprintf(programFmt, ids[i]), marketotest.Response{Body: "program_by_id_one.json"})
	}
	s.Handle(membPath, marketotest.Response{Body: membershipBody(ids...)})
	out, _, code := act(t, s)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	n := 0
	for _, id := range ids {
		n += s.Hits(fmt.Sprintf(programFmt, id))
	}
	if n != 25 {
		t.Errorf("program lookups = %d", n)
	}
	if !strings.Contains(out, "showing names for 25 of 30 programs\n") || !strings.Contains(out, "program 2029") {
		t.Errorf("out:\n%s", out)
	}
}

func TestProgramLookupCached(t *testing.T) {
	s := activityServer(t)
	s.Handle(membPath, marketotest.Response{Body: membershipBody(1001, 1001)})
	if _, _, code := act(t, s); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if got := s.Hits(fmt.Sprintf(programFmt, 1001)); got != 1 {
		t.Errorf("hits = %d", got)
	}
}

func TestActivityOutFile(t *testing.T) {
	s := activityServer(t)
	p := filepath.Join(t.TempDir(), "out.txt")
	if err := os.WriteFile(p, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, code := act(t, s, "--out", p)
	if code != 0 || out != "" {
		t.Fatalf("exit %d, stdout %q", code, out)
	}
	fi, err := os.Stat(p)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("stat %v %v", fi, err)
	}
	b, _ := os.ReadFile(p)
	want, _, _ := act(t, s)
	if string(b) != want {
		t.Errorf("file differs from stdout output:\n%s", b)
	}
}

func TestActivityHelpMentionsRetention(t *testing.T) {
	out, _, code := exec(t, nil, "activity", "--help")
	if code != 0 || !strings.Contains(out, "Marketo purges some high-volume activity types after a limited period, so an empty result for an older window isn't necessarily an error.") {
		t.Errorf("exit %d:\n%s", code, out)
	}
}

func TestActivityVerboseLeaksNothing(t *testing.T) {
	_, errOut, code := act(t, activityServer(t), "--verbose")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, bad := range []string{"example.com", "s3cret", "filterValues", "leadIds", "nextPageToken"} {
		if strings.Contains(errOut, bad) {
			t.Errorf("verbose stderr contains %q", bad)
		}
	}
}

func TestActivityProgramLookupFailureDegrades(t *testing.T) {
	s := activityServer(t)
	s.Handle(membPath, marketotest.Response{Body: membershipBody(1001, 1002)})
	s.Handle(fmt.Sprintf(programFmt, 1002), marketotest.Response{Body: `{"success":true,"result":[]}`})
	out, errOut, code := act(t, s)
	if code != 0 {
		t.Fatalf("exit %d, stderr %s", code, errOut)
	}
	if !strings.Contains(out, "Example Webinar 2026") || !strings.Contains(out, "program 1002") {
		t.Errorf("out:\n%s", out)
	}
	if want := "warning: could not look up 1 of 2 program names (Marketo returned no program 1002)\n"; errOut != want {
		t.Errorf("stderr = %q, want %q", errOut, want)
	}
	js, _, _ := act(t, s, "--json")
	if !strings.Contains(js, `"name": ""`) {
		t.Errorf("json lacks empty name:\n%s", js)
	}
}

func TestActivityProgramLookupQuotaStops(t *testing.T) {
	s := activityServer(t)
	s.Handle(fmt.Sprintf(programFmt, 1001), marketotest.Response{Body: "error_607.json"})
	if _, _, code := act(t, s); code != 4 {
		t.Errorf("exit %d", code)
	}
}

func TestActivityDuplicateMissingUpdatedAtLoses(t *testing.T) {
	s := activityServer(t)
	s.Handle(leadsPath, marketotest.Response{Body: `{"success":true,"result":[{"id":1002,"email":"a@example.com"},{"id":1001,"updatedAt":"2026-01-01T00:00:00Z"}]}`})
	if _, _, code := act(t, s); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if got := s.Query(actsPath).Get("leadIds"); got != "1001" {
		t.Errorf("leadIds = %q", got)
	}
}

func TestActivityJSONIncludesTitleCompany(t *testing.T) {
	s := activityServer(t)
	s.Handle(leadsPath, marketotest.Response{Body: `{"success":true,"result":[{"id":1001,"title":"Engineer","company":"Example Corp"}]}`})
	js, _, _ := act(t, s, "--json")
	if !strings.Contains(js, `"title": "Engineer"`) || !strings.Contains(js, `"company": "Example Corp"`) {
		t.Errorf("json:\n%s", js)
	}
}

func TestMembershipsStopsWhenMoreResultFalse(t *testing.T) {
	s := activityServer(t)
	s.Handle(membPath, marketotest.Response{Body: `{"success":true,"result":[{"id":1001,"progressionStatus":"Member"}],"nextPageToken":"T","moreResult":false}`})
	if _, _, code := act(t, s); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if got := s.Hits(membPath); got != 1 {
		t.Errorf("membership hits = %d", got)
	}
}

const evil = `Evil\u001b]52;c;ZXZpbA==\u0007Corp`

func TestTerminalEscapesStripped(t *testing.T) {
	body := `{"success":true,"result":[{"id":1001,"updatedAt":"2026-10-01T12:00:00Z","email":"jane.doe@example.com","firstName":"` + evil + `","company":"` + evil + `","title":"` + evil + `"}]}`
	s := activityServer(t)
	s.Handle(leadsPath, marketotest.Response{Body: body})
	for _, cmd := range []string{"lead", "activity"} {
		out, errOut, code := fakeCLINow(t, s, fixedNow, cmd, "jane.doe@example.com")
		if code != 0 {
			t.Fatalf("%s exit %d", cmd, code)
		}
		for _, o := range []string{out, errOut} {
			if strings.ContainsAny(o, "\x1b\x07") {
				t.Errorf("%s output has control characters: %q", cmd, o)
			}
		}
		if !strings.Contains(out, "Evil�]52") {
			t.Errorf("%s output lacks cleaned value:\n%s", cmd, out)
		}
	}
}

// datedMemberships returns a membership body with membershipDate set for each id.
func datedMemberships(dates map[int]string) string {
	var rows []string
	for _, id := range slices.Sorted(maps.Keys(dates)) {
		rows = append(rows, fmt.Sprintf(`{"id":%d,"progressionStatus":"Member","membershipDate":%q}`, id, dates[id]))
	}
	return `{"success":true,"result":[` + strings.Join(rows, ",") + `]}`
}

func namedProgram(id int) marketotest.Response {
	return marketotest.Response{Body: fmt.Sprintf(`{"success":true,"result":[{"id":%d,"name":"Program %d","createdAt":"2026-01-05T10:00:00Z+0000"}]}`, id, id)}
}

// windowServer has two programs inside the 30-day window (since 2026-10-08 12:00 UTC) and one older.
func windowServer(t *testing.T) *marketotest.Server {
	s := activityServer(t)
	s.Handle(membPath, marketotest.Response{Body: datedMemberships(map[int]string{
		3001: "2026-11-01T01:08:44Z+0000", 3002: "2026-09-01T00:00:00Z+0000", 3003: "2026-11-05T09:00:00Z+0000"})})
	for _, id := range []int{3001, 3002, 3003} {
		s.Handle(fmt.Sprintf(programFmt, id), namedProgram(id))
	}
	return s
}

func TestActivityProgramsWindow(t *testing.T) {
	s := windowServer(t)
	out, errOut, code := act(t, s)
	if code != 0 {
		t.Fatalf("exit %d, stderr %s", code, errOut)
	}
	if want := "\nPrograms\n  Program 3003  Member\n  Program 3001  Member\n+ 1 older programs (use --all-programs)\n"; !strings.HasSuffix(out, want) {
		t.Errorf("out:\n%s", out)
	}
	if got := s.Hits(fmt.Sprintf(programFmt, 3002)); got != 0 {
		t.Errorf("older program looked up %d times", got)
	}
	js, _, _ := act(t, s, "--json")
	for _, want := range []string{`"date": "2026-11-05T09:00:00Z"`, `"notes": [
    "+ 1 older programs (use --all-programs)"`} {
		if !strings.Contains(js, want) {
			t.Errorf("json lacks %s:\n%s", want, js)
		}
	}
	if strings.Contains(js, "3002") || strings.Index(js, `"id": 3003`) > strings.Index(js, `"id": 3001`) {
		t.Errorf("json programs not windowed newest first:\n%s", js)
	}
}

func TestActivityAllPrograms(t *testing.T) {
	s := windowServer(t)
	s.Handle(membPath, marketotest.Response{Body: strings.Replace(datedMemberships(map[int]string{
		3001: "2026-11-01T01:08:44Z+0000", 3002: "2026-09-01T00:00:00Z+0000", 3003: "2026-11-05T09:00:00Z+0000"}),
		`]}`, `,{"id":3004,"progressionStatus":"Member"}]}`, 1)}) // undated: last
	s.Handle(fmt.Sprintf(programFmt, 3004), namedProgram(3004))
	out, _, code := act(t, s, "--all-programs")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if want := "\nPrograms\n  Program 3003  Member\n  Program 3001  Member\n  Program 3002  Member\n  Program 3004  Member\n"; !strings.HasSuffix(out, want) {
		t.Errorf("out:\n%s", out)
	}
}

func TestActivityProgramsNoDatesShowsAll(t *testing.T) {
	s := activityServer(t)
	s.Handle(membPath, marketotest.Response{Body: membershipBody(1001, 1002)})
	s.Handle(fmt.Sprintf(programFmt, 1002), namedProgram(1002))
	out, _, code := act(t, s)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if want := "\nPrograms\n  Example Webinar 2026  Member\n  Program 1002          Member\n"; !strings.HasSuffix(out, want) {
		t.Errorf("out:\n%s", out)
	}
	js, _, _ := act(t, s, "--json")
	if progs := js[strings.Index(js, `"programs"`):]; strings.Contains(progs, `"date"`) || strings.Contains(progs, "older programs") {
		t.Errorf("json:\n%s", js)
	}
}

func TestActivityLookupsOnlyShownPrograms(t *testing.T) {
	s := activityServer(t)
	dates := map[int]string{}
	for i := range 35 {
		d := "2026-11-01T00:00:00Z+0000"
		if i >= 30 {
			d = "2026-01-01T00:00:00Z+0000" // older than the window
		}
		dates[4000+i] = d
		s.Handle(fmt.Sprintf(programFmt, 4000+i), namedProgram(4000+i))
	}
	s.Handle(membPath, marketotest.Response{Body: datedMemberships(dates)})
	out, _, code := act(t, s)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	shown, older := 0, 0
	for id := range dates {
		n := s.Hits(fmt.Sprintf(programFmt, id))
		if id >= 4030 {
			older += n
		} else {
			shown += n
		}
	}
	if shown != 25 || older != 0 {
		t.Errorf("lookups: %d shown, %d older; want 25 and 0", shown, older)
	}
	if !strings.Contains(out, "+ 5 older programs (use --all-programs)\n") || !strings.Contains(out, "showing names for 25 of 30 programs\n") {
		t.Errorf("out:\n%s", out)
	}
}

func TestActivityLookupWarningCollapsed(t *testing.T) {
	const perm = "check the API user's asset permissions"
	for _, c := range []struct {
		name string
		resp marketotest.Response
		hint string
	}{
		{"603", marketotest.Response{Body: `{"success":false,"errors":[{"code":"603","message":"Access denied"}]}`}, perm},
		{"HTTP 401", marketotest.Response{Status: http.StatusUnauthorized, Body: `{"success":false}`}, perm},
		{"HTTP 403", marketotest.Response{Status: http.StatusForbidden, Body: `{"success":false}`}, perm},
		{"decode", marketotest.Response{Body: `{"success":true,"result":[{"id":"not a number"}]}`}, "unexpected response format from Marketo; please report it"},
		{"other", marketotest.Response{Body: `{"success":false,"errors":[{"code":"610","message":"Requested resource not found"}]}`}, "Marketo error 610: Requested resource not found"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := activityServer(t)
			s.Handle(membPath, marketotest.Response{Body: membershipBody(1001, 1002, 1003, 1004)})
			for _, id := range []int{1002, 1003, 1004} {
				s.Handle(fmt.Sprintf(programFmt, id), c.resp)
			}
			_, errOut, code := act(t, s)
			if code != 0 {
				t.Fatalf("exit %d, stderr %s", code, errOut)
			}
			if want := "warning: could not look up 3 of 4 program names (" + c.hint + ")\n"; errOut != want {
				t.Errorf("stderr = %q, want %q", errOut, want)
			}
		})
	}
}

func TestActivityDropsMissingExtraField(t *testing.T) {
	s := activityServer(t)
	s.Handle(leadsPath, marketotest.Response{Body: `{"success":false,"errors":[{"code":"1006","message":"Field 'title' not found"}]}`},
		marketotest.Response{Body: "leads_one.json"})
	_, errOut, code := act(t, s, "--verbose")
	if code != 0 {
		t.Fatalf("exit %d, stderr %s", code, errOut)
	}
	if got := s.Hits(leadsPath); got != 2 {
		t.Errorf("leads hits = %d, want 2", got)
	}
	if f := strings.Split(s.Query(leadsPath).Get("fields"), ","); slices.Contains(f, "title") || !slices.Contains(f, "company") {
		t.Errorf("retry fields = %v", f)
	}
	if !strings.Contains(errOut, "note: field \"title\" isn't on this Marketo instance; skipped\n") {
		t.Errorf("stderr: %s", errOut)
	}
}

func TestActivityProgramsMixedDates(t *testing.T) {
	s := activityServer(t)
	s.Handle(membPath, marketotest.Response{Body: `{"success":true,"result":[
{"id":3001,"progressionStatus":"Member"},
{"id":3002,"progressionStatus":"Member","membershipDate":"2026-09-01T00:00:00Z+0000"},
{"id":3003,"progressionStatus":"Member","membershipDate":"2026-10-08T12:00:00Z+0000"},
{"id":3004,"progressionStatus":"Member","membershipDate":"2026-11-05T09:00:00Z+0000"}]}`})
	for _, id := range []int{3001, 3002, 3003, 3004} {
		s.Handle(fmt.Sprintf(programFmt, id), namedProgram(id))
	}
	out, _, code := act(t, s)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	// 3003 is dated exactly at since (inclusive); undated 3001 is kept, last; only 3002 is older.
	if want := "\nPrograms\n  Program 3004  Member\n  Program 3003  Member\n  Program 3001  Member\n+ 1 older programs (use --all-programs)\n"; !strings.HasSuffix(out, want) {
		t.Errorf("out:\n%s", out)
	}
}

// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
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
	if strings.Count(errOut, "warning: could not look up program 1002 (") != 1 || !strings.Contains(errOut, "check the API user's asset permissions") {
		t.Errorf("stderr: %s", errOut)
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

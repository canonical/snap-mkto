// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/canonical/snap-mkto/internal/config"
	"github.com/canonical/snap-mkto/internal/marketo"
	"github.com/canonical/snap-mkto/internal/marketotest"
)

const (
	typesPath  = "/rest/v1/activities/types.json"
	tokenPath  = "/rest/v1/activities/pagingtoken.json"
	actsPath   = "/rest/v1/activities.json"
	membPath   = "/rest/v1/leads/1001/programMembership.json"
	programFmt = "/rest/asset/v1/program/%d.json"
	tokensFmt  = "/rest/asset/v1/folder/%d/tokens.json"
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
	handleProgram(s, 1001, marketotest.Response{Body: "program_by_id_one.json"})
	return s
}

// handleProgram serves program id's name lookup with r and its token lookup with no tokens.
func handleProgram(s *marketotest.Server, id int, r marketotest.Response) {
	s.Handle(fmt.Sprintf(programFmt, id), r)
	s.Handle(fmt.Sprintf(tokensFmt, id), marketotest.Response{Body: `{"success":true,"result":[]}`})
}

func act(t *testing.T, s *marketotest.Server, args ...string) (string, string, int) {
	t.Helper()
	return fakeCLINow(t, s, fixedNow, append([]string{"activity", "jane.doe@example.com"}, args...)...)
}

func TestActivityTimelineGolden(t *testing.T) {
	s := activityServer(t)
	// The fake returns activities_final_page.json (with a Visit Webpage row) whatever ids are asked for.
	// --days 30 keeps the program (joined 2026-10-20) in the window.
	out, _, code := act(t, s, "--days", "30")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	golden(t, "activity_one.txt", out)
	out, _, _ = act(t, s, "--days", "30", "--json")
	golden(t, "activity_one.json", out)
	if m := s.Methods(); slices.ContainsFunc(m, func(x string) bool { return x != http.MethodGet }) {
		t.Errorf("non-GET request: %v", m)
	}
	if _, _, _ = act(t, s); s.Query(tokenPath).Get("sinceDatetime") != "2026-10-24T12:00:00Z" { // the default is 14 days
		t.Errorf("sinceDatetime = %q", s.Query(tokenPath).Get("sinceDatetime"))
	}
	f := strings.Split(s.Query(leadsPath).Get("fields"), ",")
	for _, k := range []string{"id", "updatedAt", "title", "company", "email", "leadScore", "sfdcAccountId"} {
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
	want := "Jane Doe · lead 1001\ntimes in Asia/Tbilisi\n\nNo activity in the last 7 days.\nMarketo purges some high-volume activity types after a limited period.\n\nPrograms\n  (none)\n"
	if out != want {
		t.Errorf("got:\n%s", out)
	}
}

func TestActivityDuplicatesPicksLatest(t *testing.T) {
	s := activityServer(t)
	s.Handle(leadsPath, marketotest.Response{Body: "leads_two_duplicates.json"})
	out, errOut, code := act(t, s)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	want := "2 leads share this email; showing 1001 (most recently updated). Others: 1002. Use --lead-id to pick one."
	if !strings.HasPrefix(errOut, "note: "+want+"\n") || strings.Contains(out, "share this email") {
		t.Errorf("duplicate note not first on stderr:\nstderr:\n%s\nstdout:\n%s", errOut, out)
	}
	js, errOut, _ := act(t, s, "--json")
	if !strings.Contains(js, `"`+want+`"`) || strings.Contains(errOut, "share this email") {
		t.Errorf("--json: note not in notes:\n%s\nstderr:\n%s", js, errOut)
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
	if got := s.Hits(tokenPath); got != 7 { // 25 types in 7 batches of at most 4
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
		handleProgram(s, ids[i], marketotest.Response{Body: "program_by_id_one.json"})
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
	n = 0
	for _, id := range ids {
		n += s.Hits(fmt.Sprintf(tokensFmt, id))
	}
	if n != 25 {
		t.Errorf("token lookups = %d, want 25 (the programs whose names were looked up)", n)
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
	out, _, code := act(t, s, "--out", p, "--force")
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

func TestOutUnwritableNoCalls(t *testing.T) {
	s := activityServer(t)
	p := filepath.Join(t.TempDir(), "missing", "out.txt")
	if _, errOut, code := act(t, s, "--out", p); code != 1 || !strings.Contains(errOut, "no such file") {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
	if n := s.Hits(marketotest.IdentityPath); n != 0 {
		t.Errorf("identity hits = %d", n)
	}
}

func TestOutExistsRefuses(t *testing.T) {
	s := activityServer(t)
	p := filepath.Join(t.TempDir(), "out.txt")
	if err := os.WriteFile(p, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, errOut, code := act(t, s, "--out", p)
	if code != 2 || !strings.Contains(errOut, p+" exists; use --force to overwrite") {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
	if b, _ := os.ReadFile(p); string(b) != "old" {
		t.Errorf("file changed: %q", b)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o644 {
		t.Errorf("mode changed: %v", fi.Mode())
	}
	if n := s.Hits(marketotest.IdentityPath); n != 0 {
		t.Errorf("identity hits = %d", n)
	}
}

func TestOutForceOverwrites(t *testing.T) {
	s := activityServer(t)
	p := filepath.Join(t.TempDir(), "out.txt")
	if err := os.WriteFile(p, []byte(strings.Repeat("old\n", 5000)), 0o644); err != nil { // longer than the output: truncation shows
		t.Fatal(err)
	}
	if _, errOut, code := act(t, s, "--out", p, "--force"); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	want, _, _ := act(t, s)
	if b, _ := os.ReadFile(p); string(b) != want {
		t.Errorf("file differs from stdout output:\n%s", b)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", fi.Mode())
	}
	// A failed --force run leaves the existing file as it was, mode included, and no temp file.
	s.Handle(actsPath, marketotest.Response{Status: http.StatusInternalServerError})
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, code := act(t, s, "--out", p, "--force"); code == 0 {
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
}

func TestOutRemovedOnFailure(t *testing.T) {
	for _, force := range []bool{false, true} {
		s := activityServer(t)
		s.Handle(leadsPath, marketotest.Response{Body: `{"success":true,"result":[]}`}) // not found: exit 3
		p := filepath.Join(t.TempDir(), "out.txt")
		args := []string{"--out", p}
		if force {
			args = append(args, "--force")
		}
		if _, _, code := act(t, s, args...); code != 3 {
			t.Errorf("force=%v: exit %d", force, code)
		}
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("force=%v: file left behind: %v", force, err)
		}
	}
}

func TestOutPrintsWrote(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	_, errOut, code := act(t, activityServer(t), "--out", "b5.txt")
	if code != 0 || !strings.Contains(errOut, "wrote "+filepath.Join(dir, "b5.txt")+"\n") {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
}

func TestActivityOutSnapPermissionHint(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := filepath.Join(t.TempDir(), "ro")
	if err := os.Mkdir(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	args := []string{"activity", "jane.doe@example.com", "--out", filepath.Join(dir, ".b5.txt")}
	_, errOut, code := fakeCLIFull(t, activityServer(t), fixedNow, "", "", map[string]string{"SNAP_NAME": "mkto"}, args...)
	if code != 1 || !strings.Contains(errOut, "permission denied; the mkto snap can write only to non-hidden paths in your home folder (home plug), e.g. ~/b5.txt") {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
	// Outside the snap the plain error stays.
	_, errOut, _ = fakeCLIFull(t, activityServer(t), fixedNow, "", "", nil, args...)
	if strings.Contains(errOut, "home plug") {
		t.Errorf("hint shown outside the snap: %q", errOut)
	}
}

func TestSnapTmpNote(t *testing.T) {
	for _, c := range []struct{ snap, path, want string }{
		{"mkto", "/tmp/b5.txt", "note: /tmp is private to the mkto snap; the file is at /tmp/snap-private-tmp/snap.mkto/tmp/b5.txt (readable with sudo)"},
		{"mkto", "/tmp/sub/b5.txt", "note: /tmp is private to the mkto snap; the file is at /tmp/snap-private-tmp/snap.mkto/tmp/sub/b5.txt (readable with sudo)"},
		{"mkto", "/home/jane/b5.txt", ""},
		{"mkto", "/tmpfoo/b5.txt", ""},
		{"", "/tmp/b5.txt", ""},
	} {
		if got := snapTmpNote(c.snap, c.path); got != c.want {
			t.Errorf("snapTmpNote(%q, %q) = %q, want %q", c.snap, c.path, got, c.want)
		}
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
	handleProgram(s, 1002, marketotest.Response{Body: `{"success":true,"result":[]}`})
	out, errOut, code := act(t, s, "--web") // --web: keeps stderr to the lookup warning
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
	handleProgram(s, 1001, marketotest.Response{Body: "error_607.json"})
	if _, _, code := act(t, s, "--days", "30"); code != 4 { // the program joined 2026-10-20: in the window
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

// TestActivityJSONProfileFields: profile fields that came back non-nil join the lead fields.
func TestActivityJSONProfileFields(t *testing.T) {
	s := activityServer(t)
	s.Handle(leadsPath, marketotest.Response{Body: "leads_one_scored.json"})
	js, _, _ := act(t, s, "--json")
	if !strings.Contains(js, `"leadScore": 85`) || !strings.Contains(js, `"sfdcAccountId": ""`) || strings.Contains(js, "sfdcContactId") {
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
		handleProgram(s, id, namedProgram(id))
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
	handleProgram(s, 3004, namedProgram(3004))
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
	handleProgram(s, 1002, namedProgram(1002))
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
		handleProgram(s, 4000+i, namedProgram(4000+i))
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
				handleProgram(s, id, c.resp)
			}
			_, errOut, code := act(t, s, "--web") // --web: keeps stderr to the lookup warning
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
		handleProgram(s, id, namedProgram(id))
	}
	out, _, code := act(t, s, "--days", "30")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	// 3003 is dated exactly at since (inclusive); undated 3001 is kept, last; only 3002 is older.
	if want := "\nPrograms\n  Program 3004  Member\n  Program 3003  Member\n  Program 3001  Member\n+ 1 older programs (use --all-programs)\n"; !strings.HasSuffix(out, want) {
		t.Errorf("out:\n%s", out)
	}
}

// actTypeIDs returns the activityTypeIds of every activities call on s (batches run
// concurrently, so no single call holds them all), de-duplicated in arrival order.
func actTypeIDs(s *marketotest.Server) []string {
	return actParam(s, "activityTypeIds", true)
}

// actLeadIDs returns the distinct leadIds values (one per lead batch) the activities calls on s sent.
func actLeadIDs(s *marketotest.Server) []string {
	return actParam(s, "leadIds", false)
}

func actParam(s *marketotest.Server, key string, split bool) []string {
	var vals []string
	for _, q := range s.Queries(actsPath) {
		vs := []string{q.Get(key)}
		if split {
			vs = strings.Split(vs[0], ",")
		}
		for _, v := range vs {
			if !slices.Contains(vals, v) {
				vals = append(vals, v)
			}
		}
	}
	return vals
}

// webTypes is a hand-edited activity_types with web types; the defaults have none.
const webTypes = "activity_types = [\"Visit Webpage\", \"Fill Out Form\", \"Click Link\", \"Open Email\"]\n"

func actConf(t *testing.T, s *marketotest.Server, extra string, args ...string) (string, string, int) {
	t.Helper()
	return fakeCLIConf(t, s, fixedNow, extra, append([]string{"activity", "jane.doe@example.com"}, args...)...)
}

func TestActivityDefaultSkipsWebTypes(t *testing.T) {
	s := activityServer(t)
	_, errOut, code := act(t, s)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if ids := actTypeIDs(s); slices.Contains(ids, "1") || slices.Contains(ids, "3") || strings.Contains(errOut, webNote) {
		t.Errorf("default types: activityTypeIds = %v, stderr:\n%s", ids, errOut)
	}
	_, errOut, code = actConf(t, s, webTypes)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if ids := actTypeIDs(s); slices.Contains(ids, "1") || slices.Contains(ids, "3") {
		t.Errorf("activityTypeIds = %v, want no Visit Webpage (1) or Click Link (3)", ids)
	}
	if strings.Count(errOut, webNote) != 1 {
		t.Errorf("stderr lacks the web note once:\n%s", errOut)
	}
	if out, _, _ := actConf(t, s, webTypes, "--json"); strings.Contains(out, "note:") {
		t.Errorf("web note leaked into --json stdout:\n%s", out)
	}
}

func TestActivityWebFlagIncludes(t *testing.T) {
	s := activityServer(t)
	_, errOut, code := actConf(t, s, webTypes, "--web")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if ids := actTypeIDs(s); !slices.Contains(ids, "1") || !slices.Contains(ids, "3") {
		t.Errorf("activityTypeIds = %v, want Visit Webpage (1) and Click Link (3)", ids)
	}
	if strings.Contains(errOut, webNote) {
		t.Errorf("web note printed with --web:\n%s", errOut)
	}
}

func TestActivityExplicitTypesKeepWeb(t *testing.T) {
	s := activityServer(t)
	_, errOut, code := act(t, s, "--types", "Visit Webpage,Click Link")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if ids := actTypeIDs(s); !slices.Equal(ids, []string{"1", "3"}) {
		t.Errorf("--types: activityTypeIds = %v", ids)
	}
	s = activityServer(t)
	_, errOut2, code := act(t, s, "--all-types")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if ids := actTypeIDs(s); len(ids) != 25 || !slices.Contains(ids, "1") || !slices.Contains(ids, "3") {
		t.Errorf("--all-types: activityTypeIds = %v, want all 25 with the web ids", ids)
	}
	for _, e := range []string{errOut, errOut2} {
		if strings.Contains(e, webNote) {
			t.Errorf("web note printed for an explicit choice:\n%s", e)
		}
	}
	// web-only activity_types: an error, not an empty fetch
	_, errOut, code = fakeCLIConf(t, activityServer(t), fixedNow, "activity_types = [\"Visit Webpage\", \"Click Link\"]\n", "activity", "jane.doe@example.com")
	if code != 1 || errOut != "no activity types left to fetch: activity_types only has web types; add --web to include them\n" {
		t.Errorf("web-only: exit %d, stderr %q", code, errOut)
	}
}

// TestDefaultTypesThreeBatches: the 10 default types go in 3 batches of at most 4.
func TestDefaultTypesThreeBatches(t *testing.T) {
	s := activityServer(t)
	s.Handle(typesPath, marketotest.Response{Body: "activity_types_story.json"}) // has all 10 defaults
	_, errOut, code := act(t, s)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if got := s.Hits(tokenPath); got != 3 {
		t.Errorf("token hits = %d, want 3", got)
	}
	for _, q := range s.Queries(actsPath) {
		if n := len(strings.Split(q.Get("activityTypeIds"), ",")); n > 4 {
			t.Errorf("a batch has %d types, want at most 4", n)
		}
	}
	if ids := actTypeIDs(s); len(ids) != 10 {
		t.Errorf("activityTypeIds = %v, want 10", ids)
	}
	if errOut != "" {
		t.Errorf("stderr:\n%s", errOut)
	}
}

// TestDefaultMissingTypeQuiet: a default type this instance lacks (activity_types.json has
// no Email Bounced Soft) is a --verbose note, never a warning; a user's own list still warns.
const legacyTypes = "activity_types = [\"Visit Webpage\", \"Fill Out Form\", \"Click Link\", \"Email Delivered\", \"Open Email\", \"Click Email\", \"Unsubscribe Email\", \"Change Status in Progression\", \"New Lead\"]\n"

func TestDefaultMissingTypeQuiet(t *testing.T) {
	const missing = "note: default activity types not on this instance: Email Bounced Soft\n"
	const migrated = "note: activity_types is the unedited v0.1.0 list; using the 0.2.0 default activity types instead (delete activity_types from "
	for _, c := range []struct {
		name, extra string
		verbose     bool
	}{
		{"default", "", false},
		{"default verbose", "", true},
		{"legacy", legacyTypes, false},
		{"legacy verbose", legacyTypes, true},
	} {
		for _, cmd := range []string{"activity", "brief"} {
			args := []string{cmd, "jane.doe@example.com"}
			s := activityServer(t)
			if cmd == "brief" {
				s = briefServer(t, "Contact Us Form")
				args = append(args, "--show-payload")
			}
			if c.verbose {
				args = append(args, "--verbose")
			}
			_, errOut, code := fakeCLIConf(t, s, fixedNow, c.extra, args...)
			if code != 0 {
				t.Fatalf("%s %s: exit %d: %s", c.name, cmd, code, errOut)
			}
			wantMigrated := c.verbose && c.extra != ""
			if strings.Contains(errOut, "warning:") || strings.Count(errOut, missing) != map[bool]int{true: 1}[c.verbose] ||
				strings.Count(errOut, migrated) != map[bool]int{true: 1}[wantMigrated] {
				t.Errorf("%s %s: stderr:\n%s", c.name, cmd, errOut)
			}
		}
	}
	_, errOut, code := actConf(t, activityServer(t), "activity_types = [\"Open Email\", \"Nope\"]\n")
	if code != 0 || !strings.Contains(errOut, `warning: unknown activity type "Nope" skipped; check activity_types in `) {
		t.Errorf("user list: exit %d, stderr %s", code, errOut)
	}
}

func storyServer(t *testing.T) *marketotest.Server {
	t.Helper()
	s := activityServer(t)
	s.Handle(typesPath, marketotest.Response{Body: "activity_types_story.json"})
	s.Handle(actsPath, marketotest.Response{Body: "activities_story_page.json"})
	s.Handle(fmt.Sprintf(campaignFmt, 3001), marketotest.Response{Body: "smart_campaign_one.json"}) // "Score Threshold"
	s.Handle(fmt.Sprintf(campaignFmt, 3002), campaignBody(3002, "Behavior Scoring"))
	s.Handle(fmt.Sprintf(campaignFmt, 3003), campaignBody(3003, "Webinar Attendance"))
	s.Handle(fmt.Sprintf(campaignFmt, 3004), campaignBody(3004, "Disqualify Bounced"))
	return s
}

const campaignFmt = "/rest/asset/v1/smartCampaign/%d.json"

// campaignBody is a smart-campaign-by-id reply naming campaign id.
func campaignBody(id int, name string) marketotest.Response {
	return marketotest.Response{Body: fmt.Sprintf(`{"success":true,"result":[{"id":%d,"name":%q}]}`, id, name)}
}

// storyPII are the PII attribute values in activities_story_page.json.
var storyPII = []string{"192.0.2.10", "ExampleBrowser", "Form Fields", `"Email":`, "utm_source"}

func TestActivityStoryGolden(t *testing.T) {
	s := storyServer(t)
	out, _, code := act(t, s, "--days", "60")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	golden(t, "activity_story.txt", out)
	for _, l := range strings.Split(out, "\n") {
		if strings.TrimRight(l, " ") != l {
			t.Errorf("trailing spaces in %q", l)
		}
	}
	for _, p := range storyPII {
		if strings.Contains(out, p) {
			t.Errorf("text output contains PII value %q", p)
		}
	}
	js, _, _ := act(t, s, "--days", "60", "--json")
	golden(t, "activity_story.json", js)
	var res struct{ Events []struct{ Detail string } }
	if err := json.Unmarshal([]byte(js), &res); err != nil {
		t.Fatal(err)
	}
	for _, e := range res.Events {
		for _, p := range storyPII {
			if strings.Contains(e.Detail, p) {
				t.Errorf("detail %q contains PII value %q", e.Detail, p)
			}
		}
	}
}

func TestChangeFieldsFilter(t *testing.T) {
	s := storyServer(t)
	out, _, code := actConf(t, s, "change_fields = [\"lead score\"]\n", "--days", "60")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if strings.Contains(out, "Nurture→MQL") || !strings.Contains(out, "Change Data Value rows hidden by change_fields: 2 (use --all-changes)") {
		t.Errorf("Lead Status changes not hidden:\n%s", out)
	}
	if !strings.Contains(out, "+25 (60→85)") {
		t.Errorf("other rows lost:\n%s", out)
	}
	js, _, _ := actConf(t, s, "change_fields = [\"lead score\"]\n", "--days", "60", "--json")
	if !strings.Contains(js, `"detail": "Nurture→MQL"`) || !strings.Contains(js, `"detail": "MQL→Disqualified"`) {
		t.Errorf("--json lost field changes:\n%s", js)
	}
	out, _, _ = actConf(t, s, "change_fields = [\" Lead Status \"]\n", "--days", "60")
	if !strings.Contains(out, "Nurture→MQL") || !strings.Contains(out, "MQL→Disqualified") || strings.Contains(out, "hidden") {
		t.Errorf("listed field hidden:\n%s", out)
	}
}

func TestAllChangesFlag(t *testing.T) {
	s := storyServer(t)
	out, _, code := actConf(t, s, "change_fields = [\"Lead Score\"]\n", "--days", "60", "--all-changes")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "Nurture→MQL") || !strings.Contains(out, "MQL→Disqualified") || strings.Contains(out, "hidden") {
		t.Errorf("--all-changes hid rows:\n%s", out)
	}
}

func TestChangeFieldsAllHiddenSaysSo(t *testing.T) {
	var buf bytes.Buffer
	if err := writeActivity(&buf, nil, 30, time.UTC, nil, 2, nil, nil, nil, "", nil); err != nil {
		t.Fatal(err)
	}
	if out := buf.String(); strings.Contains(out, "No activity") || !strings.Contains(out, "Nothing to show: every activity in the last 30 days is a field change hidden by change_fields.") {
		t.Errorf("hidden rows reported as no activity:\n%s", out)
	}
}

func TestActivityCallCardGolden(t *testing.T) {
	s := storyServer(t)
	s.Handle(leadsPath, marketotest.Response{Body: "leads_one_scored.json"})
	tail := "lead_url = \"https://marketo.example.com/leads/{id}/detail\"\n"
	out, _, code := fakeCLIFull(t, s, fixedNow, "", tail, nil, "activity", "jane.doe@example.com", "--days", "60")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	golden(t, "activity_callcard.txt", out)
	out, errOut, code := fakeCLIFull(t, s, fixedNow, "", "lead_url = \"https://marketo.example.com/leads\"\n", nil,
		"activity", "jane.doe@example.com", "--days", "60", "--verbose")
	if code != 0 || strings.Contains(out, "open:") || !strings.Contains(errOut, "note: output.lead_url has no {id}; not showing a record link") {
		t.Errorf("lead_url without {id}: exit %d, stdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
}

func TestCallCardCountsCapped(t *testing.T) {
	cfg := &config.Config{Output: config.Output{Timezone: "UTC"}}
	now := fixedNow()
	names := map[int]string{}
	var acts []marketo.Activity
	for i := 1; i <= 7; i++ {
		names[i] = fmt.Sprintf("Type %d", i)
		acts = append(acts, marketo.Activity{TypeID: i, Date: now.Add(-time.Hour)})
	}
	got := callCard(cfg, time.UTC, marketo.Lead{"id": float64(1)}, acts, names, 30, now)
	if !strings.Contains(strings.Join(got, "\n"), ", +2 more · times in UTC") {
		t.Errorf("counts not capped at %d: %q", maxCardCounts, got)
	}
}

func TestCallCardNoProfileFields(t *testing.T) {
	cfg := &config.Config{Output: config.Output{Timezone: "Asia/Tbilisi"}}
	loc, _ := cfg.Location()
	now := fixedNow()
	names := map[int]string{7: "Email Delivered", 10: "Open Email", 11: "Click Email", 2: "Fill Out Form"}
	opens := []marketo.Activity{{TypeID: 10, Date: now.Add(-50 * time.Hour)}, {TypeID: 2, Date: now.Add(-60 * time.Hour)},
		{TypeID: 10, Date: now.Add(-70 * time.Hour)}, {TypeID: 11, Date: now.Add(-71 * time.Hour)}, {TypeID: 10, Date: now.Add(-72 * time.Hour)},
		{TypeID: 7, Date: now.Add(-time.Hour)}}
	for _, c := range []struct {
		name string
		lead marketo.Lead
		acts []marketo.Activity
		want []string
	}{
		{"bare", marketo.Lead{"id": float64(1001)}, nil, []string{"lead 1001", "times in Asia/Tbilisi"}},
		{"only deliveries", marketo.Lead{"id": float64(1001), "leadScore": nil}, opens[5:], []string{"lead 1001", "times in Asia/Tbilisi"}},
		{"job only", marketo.Lead{"id": float64(1001), "title": "Platform Engineer", "company": "Example Corp"}, nil,
			[]string{"Platform Engineer @ Example Corp · lead 1001", "times in Asia/Tbilisi"}},
		{"name and company", marketo.Lead{"id": float64(1001), "firstName": "Jane", "company": "Example Corp", "leadStatus": ""}, opens,
			[]string{"Jane · Example Corp · lead 1001", "last touch 2d ago · 30d: 3 opens, 1 form, 1 click · times in Asia/Tbilisi"}},
		{"owner only", marketo.Lead{"id": float64(1001), "lastName": "Doe", "title": "Platform Engineer", "leadOwnerEmailAddress": "a.rep@example.com"}, nil,
			[]string{"Doe · Platform Engineer · lead 1001", "owner a.rep@example.com", "times in Asia/Tbilisi"}},
		{"salesforce owner only", marketo.Lead{"id": float64(1001), "leadScore": float64(40), "leadOwnerEmailAddress": "", "sfdcLeadOwnerId": "005000000000001AAA"}, nil,
			[]string{"lead 1001", "score 40 · owner 005000000000001AAA", "times in Asia/Tbilisi"}},
		{"owner email wins", marketo.Lead{"id": float64(1001), "leadOwnerEmailAddress": "a.rep@example.com", "sfdcLeadOwnerId": "005000000000001AAA"}, nil,
			[]string{"lead 1001", "owner a.rep@example.com", "times in Asia/Tbilisi"}},
	} {
		if got := callCard(cfg, loc, c.lead, c.acts, names, 30, now); !slices.Equal(got, c.want) {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
	cfg.Output.Timezone = "Local"
	got := callCard(cfg, time.Local, marketo.Lead{"id": float64(1001)}, nil, nil, 30, now)
	if zone, _ := now.In(time.Local).Zone(); !slices.Equal(got, []string{"lead 1001", "times in " + zone}) {
		t.Errorf("Local: got %q, want the zone abbreviation %q", got, zone)
	}
}

func TestActivityWebFlagAddsWebTypes(t *testing.T) {
	// A list without web types, like the defaults; short, so it stays one batch.
	s := activityServer(t)
	if _, _, code := actConf(t, s, "activity_types = [\"Open Email\"]\n", "--web"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if ids := actTypeIDs(s); !slices.Contains(ids, "1") || !slices.Contains(ids, "3") {
		t.Errorf("list without web types + --web: activityTypeIds = %v, want Visit Webpage (1) and Click Link (3) added", ids)
	}
}

// TestProgramsSectionCreditGolden: the acquisition program and program success are marked.
func TestProgramsSectionCreditGolden(t *testing.T) {
	s := activityServer(t)
	s.Handle(membPath, marketotest.Response{Body: `{"success":true,"result":[
{"id":1001,"progressionStatus":"Member","acquiredBy":true,"reachedSuccess":false},
{"id":1002,"progressionStatus":"Attended","acquiredBy":false,"reachedSuccess":true},
{"id":1003,"progressionStatus":"Attended","acquiredBy":true,"reachedSuccess":true},
{"id":1004,"progressionStatus":"Invited"}]}`})
	for _, id := range []int{1002, 1003, 1004} {
		handleProgram(s, id, namedProgram(id))
	}
	out, _, code := act(t, s)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	golden(t, "activity_credit.txt", out)
	out, _, _ = act(t, s, "--json")
	golden(t, "activity_credit.json", out)
}

// TestProgramsSectionUsesTokenName: my.Name becomes the title (program name in brackets),
// my.WPlink goes on the next line, matched with or without "my." and in any case.
func TestProgramsSectionUsesTokenName(t *testing.T) {
	s := activityServer(t)
	s.Handle(membPath, marketotest.Response{Body: membershipBody(1001, 1002)})
	s.Handle(fmt.Sprintf(tokensFmt, 1001), marketotest.Response{Body: "program_tokens_one.json"})
	handleProgram(s, 1002, namedProgram(1002))
	out, errOut, code := act(t, s)
	if code != 0 {
		t.Fatalf("exit %d, stderr %s", code, errOut)
	}
	want := "\nPrograms\n  Example Whitepaper 2026 (Example Webinar 2026)  Member\n    https://www.example.com/whitepaper\n  Program 1002                                    Member\n"
	if !strings.HasSuffix(out, want) {
		t.Errorf("out:\n%s\nwant suffix:\n%s", out, want)
	}
	if got := s.Query(fmt.Sprintf(tokensFmt, 1001)).Get("folderType"); got != "Program" {
		t.Errorf("folderType = %q", got)
	}
	js, _, _ := act(t, s, "--json")
	if !strings.Contains(js, `"title": "Example Whitepaper 2026",
      "link": "https://www.example.com/whitepaper"`) || strings.Count(js, `"title"`) != 1 {
		t.Errorf("json:\n%s", js)
	}
	out, _, _ = fakeCLIConf(t, s, fixedNow, "name_tokens = [\"NAME\"]\nlink_tokens = []\n", "activity", "jane.doe@example.com")
	if !strings.Contains(out, "  Example Whitepaper 2026 (Example Webinar 2026)  Member\n  Program 1002") {
		t.Errorf("name_tokens NAME, link_tokens []:\n%s", out)
	}
	hits := s.Hits(fmt.Sprintf(tokensFmt, 1001))
	fakeCLIConf(t, s, fixedNow, "name_tokens = []\nlink_tokens = []\n", "activity", "jane.doe@example.com")
	if got := s.Hits(fmt.Sprintf(tokensFmt, 1001)); got != hits {
		t.Errorf("no token names configured, yet tokens were read")
	}
}

// TestTokenLookupFailsSoft: a 603 on tokens falls back to the program name, with one warning.
func TestTokenLookupFailsSoft(t *testing.T) {
	s := activityServer(t)
	s.Handle(membPath, marketotest.Response{Body: membershipBody(1001, 1002)})
	handleProgram(s, 1002, namedProgram(1002))
	for _, id := range []int{1001, 1002} {
		s.Handle(fmt.Sprintf(tokensFmt, id), marketotest.Response{Body: "error_603.json"})
	}
	out, errOut, code := act(t, s, "--web") // --web: keeps stderr to the lookup warning
	if code != 0 {
		t.Fatalf("exit %d, stderr %s", code, errOut)
	}
	if want := "\nPrograms\n  Example Webinar 2026  Member\n  Program 1002          Member\n"; !strings.HasSuffix(out, want) {
		t.Errorf("out:\n%s", out)
	}
	if want := "warning: could not read program tokens for 2 of 2 programs (check the API user's asset permissions)\n"; errOut != want {
		t.Errorf("stderr = %q, want %q", errOut, want)
	}
}

// TestChangeFieldsDefaultAndEmpty: absent change_fields hides fields outside the default
// list; change_fields = [] and --all-changes show every field.
func TestChangeFieldsDefaultAndEmpty(t *testing.T) {
	const page = `{"requestId":"r","success":true,"moreResult":false,"result":[
{"id":1,"leadId":1001,"activityDate":"2026-10-05T09:00:00Z","activityTypeId":13,"primaryAttributeValue":"Lead Status","attributes":[{"name":"Old Value","value":"Nurture"},{"name":"New Value","value":"MQL"}]},
{"id":2,"leadId":1001,"activityDate":"2026-10-06T09:00:00Z","activityTypeId":13,"primaryAttributeValue":"Job Title","attributes":[{"name":"Old Value","value":"Engineer"},{"name":"New Value","value":"Architect"}]}]}`
	for _, c := range []struct {
		name, conf string
		args       []string
		hidden     bool
	}{
		{"absent", "", nil, true},
		{"empty", "change_fields = []\n", nil, false},
		{"all-changes", "", []string{"--all-changes"}, false},
	} {
		s := storyServer(t)
		s.Handle(actsPath, marketotest.Response{Body: page})
		out, errOut, code := actConf(t, s, c.conf, append([]string{"--days", "60"}, c.args...)...)
		if code != 0 {
			t.Fatalf("%s: exit %d: %s", c.name, code, errOut)
		}
		if !strings.Contains(out, "Nurture→MQL") || strings.Contains(out, "Engineer→Architect") == c.hidden ||
			strings.Contains(out, "hidden by change_fields: 1") != c.hidden {
			t.Errorf("%s: hidden = %t, got:\n%s", c.name, c.hidden, out)
		}
	}
}

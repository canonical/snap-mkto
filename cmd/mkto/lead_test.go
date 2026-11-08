// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/canonical/snap-mkto/internal/marketotest"
)

// fakeCLI runs the CLI against a fake Marketo server, with a temp 0600 config.
func fakeCLI(t *testing.T, srv *marketotest.Server, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	return fakeCLINow(t, srv, time.Now, args...)
}

// fakeCLINow is fakeCLI with an injected clock.
func fakeCLINow(t *testing.T, srv *marketotest.Server, now func() time.Time, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	return fakeCLIConf(t, srv, now, "", args...)
}

// fakeCLIConf is fakeCLINow with extra [marketo] config lines.
func fakeCLIConf(t *testing.T, srv *marketotest.Server, now func() time.Time, extra string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	return fakeCLIFull(t, srv, now, extra, "", nil, args...)
}

// fakeCLIFull is fakeCLIConf with extra trailing config sections and environment.
func fakeCLIFull(t *testing.T, srv *marketotest.Server, now func() time.Time, extra, tail string, env map[string]string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	leadFields := "lead_fields = [\"id\", \"email\", \"firstName\", \"lastName\", \"createdAt\", \"updatedAt\"]\n"
	if strings.Contains(extra, "lead_fields =") { // extra sets its own
		leadFields = ""
	}
	cfg := "[marketo]\nbase_url = \"https://x.mktorest.com\"\nclient_id = \"id\"\nclient_secret = \"s3cret\"\n" + extra +
		leadFields + "[output]\ntimezone = \"Asia/Tbilisi\"\n" + tail
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code = run(append([]string{"--config", p}, args...), Env{
		Stdout: &out, Stderr: &errb,
		Getenv: func(k string) string { return env[k] },
		HTTP:   &http.Client{Transport: toFake{srv.URL}},
		Now:    now,
		Sleep:  func(context.Context, time.Duration) error { return nil },
	})
	return out.String(), errb.String(), code
}

// toFake sends every https request to the plain-HTTP fake server, since config demands https.
// Plain-HTTP requests (the llmtest server via Env.LLMBaseURL) pass through unchanged.
type toFake struct{ base string }

func (f toFake) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme != "https" {
		return http.DefaultTransport.RoundTrip(r)
	}
	u, _ := url.Parse(f.base)
	r = r.Clone(r.Context())
	r.URL.Scheme, r.URL.Host = u.Scheme, u.Host
	return http.DefaultTransport.RoundTrip(r)
}

const leadsPath = "/rest/v1/leads.json"

func TestLeadEmailTrimmedAndEncoded(t *testing.T) {
	srv := marketotest.New(t)
	srv.Handle(leadsPath, marketotest.Response{Body: "leads_one.json"})
	if _, _, code := fakeCLI(t, srv, "lead", " Jane+test@example.com "); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if got := srv.Query(leadsPath).Get("filterValues"); got != "Jane+test@example.com" {
		t.Errorf("filterValues = %q", got)
	}
	if got := srv.Query(leadsPath).Get("fields"); got != "id,email,firstName,lastName,createdAt,updatedAt,"+
		"leadScore,leadStatus,sfdcLeadOwnerId,unsubscribed,emailInvalid,sfdcLeadId,sfdcContactId,sfdcAccountId" {
		t.Errorf("fields = %q", got)
	}
	if m := srv.Methods(); slices.ContainsFunc(m, func(s string) bool { return s != http.MethodGet }) {
		t.Errorf("non-GET request: %v", m)
	}
}

func TestLeadOne(t *testing.T) {
	srv := marketotest.New(t)
	srv.Handle(leadsPath, marketotest.Response{Body: "leads_one.json"})
	out, _, code := fakeCLI(t, srv, "lead", "jane.doe@example.com")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	golden(t, "lead_one.txt", out)
	out, _, _ = fakeCLI(t, srv, "lead", "jane.doe@example.com", "--json")
	golden(t, "lead_one.json", out)
}

// One column per lead: id and updatedAt always, then only the fields that differ.
func TestLeadDuplicatesSideBySideGolden(t *testing.T) {
	srv := marketotest.New(t)
	srv.Handle(leadsPath, marketotest.Response{Body: "leads_two_duplicates.json"})
	out, _, code := fakeCLI(t, srv, "lead", "jane.doe@example.com")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	golden(t, "lead_duplicates.txt", out)

	// A field that differs gets a row; the empty profile fields are not counted as "same".
	srv.Handle(leadsPath, marketotest.Response{Body: `{"success":true,"result":[
{"id":1001,"updatedAt":"2026-10-01T12:00:00Z","email":"jane.doe@example.com","firstName":"Jane","lastName":"Doe","leadScore":85},
{"id":1002,"updatedAt":"2026-06-15T08:30:00Z","email":"jane.doe@example.com","firstName":"Janet","lastName":"Doe","leadScore":85}]}`})
	out, _, _ = fakeCLI(t, srv, "lead", "jane.doe@example.com")
	if got := rowNames(out); !slices.Equal(got, []string{"id", "updatedAt", "firstName", "4"}) || // "4" is the summary line
		!strings.HasPrefix(out, "FIELD ") || !strings.HasSuffix(out, "\n4 fields are the same in every lead (not shown)\n") {
		t.Errorf("rows = %v\n%s", got, out)
	}
}

func TestLeadByID(t *testing.T) {
	srv := marketotest.New(t)
	srv.Handle(leadsPath, marketotest.Response{Body: "leads_one.json"})
	if _, errOut, code := fakeCLI(t, srv, "lead", "1001", "--by", "id"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if q := srv.Query(leadsPath); q.Get("filterType") != "id" || q.Get("filterValues") != "1001" {
		t.Errorf("query = %v", q)
	}
	srv.Handle(leadsPath, marketotest.Response{Body: "leads_zero.json"})
	if _, errOut, code := fakeCLI(t, srv, "lead", "9", "--by", "id"); code != 3 || !strings.Contains(errOut, "no lead found for id 9\n") {
		t.Errorf("not found: exit %d, stderr %q", code, errOut)
	}
	for _, bad := range []string{"", "1,2"} {
		s := marketotest.New(t)
		if _, _, code := fakeCLI(t, s, "lead", bad, "--by", "id"); code != 2 || s.Hits(marketotest.IdentityPath) != 0 {
			t.Errorf("%q: exit %d, identity hits %d", bad, code, s.Hits(marketotest.IdentityPath))
		}
	}
}

func TestLeadBySfdcContactID(t *testing.T) {
	const id = "003EXAMPLE000001AAA"
	srv := activityServer(t)
	srv.Handle(leadsPath, marketotest.Response{Body: "leads_one.json"})
	for _, cmd := range [][]string{{"lead"}, {"activity"}, {"brief", "--show-payload"}} {
		args := append([]string{cmd[0], id, "--by", "sfdcContactId"}, cmd[1:]...)
		if _, errOut, code := fakeCLINow(t, srv, fixedNow, args...); code != 0 {
			t.Errorf("%s: exit %d: %s", cmd[0], code, errOut)
		}
		if q := srv.Query(leadsPath); q.Get("filterType") != "sfdcContactId" || q.Get("filterValues") != id {
			t.Errorf("%s: query = %v", cmd[0], q)
		}
	}
	srv.Handle(leadsPath, marketotest.Response{Body: "leads_two_duplicates.json"})
	if _, errOut, _ := fakeCLI(t, srv, "lead", id, "--by", "sfdcContactId"); !strings.HasPrefix(errOut, "note: 2 leads share this sfdcContactId ") {
		t.Errorf("stderr: %s", errOut)
	}
}

func TestLeadLeadIDFlag(t *testing.T) {
	srv := marketotest.New(t)
	srv.Handle(leadsPath, marketotest.Response{Body: "leads_two_duplicates.json"})
	out, errOut, code := fakeCLI(t, srv, "lead", "jane.doe@example.com", "--lead-id", "1002")
	if code != 0 || !strings.HasPrefix(out, "Lead 1002\n") || strings.Contains(out, "1001") || errOut != "" {
		t.Errorf("exit %d, stdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	_, errOut, code = fakeCLI(t, srv, "lead", "jane.doe@example.com", "--lead-id", "9")
	if code != 3 || !strings.Contains(errOut, "lead 9 not found for jane.doe@example.com") {
		t.Errorf("exit %d, stderr %s", code, errOut)
	}
}

func TestBy1003Hint(t *testing.T) {
	srv := activityServer(t)
	srv.Handle(leadsPath, marketotest.Response{Body: "error_1003.json"})
	for _, cmd := range [][]string{{"lead"}, {"activity"}, {"brief", "--show-payload"}} {
		args := append([]string{cmd[0], "001EXAMPLE000001AAA", "--by", "sfdcAccountId"}, cmd[1:]...)
		_, errOut, code := fakeCLINow(t, srv, fixedNow, args...)
		want := "Too many results match the filter; check that sfdcAccountId is a searchable field (names: mkto fields)\n"
		if code != 1 || !strings.HasSuffix(errOut, want) {
			t.Errorf("%s: exit %d, stderr %q", cmd[0], code, errOut)
		}
	}
}

func TestLeadDuplicateNoteOnStderr(t *testing.T) {
	srv := marketotest.New(t)
	srv.Handle(leadsPath, marketotest.Response{Body: "leads_two_duplicates.json"})
	out, errOut, code := fakeCLI(t, srv, "lead", "jane.doe@example.com")
	want := "note: 2 leads share this email (ids: 1001, 1002); use --lead-id to pick one\n"
	if code != 0 || !strings.HasPrefix(errOut, want) || strings.Contains(out, "share this email") {
		t.Errorf("exit %d, stderr:\n%s\nstdout:\n%s", code, errOut, out)
	}
	js, errOut, _ := fakeCLI(t, srv, "lead", "jane.doe@example.com", "--json")
	if strings.Contains(js, "share this email") || strings.Contains(errOut, "share this email") {
		t.Errorf("--json has a duplicate note:\n%s\nstderr:\n%s", js, errOut)
	}
}

func TestLeadNotFound(t *testing.T) {
	srv := marketotest.New(t)
	srv.Handle(leadsPath, marketotest.Response{Body: "leads_zero.json"})
	out, errOut, code := fakeCLI(t, srv, "lead", "nobody@example.com", "--verbose")
	if code != 3 || out != "" {
		t.Errorf("exit %d, stdout %q", code, out)
	}
	if !strings.Contains(errOut, "no lead found for nobody@example.com\n") {
		t.Errorf("stderr: %s", errOut)
	}
	if strings.Count(errOut, "nobody@example.com") != 1 {
		t.Errorf("--verbose must not log the email:\n%s", errOut)
	}
}

func TestLead1006ResponseLevel(t *testing.T) { lead1006(t, "leads_1006_response.json") }
func TestLead1006RecordLevel(t *testing.T)   { lead1006(t, "leads_1006_record.json") }

func lead1006(t *testing.T, fixture string) {
	srv := marketotest.New(t)
	srv.Handle(leadsPath, marketotest.Response{Body: fixture})
	_, errOut, code := fakeCLI(t, srv, "lead", "jane.doe@example.com")
	if code != 1 {
		t.Errorf("exit %d", code)
	}
	// bogusField isn't in lead_fields: the hint names the fields mkto can't do without.
	if !strings.Contains(errOut, `field "bogusField" not found. It isn't in lead_fields, but mkto needs id, email, firstName and lastName (the lookup key and the redaction guard); this Marketo instance may not have that field`) {
		t.Errorf("stderr: %s", errOut)
	}
}

func TestLead1006InLeadFields(t *testing.T) {
	srv := marketotest.New(t)
	srv.Handle(leadsPath, marketotest.Response{Body: `{"requestId":"r","success":false,"errors":[{"code":"1006","message":"Field 'createdAt' not found"}]}`})
	_, errOut, code := fakeCLI(t, srv, "lead", "jane.doe@example.com")
	if code != 1 || !strings.Contains(errOut, `field "createdAt" not found: fix lead_fields in `) || !strings.Contains(errOut, "config.toml") {
		t.Errorf("exit %d, stderr: %s", code, errOut)
	}
}

func TestLeadQuotaExit4(t *testing.T) {
	srv := marketotest.New(t)
	srv.Handle(leadsPath, marketotest.Response{Body: "error_607.json"})
	_, errOut, code := fakeCLI(t, srv, "lead", "jane.doe@example.com")
	if code != 4 || !strings.Contains(errOut, "mkto quota") {
		t.Errorf("exit %d, stderr: %s", code, errOut)
	}
}

// rowNames returns the first column of each table row after the "Lead <id>" line.
func rowNames(out string) []string {
	var names []string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n")[1:] {
		names = append(names, strings.Fields(l)[0])
	}
	return names
}

func TestLeadShowsProfileFields(t *testing.T) {
	srv := marketotest.New(t)
	srv.Handle(leadsPath, marketotest.Response{Body: "leads_one_scored.json"})
	out, errOut, code := fakeCLI(t, srv, "lead", "jane.doe@example.com")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	// After lead_fields; sfdcLeadOwnerId (absent), sfdcContactId (null) and sfdcAccountId ("") are skipped.
	want := []string{"id", "email", "firstName", "lastName", "createdAt", "updatedAt",
		"leadScore", "leadStatus", "unsubscribed", "emailInvalid", "sfdcLeadId"}
	if got := rowNames(out); !slices.Equal(got, want) {
		t.Errorf("rows = %v\n%s", got, out)
	}
	if !strings.Contains(out, "leadScore ") || !strings.Contains(out, " 85\n") || !strings.Contains(out, " MQL\n") {
		t.Errorf("out:\n%s", out)
	}
	out, _, _ = fakeCLI(t, srv, "lead", "jane.doe@example.com", "--json")
	var js []struct{ Fields map[string]any }
	if err := json.Unmarshal([]byte(out), &js); err != nil {
		t.Fatal(err)
	}
	if _, ok := js[0].Fields["sfdcContactId"]; ok || js[0].Fields["leadScore"] != 85.0 || js[0].Fields["leadStatus"] != "MQL" {
		t.Errorf("json fields: %v", js[0].Fields)
	}

	// A profile field that is also in lead_fields gets one row, in the lead_fields place.
	out, _, _ = fakeCLIConf(t, srv, time.Now, "profile_fields = [\"email\", \"leadScore\"]\n", "lead", "jane.doe@example.com")
	if got := rowNames(out); !slices.Equal(got, []string{"id", "email", "firstName", "lastName", "createdAt", "updatedAt", "leadScore"}) {
		t.Errorf("rows = %v", got)
	}

	// profile_fields = [] turns them off.
	out, _, _ = fakeCLIConf(t, srv, time.Now, "profile_fields = []\n", "lead", "jane.doe@example.com")
	if strings.Contains(out, "leadScore") || srv.Query(leadsPath).Get("fields") != "id,email,firstName,lastName,createdAt,updatedAt" {
		t.Errorf("fields = %q, out:\n%s", srv.Query(leadsPath).Get("fields"), out)
	}
}

func TestProfileFieldMissingSkipped(t *testing.T) {
	srv := marketotest.New(t)
	srv.Handle(leadsPath, marketotest.Response{Body: `{"success":false,"errors":[{"code":"1006","message":"Field 'leadScore' not found"}]}`},
		marketotest.Response{Body: "leads_one_scored.json"})
	out, errOut, code := fakeCLI(t, srv, "lead", "jane.doe@example.com", "--verbose")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if got := srv.Hits(leadsPath); got != 2 {
		t.Errorf("leads hits = %d, want 2 (one retry)", got)
	}
	if f := strings.Split(srv.Query(leadsPath).Get("fields"), ","); slices.Contains(f, "leadScore") || !slices.Contains(f, "leadStatus") {
		t.Errorf("retry fields = %v", f)
	}
	if !strings.Contains(errOut, "note: field \"leadScore\" isn't on this Marketo instance; skipped\n") {
		t.Errorf("stderr: %s", errOut)
	}
	if !strings.Contains(out, "leadStatus") {
		t.Errorf("out:\n%s", out)
	}
}

func TestLeadFieldsFlag(t *testing.T) {
	srv := marketotest.New(t)
	srv.Handle(leadsPath, marketotest.Response{Body: "leads_one_scored.json"})
	out, errOut, code := fakeCLI(t, srv, "lead", "jane.doe@example.com", "--fields", " email, leadScore,")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if got := srv.Query(leadsPath).Get("fields"); got != "email,leadScore,id,updatedAt" { // id is the lookup key; updatedAt is for duplicates
		t.Errorf("fields = %q", got)
	}
	if got := rowNames(out); !slices.Equal(got, []string{"email", "leadScore"}) || !strings.HasPrefix(out, "Lead 1001\n") {
		t.Errorf("rows = %v\n%s", got, out)
	}
	out, _, _ = fakeCLI(t, srv, "lead", "jane.doe@example.com", "--fields", "email,leadScore", "--json")
	var js []struct{ Fields map[string]any }
	if err := json.Unmarshal([]byte(out), &js); err != nil {
		t.Fatal(err)
	}
	if len(js[0].Fields) != 2 {
		t.Errorf("json fields: %v", js[0].Fields)
	}
	if _, _, code := fakeCLI(t, srv, "lead", "jane.doe@example.com", "--fields", " , "); code != 2 {
		t.Errorf("empty --fields: exit %d, want 2", code)
	}
}

func TestLeadFieldsFlagMissingErrors(t *testing.T) {
	srv := marketotest.New(t)
	srv.Handle(leadsPath, marketotest.Response{Body: `{"success":false,"errors":[{"code":"1006","message":"Field 'leadScore' not found"}]}`})
	_, errOut, code := fakeCLI(t, srv, "lead", "jane.doe@example.com", "--fields", "email,leadScore")
	if code != 1 || srv.Hits(leadsPath) != 1 {
		t.Errorf("exit %d, hits %d", code, srv.Hits(leadsPath))
	}
	if !strings.Contains(errOut, `field "leadScore" not found`) || !strings.Contains(errOut, "check --fields") || strings.Contains(errOut, "lead_fields") {
		t.Errorf("stderr: %s", errOut)
	}
}

func TestBadEmailNoCalls(t *testing.T) {
	for _, cmd := range []string{"lead", "activity", "brief"} {
		for _, bad := range []string{"jane.doe", "jane@example", "@example.com", "jane@", "jane@@example.com", "a@b@example.com", "jane doe@example.com", "jane@exa\tmple.com"} {
			s := marketotest.New(t)
			args := []string{cmd, bad}
			if cmd == "brief" { // past the "brief is off" gate
				args = append(args, "--show-payload")
			}
			_, errOut, code := fakeCLI(t, s, args...)
			if want := fmt.Sprintf("%q doesn't look like an email address", bad); code != 2 || !strings.Contains(errOut, want) {
				t.Errorf("%s %q: exit %d, stderr %q", cmd, bad, code, errOut)
			}
			if n := s.Hits(marketotest.IdentityPath); n != 0 {
				t.Errorf("%s %q: identity hits = %d", cmd, bad, n)
			}
		}
	}
	for _, ok := range []string{"jane+test@example.com", "jane.doe@mail.eu.example.com", "jürgen@bücher.example", "O'Neil@example.org"} {
		if err := checkEmail(ok); err != nil {
			t.Errorf("checkEmail(%q) = %v", ok, err)
		}
	}
}

func TestBaseURLSourceHint(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := &marketotest.Server{URL: "http://" + l.Addr().String()}
	l.Close()
	const hint = "check marketo.base_url and your network"
	_, errOut, code := fakeCLIFull(t, dead, time.Now, "", "", map[string]string{"MKTO_BASE_URL": "https://y.mktorest.com"}, "quota")
	if want := hint + " (base_url comes from MKTO_BASE_URL, set in the environment)\n"; code != 1 || !strings.HasSuffix(errOut, want) {
		t.Errorf("env: exit %d, stderr %q; want suffix %q", code, errOut, want)
	}
	if strings.Contains(errOut, "config.toml") {
		t.Errorf("env: stderr points at the config file: %q", errOut)
	}
	_, errOut, code = fakeCLIFull(t, dead, time.Now, "", "", nil, "quota")
	if want := filepath.Join("config.toml") + ")\n"; code != 1 || !strings.Contains(errOut, hint+" (base_url is in ") || !strings.HasSuffix(errOut, want) {
		t.Errorf("file: exit %d, stderr %q", code, errOut)
	}
}

const listsPath = "/rest/v1/leads/1001/listMembership.json"

// Static lists follow the profile, newest first; undated rows sort last.
func TestLeadListsGolden(t *testing.T) {
	srv := marketotest.New(t)
	srv.Handle(leadsPath, marketotest.Response{Body: "leads_one.json"})
	srv.Handle(listsPath, marketotest.Response{Body: `{"success":true,"result":[
{"listId":5001,"createdAt":"2026-09-01T10:00:00Z+0000"},
{"listId":5003,"createdAt":"junk"},
{"listId":5002,"createdAt":"2026-10-15T08:30:00Z+0000"}],"moreResult":false}`})
	out, _, code := fakeCLI(t, srv, "lead", "jane.doe@example.com", "--lists")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	golden(t, "lead_lists.txt", out)
	out, _, _ = fakeCLI(t, srv, "lead", "jane.doe@example.com", "--lists", "--json")
	golden(t, "lead_lists.json", out)
	if m := srv.Methods(); slices.ContainsFunc(m, func(s string) bool { return s != http.MethodGet }) {
		t.Errorf("non-GET request: %v", m)
	}

	srv.Handle(listsPath, marketotest.Response{Body: `{"success":true,"result":[],"moreResult":false}`})
	out, _, _ = fakeCLI(t, srv, "lead", "jane.doe@example.com", "--lists")
	if !strings.HasSuffix(out, "updatedAt  2026-10-01 16:00\n\nStatic lists: none\n") {
		t.Errorf("no lists:\n%s", out)
	}
	out, _, _ = fakeCLI(t, srv, "lead", "jane.doe@example.com", "--lists", "--json")
	if !strings.Contains(out, `"lists": []`) {
		t.Errorf("no lists --json:\n%s", out)
	}

	// Without --lists: no call, no key.
	srv = marketotest.New(t)
	srv.Handle(leadsPath, marketotest.Response{Body: "leads_one.json"})
	out, _, _ = fakeCLI(t, srv, "lead", "jane.doe@example.com", "--json")
	if srv.Hits(listsPath) != 0 || strings.Contains(out, `"lists"`) {
		t.Errorf("hits %d without --lists:\n%s", srv.Hits(listsPath), out)
	}
}

func TestLeadListsNeedsOneLead(t *testing.T) {
	srv := marketotest.New(t)
	srv.Handle(leadsPath, marketotest.Response{Body: "leads_two_duplicates.json"})
	srv.Handle("/rest/v1/leads/1002/listMembership.json", marketotest.Response{Body: "list_membership_one.json"})
	out, errOut, code := fakeCLI(t, srv, "lead", "jane.doe@example.com", "--lists")
	if code != 2 || out != "" || !strings.Contains(errOut, "--lists needs one lead: use --lead-id (ids: 1001, 1002)") || srv.Hits(listsPath) != 0 {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	out, _, code = fakeCLI(t, srv, "lead", "jane.doe@example.com", "--lists", "--lead-id", "1002")
	if code != 0 || !strings.HasPrefix(out, "Lead 1002\n") || !strings.Contains(out, "Static lists (1)\n") {
		t.Errorf("exit %d, stdout:\n%s", code, out)
	}
}

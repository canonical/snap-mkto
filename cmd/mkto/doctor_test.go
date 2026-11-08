// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
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
	"github.com/canonical/snap-mkto/internal/output"
)

// doctorFields are lead_fields the describe fixture has (it lacks id, createdAt and updatedAt).
const doctorFields = "lead_fields = [\"email\", \"firstName\", \"lastName\"]\n"

// doctorServer answers the four doctor calls; Handle again to change one.
func doctorServer(t *testing.T) *marketotest.Server {
	t.Helper()
	s := marketotest.New(t)
	s.Handle(typesPath, marketotest.Response{Body: "activity_types_story.json"})
	s.Handle(describePath, marketotest.Response{Body: "leads_describe.json"})
	s.Handle(programsPath, marketotest.Response{Body: `{"requestId":"r","success":true,"result":[{"id":2001,"name":"Example Webinar 2026"}]}`})
	return s
}

func doctor(t *testing.T, s *marketotest.Server, extra string, args ...string) (string, string, int) {
	t.Helper()
	return fakeCLIConf(t, s, time.Now, extra, append([]string{"doctor"}, args...)...)
}

// tail splits off the first line, which shows the temp config path, and returns the path and the rest.
func tail(t *testing.T, out string) (path, rest string) {
	t.Helper()
	first, rest, _ := strings.Cut(out, "\n")
	path, ok := strings.CutPrefix(first, "✓ config: ")
	if !ok || !strings.HasSuffix(path, "config.toml") {
		t.Errorf("first line = %q", first)
	}
	return path, rest
}

func TestDoctorAllGood(t *testing.T) {
	s := doctorServer(t)
	out, errOut, code := doctor(t, s, doctorFields)
	want := "✓ credentials: access token fetched\n" +
		"✓ activity types: 10 types\n" +
		"✓ lead fields: 14 fields\n" +
		"✓ assets: programs readable\n" +
		"✓ names: every configured field and activity type is on this instance\n"
	if _, rest := tail(t, out); code != 0 || rest != want || errOut != "" {
		t.Errorf("exit %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	for _, p := range []string{marketotest.IdentityPath, typesPath, describePath, programsPath} {
		if s.Hits(p) != 1 {
			t.Errorf("%s hits = %d, want 1", p, s.Hits(p))
		}
	}
	if q := s.Query(programsPath); q.Get("maxReturn") != "1" || q.Has("offset") {
		t.Errorf("programs query = %v", q)
	}
	if m := s.Methods(); len(m) != 4 || slices.ContainsFunc(m, func(x string) bool { return x != http.MethodGet }) {
		t.Errorf("calls = %v, want 4 GETs", m)
	}

	out, _, code = doctor(t, doctorServer(t), doctorFields, "--json")
	var got output.DoctorJSON
	if err := json.Unmarshal([]byte(out), &got); err != nil || code != 0 {
		t.Fatalf("exit %d, %v:\n%s", code, err, out)
	}
	var names []string
	for _, c := range got.Checks {
		names = append(names, c.Name)
		if c.Status != "ok" || c.Detail == "" {
			t.Errorf("check %+v", c)
		}
	}
	if !slices.Equal(names, []string{"config", "credentials", "activity types", "lead fields", "assets", "names"}) {
		t.Errorf("names = %v", names)
	}
}

func TestDoctorAssetDenied(t *testing.T) {
	const denied = "✗ assets: access denied. Ask Marketing Ops to give the API role Read-Only Assets (needed for program names, program tokens and smart campaign names)\n"
	for name, r := range map[string]marketotest.Response{
		"603": {Body: "error_603.json"},
		"401": {Status: 401, Body: `{}`},
		"403": {Status: 403, Body: `{}`},
	} {
		s := doctorServer(t)
		s.Handle(programsPath, r)
		out, errOut, code := doctor(t, s, doctorFields)
		// The names check still runs after a failed assets check.
		if code != 1 || !strings.Contains(out, "✓ lead fields: 14 fields\n"+denied+"✓ names: ") || errOut != "1 check failed\n" {
			t.Errorf("%s: exit %d\nstdout:\n%s\nstderr:\n%s", name, code, out, errOut)
		}
	}

	// Checks 3 to 5 all run when one fails; the names check needs both lists, so it is skipped.
	s := doctorServer(t)
	s.Handle(describePath, marketotest.Response{Body: `{"success":false,"errors":[{"code":"612","message":"Invalid Content Type"}]}`})
	s.Handle(programsPath, marketotest.Response{Body: "error_603.json"})
	out, errOut, code := doctor(t, s, doctorFields)
	want := "✓ credentials: access token fetched\n" +
		"✓ activity types: 10 types\n" +
		"✗ lead fields: Marketo error 612: Invalid Content Type\n" + denied
	if _, rest := tail(t, out); code != 1 || rest != want || errOut != "2 checks failed\n" {
		t.Errorf("exit %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
}

func TestDoctorMissingFields(t *testing.T) {
	extra := "lead_fields = [\"email\", \"bogusField\", \"FIRSTNAME\"]\n" +
		"profile_fields = [\"leadScore\", \"noSuchField\", \"otherMissing\"]\n" +
		"change_fields = [\"lead status\", \"Nope Label\"]\n" +
		"activity_types = [\"Fill Out Form\", \"Chnage Score\"]\n"
	out, errOut, code := doctor(t, doctorServer(t), extra)
	path, rest := tail(t, out)
	lines := strings.Split(rest, "\n")
	if code != 1 || len(lines) != 9 || errOut != "1 check failed\n" {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	for i, want := range []string{
		"✗ lead_fields: not on this instance: bogusField. `mkto lead` fails until you fix lead_fields in " + path + " (`mkto fields` lists the names)",
		"! profile_fields: not on this instance: noSuchField, otherMissing. Each missing profile field costs one extra API call per lead lookup; remove it from profile_fields in " + path,
		"! change_fields: no field has this label on this instance: Nope Label. Fix change_fields in " + path + " (`mkto fields` lists the labels)",
		`! activity_types: not on this instance: Chnage Score (did you mean "Change Score"?). Fix activity_types in ` + path + " (`mkto types` lists the names)",
	} {
		if lines[4+i] != want {
			t.Errorf("line %d:\n got %q\nwant %q", 4+i, lines[4+i], want)
		}
	}

	// A defaulted list says the defaults are missing; a warning only, so exit 0.
	s := doctorServer(t)
	s.Handle(typesPath, marketotest.Response{Body: `{"success":true,"result":[{"id":2,"name":"Fill Out Form"},{"id":10,"name":"Open Email"}]}`})
	out, errOut, code = doctor(t, s, doctorFields)
	path, _ = tail(t, out)
	want := "! activity_types: default activity types not on this instance: Click Email, Email Bounced, Email Bounced Soft, Unsubscribe Email, Change Score, Change Data Value, Change Status in Progression, New Lead. " +
		"mkto skips them; to choose your own, set activity_types in " + path + " (`mkto types` lists the names)\n"
	if code != 0 || !strings.HasSuffix(out, want) || errOut != "" {
		t.Errorf("defaulted: exit %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}

	// An unedited v0.1.0 list is migrated: doctor says so.
	legacy := "activity_types = [\"Visit Webpage\", \"Fill Out Form\", \"Click Link\", \"Email Delivered\", \"Open Email\", \"Click Email\", " +
		"\"Unsubscribe Email\", \"Change Status in Progression\", \"New Lead\"]\n"
	out, _, code = doctor(t, doctorServer(t), doctorFields+legacy)
	path, _ = tail(t, out)
	if want := "! activity_types: activity_types is the unedited v0.1.0 list, so mkto uses the 0.2.0 defaults; delete activity_types from " + path + " to drop this warning\n"; code != 0 || !strings.HasSuffix(out, want) {
		t.Errorf("migrated: exit %d\n%s", code, out)
	}

	// None resolving is a failure: activity has nothing to fetch.
	out, _, code = doctor(t, doctorServer(t), doctorFields+"activity_types = [\"Nope\"]\n", "--json")
	var got output.DoctorJSON
	if err := json.Unmarshal([]byte(out), &got); err != nil || code != 1 {
		t.Fatalf("exit %d, %v:\n%s", code, err, out)
	}
	if c := got.Checks[len(got.Checks)-1]; c.Name != "activity_types" || c.Status != "fail" || !strings.Contains(c.Detail, "none of activity_types is on this instance: Nope.") {
		t.Errorf("none resolve: %+v", c)
	}
}

func TestDoctorBadCredentials(t *testing.T) {
	s := doctorServer(t)
	s.Handle(marketotest.IdentityPath, marketotest.Response{Status: 401, Body: `{"error":"unauthorized","error_description":"Bad client credentials"}`})
	out, errOut, code := doctor(t, s, doctorFields)
	want := "✗ credentials: Marketo authentication failed (HTTP 401: Bad client credentials): check client_id and client_secret in "
	if _, rest := tail(t, out); code != 1 || !strings.HasPrefix(rest, want) || strings.Count(rest, "\n") != 1 || errOut != "1 check failed\n" {
		t.Errorf("exit %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	if strings.Contains(out+errOut, "s3cret") {
		t.Error("output contains the client secret")
	}
	if s.Hits(typesPath) != 0 {
		t.Errorf("types hits = %d after a failed token", s.Hits(typesPath))
	}

	// A config that doesn't load stops before any network call.
	s = doctorServer(t)
	missing := filepath.Join(t.TempDir(), "nope.toml")
	out, errOut, code = doctor(t, s, "", "--config", missing)
	if code != 1 || out != "✗ config: no config file at "+missing+": run `mkto init` to create one\n" || errOut != "1 check failed\n" {
		t.Errorf("no config: exit %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	if s.Hits(marketotest.IdentityPath) != 0 {
		t.Error("doctor called Marketo without a config")
	}
}

func TestDoctorQuotaStops(t *testing.T) {
	s := doctorServer(t)
	s.Handle(typesPath, marketotest.Response{Body: "error_607.json"})
	out, errOut, code := doctor(t, s, doctorFields)
	if code != 4 || out != "" || !strings.Contains(errOut, "daily API quota reached") {
		t.Errorf("exit %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	if s.Hits(describePath) != 0 || s.Hits(programsPath) != 0 {
		t.Errorf("calls after 607: describe %d, programs %d", s.Hits(describePath), s.Hits(programsPath))
	}
}

func TestDoctorDefaultLeadFieldsPass(t *testing.T) {
	// leads_describe.json lacks id, createdAt and updatedAt; the default lead_fields must still pass.
	names := map[string]string{}
	var got []string
	checkNames(&config.Config{Marketo: config.Marketo{LeadFields: config.DefaultLeadFields}}, "cfg.toml", nil,
		[]marketo.Field{{Name: "email"}, {Name: "firstName"}, {Name: "lastName"}, {Name: "title"}, {Name: "company"},
			{Name: "industry"}, {Name: "country"}, {Name: "leadSource"}},
		func(name, status, detail string) { names[name] = status; got = append(got, name+": "+detail) })
	if names["lead_fields"] != "" {
		t.Errorf("default lead_fields flagged: %v", got)
	}
}

func TestDoctorWarnsLooseConfig(t *testing.T) {
	s := doctorServer(t)
	p := filepath.Join(t.TempDir(), "config.toml")
	cfg := "[marketo]\nbase_url = \"https://x.mktorest.com\"\nclient_id = \"id\"\nclient_secret = \"s3cret\"\n" + doctorFields
	if err := os.WriteFile(p, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := run([]string{"--config", p, "doctor", "--json"}, Env{Stdout: &out, Stderr: &errb, Getenv: func(string) string { return "" },
		HTTP: &http.Client{Transport: toFake{s.URL}}, Now: time.Now, Sleep: func(context.Context, time.Duration) error { return nil }})
	if code != 0 || !strings.Contains(out.String(), `"name": "config permissions"`) || !strings.Contains(out.String(), "run: chmod 600 "+p) {
		t.Errorf("exit %d, want a config permissions warning:\n%s", code, out.String())
	}
}

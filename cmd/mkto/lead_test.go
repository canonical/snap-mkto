// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
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
	cfg := "[marketo]\nbase_url = \"https://x.mktorest.com\"\nclient_id = \"id\"\nclient_secret = \"s3cret\"\n" + extra +
		"lead_fields = [\"id\", \"email\", \"firstName\", \"lastName\", \"createdAt\", \"updatedAt\"]\n" +
		"[output]\ntimezone = \"Asia/Tbilisi\"\n" + tail
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
	if got := srv.Query(leadsPath).Get("fields"); got != "id,email,firstName,lastName,createdAt,updatedAt" {
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

func TestLeadDuplicates(t *testing.T) {
	srv := marketotest.New(t)
	srv.Handle(leadsPath, marketotest.Response{Body: "leads_two_duplicates.json"})
	out, _, code := fakeCLI(t, srv, "lead", "jane.doe@example.com")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	golden(t, "lead_duplicates.txt", out)
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

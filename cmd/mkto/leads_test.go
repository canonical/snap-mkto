// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
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

// writeInput writes an --from file in a temp dir and returns its path.
func writeInput(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "emails.csv")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// leadsFound is two leads sharing jane's email, one for john, and a status that a
// spreadsheet would run as a formula.
const leadsFound = `{"success":true,"result":[
{"id":1001,"email":"jane.doe@example.com","leadScore":85,"leadStatus":"MQL","leadOwnerEmailAddress":"owner@example.com","updatedAt":"2026-10-01T12:00:00Z"},
{"id":1002,"email":"Jane.Doe@example.com","leadScore":10,"updatedAt":"2026-06-15T08:30:00Z"},
{"id":1003,"email":"john.roe@example.com","leadScore":-5,"leadStatus":"=1+1","updatedAt":"2026-09-01T00:00:00Z"}]}`

func TestLeadsFromFileChunks(t *testing.T) {
	for name, local := range map[string]string{
		"count": "a",                     // 301 short emails: 300 per call
		"bytes": strings.Repeat("x", 60), // 150 long emails: over 7 KB in one URL
	} {
		t.Run(name, func(t *testing.T) {
			n := 301
			if name == "bytes" {
				n = 150
			}
			var b strings.Builder
			for i := range n {
				fmt.Fprintf(&b, "%s%d@example.com\n", local, i)
			}
			srv := marketotest.New(t)
			srv.Handle(leadsPath, marketotest.Response{Body: "leads_zero.json"})
			_, errOut, code := fakeCLI(t, srv, "leads", "--from", writeInput(t, b.String()))
			if code != 0 {
				t.Fatalf("exit %d: %s", code, errOut)
			}
			if got := srv.Hits(leadsPath); got != 2 {
				t.Errorf("leads.json hits = %d, want 2", got)
			}
			q := srv.Query(leadsPath)
			if u := "https://x.mktorest.com" + leadsPath + "?" + q.Encode(); len(u) >= 7000 {
				t.Errorf("URL is %d bytes", len(u))
			}
			if q.Get("filterType") != "email" || len(strings.Split(q.Get("filterValues"), ",")) > 300 {
				t.Errorf("query: filterType %q, %d values", q.Get("filterType"), len(strings.Split(q.Get("filterValues"), ",")))
			}
		})
	}
}

func TestLeadsNotFoundRow(t *testing.T) {
	srv := marketotest.New(t)
	srv.Handle(leadsPath, marketotest.Response{Body: "leads_one.json"})
	in := "email\nmissing@example.com\n jane.doe@example.com \nJANE.DOE@example.com\nbad@nodot\n\n"
	out, errOut, code := fakeCLI(t, srv, "leads", "--from", writeInput(t, in))
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if got := srv.Query(leadsPath).Get("filterValues"); got != "missing@example.com,jane.doe@example.com" {
		t.Errorf("filterValues = %q", got)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "EMAIL ") ||
		!strings.Contains(lines[1], "missing@example.com") || !strings.Contains(lines[1], "not found") ||
		!strings.Contains(lines[2], "jane.doe@example.com") || !strings.Contains(lines[2], "1001") {
		t.Errorf("table:\n%s", out)
	}
	for _, want := range []string{"1 line without @", "1 invalid email address"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr missing %q:\n%s", want, errOut)
		}
	}
	if strings.Contains(errOut, "example.com") || strings.Contains(errOut, "nodot") {
		t.Errorf("stderr names an address:\n%s", errOut)
	}
}

func TestLeadsNoEmailsNoCalls(t *testing.T) {
	srv := marketotest.New(t)
	_, errOut, code := fakeCLI(t, srv, "leads", "--from", writeInput(t, "email\nbad@nodot\n"))
	if code != 2 || srv.Hits(marketotest.IdentityPath) != 0 {
		t.Errorf("exit %d, identity hits %d: %s", code, srv.Hits(marketotest.IdentityPath), errOut)
	}
}

func TestLeadsCSVGolden(t *testing.T) {
	srv := marketotest.New(t)
	srv.Handle(leadsPath, marketotest.Response{Body: leadsFound})
	in := "Email,Name\njohn.roe@example.com,John\n\"jane.doe@example.com\",\"Doe, Jane\"\nnobody@example.com,\n"
	from := writeInput(t, in)

	out, errOut, code := fakeCLI(t, srv, "leads", "--from", from, "--csv")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	golden(t, "leads.csv", out)

	outPath := filepath.Join(t.TempDir(), "leads.csv")
	if _, errOut, code = fakeCLI(t, srv, "leads", "--from", from, "--csv", "--out", outPath); code != 0 || !strings.Contains(errOut, "wrote ") {
		t.Fatalf("--out: exit %d: %s", code, errOut)
	}
	if b, _ := os.ReadFile(outPath); string(b) != out {
		t.Errorf("--out file differs from stdout:\n%s", b)
	}

	out, _, _ = fakeCLI(t, srv, "leads", "--from", from)
	golden(t, "leads.txt", out)
	out, _, _ = fakeCLI(t, srv, "leads", "--from", from, "--json")
	golden(t, "leads.json", out)
	var js struct {
		Leads    []json.RawMessage `json:"leads"`
		NotFound []string          `json:"notFound"`
	}
	if err := json.Unmarshal([]byte(out), &js); err != nil || len(js.Leads) != 3 || len(js.NotFound) != 1 {
		t.Errorf("json: %v, %d leads, notFound %v", err, len(js.Leads), js.NotFound)
	}

	if _, _, code = fakeCLI(t, srv, "leads", "--from", from, "--csv", "--json"); code != 2 {
		t.Errorf("--csv --json: exit %d, want 2", code)
	}
}

func TestLeadsStdin(t *testing.T) {
	srv := marketotest.New(t)
	srv.Handle(leadsPath, marketotest.Response{Body: "leads_one.json"})
	p := filepath.Join(t.TempDir(), "config.toml")
	cfg := "[marketo]\nbase_url = \"https://x.mktorest.com\"\nclient_id = \"id\"\nclient_secret = \"s3cret\"\n"
	if err := os.WriteFile(p, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := run([]string{"--config", p, "leads", "--from", "-"}, Env{
		Stdin: strings.NewReader("\ufeffjane.doe@example.com\r\n"), Stdout: &out, Stderr: &errb,
		Getenv: func(string) string { return "" },
		HTTP:   &http.Client{Transport: toFake{srv.URL}},
		Now:    time.Now,
		Sleep:  func(context.Context, time.Duration) error { return nil },
	})
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if q := srv.Query(leadsPath); q.Get("filterValues") != "jane.doe@example.com" {
		t.Errorf("filterValues = %q", q.Get("filterValues"))
	}
	if !strings.Contains(out.String(), "1001") {
		t.Errorf("stdout:\n%s", out.String())
	}
}

// A missing profile field costs one retry per run, not one per chunk.
func TestLeadsDropsMissingProfileFieldOnce(t *testing.T) {
	var b strings.Builder
	for i := range 301 {
		fmt.Fprintf(&b, "a%d@example.com\n", i)
	}
	srv := marketotest.New(t)
	srv.Handle(leadsPath, marketotest.Response{Body: "leads_1006_response.json"}, marketotest.Response{Body: "leads_zero.json"})
	_, errOut, code := fakeCLIConf(t, srv, time.Now, "profile_fields = [\"leadScore\", \"bogusField\"]\n", "leads", "--from", writeInput(t, b.String()))
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if got := srv.Hits(leadsPath); got != 3 {
		t.Errorf("leads.json hits = %d, want 3", got)
	}
	if f := srv.Query(leadsPath).Get("fields"); strings.Contains(f, "bogusField") || !strings.Contains(f, "leadScore") {
		t.Errorf("fields = %q", f)
	}
}

func TestLeadsActivityBatches30(t *testing.T) {
	var b strings.Builder
	for i := range 31 {
		fmt.Fprintf(&b, "a%d@example.com\n", i)
	}
	srv := accountServer(t, marketotest.Response{Body: manyLeads(31)})
	out, errOut, code := fakeCLINow(t, srv, fixedNow, "leads", "--from", writeInput(t, b.String()), "--activity")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if srv.Hits(leadsPath) != 1 || srv.Hits(actsPath) != 6 || srv.Hits(tokenPath) != 6 { // 2 lead batches × 3 type batches
		t.Errorf("hits: leads %d, activities %d, pagingtoken %d", srv.Hits(leadsPath), srv.Hits(actsPath), srv.Hits(tokenPath))
	}
	if got := actLeadIDs(srv); len(got) != 2 || !slices.Contains(got, "2031") { // input order: a30 is alone in the last batch
		t.Errorf("lead batches = %q", got)
	}
	if !strings.Contains(strings.SplitN(out, "\n", 2)[0], "LAST TOUCH  14D") {
		t.Errorf("header: %q", strings.SplitN(out, "\n", 2)[0])
	}

	srv = accountServer(t, marketotest.Response{Body: manyLeads(31)})
	if _, _, _ = fakeCLINow(t, srv, fixedNow, "leads", "--from", writeInput(t, b.String())); srv.Hits(actsPath) != 0 {
		t.Errorf("without --activity: %d activities calls", srv.Hits(actsPath))
	}
	if _, errOut, code := fakeCLI(t, srv, "leads", "--from", writeInput(t, b.String()), "--web"); code != 2 || errOut != "--web only matters with --activity\n" {
		t.Errorf("--web alone: exit %d, stderr %q", code, errOut)
	}

	// No lead found: no activity types or activities calls (the fake fails on unhandled paths).
	srv = marketotest.New(t)
	srv.Handle(leadsPath, marketotest.Response{Body: "leads_zero.json"})
	if _, errOut, code := fakeCLI(t, srv, "leads", "--from", writeInput(t, b.String()), "--activity"); code != 0 {
		t.Errorf("none found: exit %d: %s", code, errOut)
	}
}

func TestLeadsOutRemovedOnFailure(t *testing.T) {
	for _, force := range []bool{false, true} {
		srv := marketotest.New(t)
		srv.Handle(leadsPath, marketotest.Response{Status: http.StatusInternalServerError})
		p := filepath.Join(t.TempDir(), "out.csv")
		args := []string{"leads", "--from", writeInput(t, "jane.doe@example.com\n"), "--csv", "--out", p}
		if force {
			args = append(args, "--force")
		}
		if _, _, code := fakeCLI(t, srv, args...); code == 0 {
			t.Errorf("force=%v: exit 0", force)
		}
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("force=%v: file left behind: %v", force, err)
		}
	}
}

// TestLeadsOwnerFromSfdcLeadOwnerId: with no owner email, OWNER shows the Salesforce owner id.
func TestLeadsOwnerFromSfdcLeadOwnerId(t *testing.T) {
	srv := marketotest.New(t)
	srv.Handle(leadsPath, marketotest.Response{Body: `{"success":true,"result":[
{"id":1001,"email":"jane.doe@example.com","leadScore":85,"sfdcLeadOwnerId":"005000000000001AAA","updatedAt":"2026-10-01T12:00:00Z"}]}`})
	out, errOut, code := fakeCLI(t, srv, "leads", "--from", writeInput(t, "jane.doe@example.com\n"))
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(out, " 005000000000001AAA ") {
		t.Errorf("owner id missing:\n%s", out)
	}
	if f := strings.Split(srv.Query(leadsPath).Get("fields"), ","); !slices.Contains(f, "sfdcLeadOwnerId") {
		t.Errorf("fields = %v", f)
	}
}

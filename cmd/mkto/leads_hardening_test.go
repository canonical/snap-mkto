// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/canonical/snap-mkto/internal/marketotest"
)

// leadsStdin runs `mkto leads --from -` with stdin and returns stdout, stderr, exit code.
func leadsStdin(t *testing.T, srv *marketotest.Server, stdin io.Reader, args ...string) (string, string, int) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	cfg := "[marketo]\nbase_url = \"https://x.mktorest.com\"\nclient_id = \"id\"\nclient_secret = \"s3cret\"\n"
	if err := os.WriteFile(p, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := run(append([]string{"--config", p, "leads", "--from", "-"}, args...), Env{
		Stdin: stdin, Stdout: &out, Stderr: &errb,
		Getenv: func(string) string { return "" },
		HTTP:   &http.Client{Transport: toFake{srv.URL}},
		Now:    time.Now,
		Sleep:  func(context.Context, time.Duration) error { return nil },
	})
	return out.String(), errb.String(), code
}

func TestLeadsCSVCleansControlChars(t *testing.T) {
	srv := marketotest.New(t)
	srv.Handle(leadsPath, marketotest.Response{Body: `{"requestId":"r","success":true,"result":[{"id":1001,"email":"jane.doe@example.com","leadStatus":"ok\u001b]0;pwned\u0007"}]}`})
	out, errOut, code := leadsStdin(t, srv, strings.NewReader("jane.doe@example.com\n"), "--csv")
	if code != 0 || strings.ContainsAny(out, "\x1b\a") {
		t.Errorf("exit %d, control characters reached stdout: %q (stderr %q)", code, out, errOut)
	}
}

func TestLeadsInputCaps(t *testing.T) {
	var many strings.Builder
	for i := range maxLeadEmails + 1 {
		many.WriteString("u" + strconv.Itoa(i) + "@example.com\n")
	}
	for name, in := range map[string]io.Reader{
		"too many emails": strings.NewReader(many.String()),
		"too many bytes":  io.LimitReader(zeroes{}, maxLeadsInput+1),
	} {
		srv := marketotest.New(t)
		_, errOut, code := leadsStdin(t, srv, in)
		if code != 2 || srv.Hits(marketotest.IdentityPath) != 0 || !strings.Contains(errOut, "split the list") {
			t.Errorf("%s: exit %d, identity hits %d, stderr %q", name, code, srv.Hits(marketotest.IdentityPath), errOut)
		}
	}
}

type zeroes struct{}

func (zeroes) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'a'
	}
	return len(p), nil
}

func TestCheckEmailRejectsCommaControlAndLength(t *testing.T) {
	for _, bad := range []string{"c,d@example.com", "j\x00a@example.com", "a\u001b@example.com", strings.Repeat("a", 250) + "@example.com"} {
		if checkEmail(bad) == nil {
			t.Errorf("checkEmail(%q) = nil, want an error", bad)
		}
	}
}

func TestLeadsBOMBeforeQuotedCell(t *testing.T) {
	srv := marketotest.New(t)
	srv.Handle(leadsPath, marketotest.Response{Body: "leads_one.json"})
	if _, errOut, code := leadsStdin(t, srv, strings.NewReader("\uFEFF\"jane.doe@example.com\"\n")); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if got := srv.Query(leadsPath).Get("filterValues"); got != "jane.doe@example.com" {
		t.Errorf("filterValues = %q, want the email without quotes", got)
	}
}

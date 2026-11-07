// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/canonical/snap-mkto/internal/marketotest"
)

const (
	programsPath = "/rest/asset/v1/programs.json"
	usagePath    = "/rest/v1/stats/usage.json"
	usage7Path   = "/rest/v1/stats/usage/last7days.json"
)

func programsPage(first, n int) string {
	rows := make([]string, n)
	for i := range rows {
		id := first + i
		rows[i] = fmt.Sprintf(`{"id":%d,"name":"Program %d","type":"Default","channel":"Email","workspace":"Default","createdAt":"2026-01-05T23:30:00Z"}`, id, id)
	}
	return `{"success":true,"result":[` + strings.Join(rows, ",") + `]}`
}

func TestProgramsOffsetPaging(t *testing.T) {
	s := marketotest.New(t)
	s.Handle(programsPath,
		marketotest.Response{Body: programsPage(1, 200)}, marketotest.Response{Body: programsPage(201, 200)},
		marketotest.Response{Body: programsPage(401, 13)}, marketotest.Response{Body: "programs_browse_empty.json"})
	out, _, code := fakeCLI(t, s, "programs", "--json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if n := strings.Count(out, `"id":`); n != 413 {
		t.Errorf("%d rows, want 413", n)
	}
	if h := s.Hits(programsPath); h != 4 {
		t.Errorf("%d hits, want 4", h)
	}
	if q := s.Query(programsPath); q.Get("maxReturn") != "200" || q.Get("offset") != "413" {
		t.Errorf("last query %v", q)
	}
}

func TestProgramsPagingGuard(t *testing.T) {
	s := marketotest.New(t)
	s.Handle(programsPath, marketotest.Response{Body: programsPage(1, 1)})
	_, errOut, code := fakeCLI(t, s, "programs")
	if code != 1 || !strings.Contains(errOut, "1000 pages") {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
}

func TestProgramsNameFilter(t *testing.T) {
	s := marketotest.New(t)
	s.Handle(programsPath, marketotest.Response{Body: `{"success":true,"result":[{"id":1,"name":"2026-Q4 Webinar","type":"Default","channel":"Webinar","workspace":"Default","createdAt":"2026-01-05T10:00:00Z"},{"id":2,"name":"Other","type":"Default","channel":"Email","workspace":"Default","createdAt":"2026-01-05T10:00:00Z"}]}`},
		marketotest.Response{Body: "programs_browse_empty.json"})
	out, _, _ := fakeCLI(t, s, "programs", "--name", "WEBinar")
	if !strings.Contains(out, "2026-Q4 Webinar") || strings.Contains(out, "Other") {
		t.Errorf("got:\n%s", out)
	}
	out, _, code := fakeCLI(t, s, "programs", "--name", "zzz")
	if code != 0 || out != "No programs match \"zzz\".\n" {
		t.Errorf("exit %d, got %q", code, out)
	}
}

func TestProgramsGolden(t *testing.T) {
	s := marketotest.New(t)
	s.Handle(programsPath, marketotest.Response{Body: "programs_browse_page.json"}, marketotest.Response{Body: "programs_browse_empty.json"})
	out, _, _ := fakeCLI(t, s, "programs")
	golden(t, "programs.txt", out)
	s = marketotest.New(t) // fresh server: Handle doesn't reset the hit count
	s.Handle(programsPath, marketotest.Response{Body: "programs_browse_page.json"}, marketotest.Response{Body: "programs_browse_empty.json"})
	out, _, _ = fakeCLI(t, s, "programs", "--json")
	golden(t, "programs.json", out)
	if m := s.Methods(); slices.ContainsFunc(m, func(x string) bool { return x != http.MethodGet }) {
		t.Errorf("unexpected method: %v", m)
	}
	s = marketotest.New(t)
	s.Handle(programsPath, marketotest.Response{Body: "programs_browse_empty.json"})
	out, _, _ = fakeCLI(t, s, "programs", "--json")
	if out != "[]\n" {
		t.Errorf("empty JSON = %q", out)
	}
}

func quotaServer(t *testing.T, today string) *marketotest.Server {
	s := marketotest.New(t)
	s.Handle(usagePath, marketotest.Response{Body: today})
	s.Handle(usage7Path, marketotest.Response{Body: "usage_last7days.json"})
	return s
}

func TestQuotaGolden(t *testing.T) {
	s := quotaServer(t, "usage_today.json")
	out, _, code := fakeCLINow(t, s, fixedNow, "quota")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "daily_quota in ") || !strings.Contains(out, "Marketo doesn't report it") {
		t.Errorf("missing limit note:\n%s", out)
	}
	golden(t, "quota.txt", regexp.MustCompile(`in \S+config\.toml;`).ReplaceAllString(out, "in <config>;"))
	out, _, _ = fakeCLINow(t, s, fixedNow, "quota", "--json")
	golden(t, "quota.json", out)
}

func TestQuotaChicagoDay(t *testing.T) {
	s := quotaServer(t, `{"success":true,"result":[{"date":"2026-11-06","total":10},{"date":"2026-11-07","total":99}]}`)
	now := func() time.Time { return time.Date(2026, 11, 7, 5, 30, 0, 0, time.UTC) }
	out, _, _ := fakeCLINow(t, s, now, "quota")
	if !strings.HasPrefix(out, "Today (2026-11-06, US Central): 10 of 50000 calls used") {
		t.Errorf("got:\n%s", out)
	}
}

func TestQuotaNoRowToday(t *testing.T) {
	s := quotaServer(t, `{"success":true,"result":[]}`)
	out, _, _ := fakeCLINow(t, s, fixedNow, "quota")
	if !strings.Contains(out, "0 of 50000 calls used (0.0%), 50000 remaining") {
		t.Errorf("got:\n%s", out)
	}
}

func TestQuotaOverLimit(t *testing.T) {
	s := quotaServer(t, "usage_today.json")
	out, errOut, code := fakeCLIConf(t, s, fixedNow, "daily_quota = 1000\n", "quota")
	if code != 0 || !strings.Contains(out, "1120 of 1000 calls used (112.0%), 0 remaining") {
		t.Errorf("exit %d, got:\n%s", code, out)
	}
	if !strings.Contains(errOut, "warning: usage is above daily_quota; check daily_quota in ") {
		t.Errorf("stderr %q", errOut)
	}
}

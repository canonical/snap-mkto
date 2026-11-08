// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/canonical/snap-mkto/internal/marketotest"
)

const describePath = "/rest/v1/leads/describe.json"

func fieldsServer(t *testing.T, body string) *marketotest.Server {
	t.Helper()
	s := marketotest.New(t)
	s.Handle(describePath, marketotest.Response{Body: body})
	return s
}

func TestFieldsGolden(t *testing.T) {
	s := fieldsServer(t, "leads_describe.json")
	out, errOut, code := fakeCLI(t, s, "fields")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	golden(t, "fields.txt", out)
	if s.Hits(describePath) != 1 || s.Hits(marketotest.IdentityPath) != 1 {
		t.Errorf("describe hits %d, identity hits %d", s.Hits(describePath), s.Hits(marketotest.IdentityPath))
	}
	if m := s.Methods(); slices.ContainsFunc(m, func(x string) bool { return x != http.MethodGet }) {
		t.Errorf("unexpected method: %v", m)
	}
	out, _, _ = fakeCLI(t, fieldsServer(t, "leads_describe.json"), "fields", "--json")
	golden(t, "fields.json", out)

	// A field with no rest child can't be used over REST: skipped, and counted in text only.
	const noRest = `{"requestId":"r","success":true,"result":[
		{"id":1,"displayName":"Zeta","dataType":"string","rest":{"name":"zeta","readOnly":false}},
		{"id":2,"displayName":"SOAP Only","dataType":"string","soap":{"name":"SoapOnly","readOnly":false}},
		{"id":3,"displayName":"Alpha","dataType":"string","rest":{"name":"Alpha","readOnly":true}}]}`
	out, errOut, code = fakeCLI(t, fieldsServer(t, noRest), "fields")
	want := "NAME   LABEL  TYPE    LENGTH  READ-ONLY\nAlpha  Alpha  string  -       yes\nzeta   Zeta   string  -       -\n"
	if code != 0 || out != want || errOut != "note: 1 field has no REST name and is not shown\n" {
		t.Errorf("no-rest: exit %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	out, errOut, _ = fakeCLI(t, fieldsServer(t, noRest), "fields", "--json")
	if strings.Contains(out, "SOAP") || strings.Contains(out, "note") || strings.Count(out, `"name"`) != 2 || errOut != "" {
		t.Errorf("no-rest JSON:\n%s\nstderr: %q", out, errOut)
	}
}

func TestFieldsSearch(t *testing.T) {
	out, _, code := fakeCLI(t, fieldsServer(t, "leads_describe.json"), "fields", "--search", "score")
	want := "NAME              LABEL           TYPE     LENGTH  READ-ONLY\n" +
		"behaviorScore__c  Behavior Score  integer  -       -\n" +
		"leadScore         Lead Score      integer  -       -\n"
	if code != 0 || out != want {
		t.Errorf("score: exit %d\n%s", code, out)
	}
	// The label matches too: "Address" is only in labels.
	out, _, _ = fakeCLI(t, fieldsServer(t, "leads_describe.json"), "fields", "--search", "ADDRESS")
	if strings.Count(out, "\n") != 3 || !strings.Contains(out, "\nemail ") || !strings.Contains(out, "\nleadOwnerEmailAddress ") {
		t.Errorf("label search:\n%s", out)
	}
	out, _, code = fakeCLI(t, fieldsServer(t, "leads_describe.json"), "fields", "--search", "zzz")
	if code != 0 || out != "No lead fields match \"zzz\".\n" {
		t.Errorf("no match: exit %d, %q", code, out)
	}
	out, _, _ = fakeCLI(t, fieldsServer(t, "leads_describe.json"), "fields", "--search", "zzz", "--json")
	if out != "[]\n" {
		t.Errorf("no match JSON = %q", out)
	}
}

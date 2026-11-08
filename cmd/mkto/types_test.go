// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/canonical/snap-mkto/internal/marketo"
	"github.com/canonical/snap-mkto/internal/marketotest"
)

func typesServer(t *testing.T, fixture string) *marketotest.Server {
	t.Helper()
	s := marketotest.New(t)
	s.Handle(typesPath, marketotest.Response{Body: fixture})
	return s
}

func TestTypesGolden(t *testing.T) {
	s := typesServer(t, "activity_types_story.json")
	out, errOut, code := fakeCLI(t, s, "types")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	golden(t, "types.txt", out)
	if s.Hits(typesPath) != 1 || s.Hits(marketotest.IdentityPath) != 1 {
		t.Errorf("types hits %d, identity hits %d", s.Hits(typesPath), s.Hits(marketotest.IdentityPath))
	}
	if m := s.Methods(); slices.ContainsFunc(m, func(x string) bool { return x != http.MethodGet }) {
		t.Errorf("unexpected method: %v", m)
	}
	out, _, _ = fakeCLI(t, typesServer(t, "activity_types_story.json"), "types", "--json")
	golden(t, "types.json", out)

	// activity_types.json lists 101 before 100 and has empty attribute lists.
	out, _, _ = fakeCLI(t, typesServer(t, "activity_types.json"), "types")
	if i, j := strings.Index(out, "\n100 "), strings.Index(out, "\n101 "); i < 0 || j < i {
		t.Errorf("not sorted by id:\n%s", out)
	}
	out, _, _ = fakeCLI(t, typesServer(t, "activity_types.json"), "types", "--json", "--search", "visit webpage")
	if !strings.Contains(out, `"attributes": []`) {
		t.Errorf("empty attributes not []:\n%s", out)
	}
}

func TestTypesSearch(t *testing.T) {
	out, _, code := fakeCLI(t, typesServer(t, "activity_types_story.json"), "types", "--search", "BOUNCED")
	if code != 0 || !strings.Contains(out, "Email Bounced Soft") || !strings.Contains(out, "\n8 ") || strings.Contains(out, "Open Email") {
		t.Errorf("name search: exit %d\n%s", code, out)
	}
	// An attribute name matches too: Device is on Open Email and Click Email only.
	out, _, _ = fakeCLI(t, typesServer(t, "activity_types_story.json"), "types", "--search", "device")
	if strings.Count(out, "\n") != 3 || !strings.Contains(out, "Open Email") || !strings.Contains(out, "Click Email") {
		t.Errorf("attribute search:\n%s", out)
	}
	out, _, code = fakeCLI(t, typesServer(t, "activity_types_story.json"), "types", "--search", "zzz")
	if code != 0 || out != "No activity types match \"zzz\".\n" {
		t.Errorf("no match: exit %d, %q", code, out)
	}
	out, _, _ = fakeCLI(t, typesServer(t, "activity_types_story.json"), "types", "--search", "zzz", "--json")
	if out != "[]\n" {
		t.Errorf("no match JSON = %q", out)
	}
}

func TestUnknownTypeSuggests(t *testing.T) {
	all := []marketo.ActivityType{{ID: 100, Name: "Change Score"}, {ID: 8, Name: "Email Bounced"}, {ID: 27, Name: "Email Bounced Soft"},
		{ID: 10, Name: "Open Email"}, {ID: 11, Name: "Click Email"}, {ID: 12, Name: "New Lead"}}
	for _, c := range []struct{ in, want string }{
		{"Chnage Score", "Change Score"},        // typo, distance 2
		{"change scor", "Change Score"},         // substring
		{"Bounced", "Email Bounced"},            // substring of two: shorter distance wins
		{"Email Bounced Hard", "Email Bounced"}, // Soft is distance 4; Email Bounced is a substring
		{"Opn Emial", "Open Email"},             // distance 3
		{"Nope", ""},
		{"", ""},
		{"e", ""},                     // too short for a substring match
		{"éé", ""},                    // 2 letters (4 bytes): still too short
		{"Lead", "New Lead"},          // 4 letters: substring counts
		{"Lick Email", "Click Email"}, // substring, distance 1
		{"Xpen Xmail", "Open Email"},  // distance 2; Click Email is 4
	} {
		if got := closestType(all, c.in); got != c.want {
			t.Errorf("closestType(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// Tie on distance: alphabetical.
	tie := []marketo.ActivityType{{ID: 2, Name: "Bbc"}, {ID: 1, Name: "Abc"}}
	if got := closestType(tie, "xbc"); got != "Abc" {
		t.Errorf("tie = %q, want Abc", got)
	}

	const want = `warning: unknown activity type "Chnage Score" skipped; did you mean "Change Score"? (check activity_types in `
	_, errOut, code := act(t, activityServer(t), "--types", "Open Email, Chnage Score")
	if code != 0 || !strings.Contains(errOut, want) || !strings.Contains(errOut, "config.toml or --types; see mkto types)\n") {
		t.Errorf("--types: exit %d, stderr %s", code, errOut)
	}
	_, errOut, code = actConf(t, activityServer(t), "activity_types = [\"Open Email\", \"Chnage Score\"]\n")
	if code != 0 || !strings.Contains(errOut, want) {
		t.Errorf("activity_types: exit %d, stderr %s", code, errOut)
	}
	_, errOut, code = fakeCLIFull(t, briefServer(t, "Contact Us Form"), fixedNow, "activity_types = [\"Fill Out Form\", \"Chnage Score\"]\n", "", nil,
		"brief", "jane.doe@example.com", "--show-payload")
	if code != 0 || !strings.Contains(errOut, want) || !strings.Contains(errOut, "config.toml; see mkto types)\n") {
		t.Errorf("brief: exit %d, stderr %s", code, errOut)
	}
}

func TestLevenshtein(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"", "", 0}, {"", "abc", 3}, {"abc", "", 3}, {"kitten", "sitting", 3},
		{"change score", "chnage score", 2}, {"flaw", "lawn", 2}, {"same", "same", 0}, {"é", "e", 1},
	} {
		if got := levenshtein(c.a, c.b); got != c.want {
			t.Errorf("levenshtein(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

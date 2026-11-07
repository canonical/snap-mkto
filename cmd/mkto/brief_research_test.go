// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/canonical/snap-mkto/internal/llmtest"
)

var (
	hitK8s   = llmtest.Result{URL: "https://news.example.com/example-corp-k8s", Title: "Example Corp moves to Kubernetes"}
	hitAbout = llmtest.Result{URL: "https://www.example.org/about", Title: "About Example Corp"}
)

// researchOK lists an unsearched source first, so the kept sources 2 and 3 are renumbered 1 and 2.
const researchOK = `{"summary":[{"text":"Example Corp is a German software company [3].","sources":[3]}],` +
	`"size":{"text":"It has about 250 employees.","sources":[3]},"industry":null,` +
	`"signals":[{"text":"It runs workloads on Kubernetes.","sources":[2]}],` +
	`"news":[{"text":"It announced a Kubernetes migration in September 2026.","sources":[2,3]},` +
	`{"text":"It raised a funding round.","sources":[1]}],` +
	`"sources":[{"url":"https://invented.example.net/funding","title":"not searched"},` +
	`{"url":"https://news.example.com/example-corp-k8s","title":"k8s"},{"url":"https://www.example.org/about","title":"about"}]}`

// research is a finished research turn: one search returning hitK8s and hitAbout, then the JSON.
func research(js string, hits ...llmtest.Result) llmtest.Response {
	if hits == nil {
		hits = []llmtest.Result{hitK8s, hitAbout}
	}
	return llmtest.Reply("end_turn", 300, 60, llmtest.SearchUse("srvtoolu_1", "Example Corp"),
		llmtest.SearchResults("srvtoolu_1", hits...), llmtest.Text("Found it.\n```json\n"+js+"\n```"))
}

func TestBriefResearchUnavailable(t *testing.T) {
	paused := llmtest.Reply("pause_turn", 1, 1, llmtest.SearchUse("srvtoolu_1", "q"), llmtest.SearchResults("srvtoolu_1", hitK8s))
	for _, c := range []struct {
		name  string
		reply llmtest.Response
		note  string
	}{
		{"did not finish", paused, "research did not finish"},
		{"tool error", llmtest.Reply("end_turn", 1, 1, llmtest.SearchUse("srvtoolu_1", "q"),
			llmtest.SearchError("srvtoolu_1", "max_uses_exceeded"), llmtest.Text("No luck.")), "max_uses_exceeded"},
		{"refused", llmtest.Refusal("cyber"), "the model declined"},
		{"api error", llmtest.Response{Status: 400, Body: `{"type":"error","error":{"type":"invalid_request_error","message":"no"}}`},
			"Anthropic API error 400: invalid_request_error"},
	} {
		for _, asJSON := range []bool{false, true} {
			ai := llmtest.New(t, narration(sPricing), llmtest.Message(`{"unsupported":[]}`, 1, 1), c.reply)
			var args []string
			if asJSON {
				args = append(args, "--json")
			}
			out, errOut, code := briefLLM(t, briefServer(t, "Contact Us Form"), ai, true, args...)
			if code != 0 {
				t.Fatalf("%s: exit %d: %s", c.name, code, errOut)
			}
			if !asJSON {
				if want := "## About Example Corp\n_Research unavailable: " + c.note + "._\n\n## How Canonical could help\n"; !strings.Contains(out, want) {
					t.Errorf("%s: stdout lacks %q:\n%s", c.name, want, out)
				}
				if strings.Contains(out, "## Sources") {
					t.Errorf("%s: no sources to list", c.name)
				}
				continue
			}
			var got struct {
				Research struct {
					Unavailable string
					Summary     []any
					Sources     []any
				}
			}
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatal(err)
			}
			if got.Research.Unavailable != c.note || got.Research.Summary == nil || got.Research.Sources == nil {
				t.Errorf("%s: --json research = %+v", c.name, got.Research)
			}
			if !strings.HasPrefix(errOut, "Research unavailable: "+c.note+".\nNo playbook.md in ") {
				t.Errorf("%s: stderr = %q", c.name, errOut)
			}
		}
	}
}

func TestBriefNoResearch(t *testing.T) {
	for _, flag := range []string{"", "--json"} {
		ai := llmtest.New(t, narration(sPricing), llmtest.Message(`{"unsupported":[]}`, 1, 1), research(researchOK))
		args := []string{"--no-research"}
		if flag != "" {
			args = append(args, flag)
		}
		out, errOut, code := briefLLM(t, briefServer(t, "Contact Us Form"), ai, true, args...)
		if code != 0 {
			t.Fatalf("exit %d: %s", code, errOut)
		}
		if n := len(ai.Requests()); n != 2 {
			t.Errorf("%q: %d LLM requests, want narrate + verify only", flag, n)
		}
		if strings.Contains(out, "About") || strings.Contains(out, "Sources") {
			t.Errorf("%q: stdout:\n%s", flag, out)
		}
		if flag != "" && !strings.Contains(out, `"research": null`) {
			t.Errorf("--json without research must say research: null:\n%s", out)
		}
	}
}

func TestBriefResearchSendsOnlyTheCompany(t *testing.T) {
	ai := llmtest.New(t, narration(sPricing), llmtest.Message(`{"unsupported":[]}`, 1, 1), research(researchOK))
	if _, errOut, code := briefLLM(t, briefServer(t, "Contact Us Form"), ai, true); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	reqs := ai.Requests()
	if len(reqs) != 3 {
		t.Fatalf("%d LLM requests, want 3", len(reqs))
	}
	var m struct {
		Messages []struct {
			Content []struct{ Text string }
		}
		Tools []map[string]any
	}
	if err := json.Unmarshal(reqs[2].Body, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Messages) != 1 || len(m.Messages[0].Content) != 1 {
		t.Fatalf("research request has %d messages, want one with one text block", len(m.Messages))
	}
	want := "Company:\n```json\n{\"company\":\"Example Corp\",\"industry\":\"Software\",\"country\":\"Germany\"}\n```"
	if got := m.Messages[0].Content[0].Text; got != want {
		t.Errorf("research user message:\n%s\nwant:\n%s", got, want)
	}
	if len(m.Tools) != 1 || m.Tools[0]["type"] != "web_search_20260209" || m.Tools[0]["name"] != "web_search" || m.Tools[0]["max_uses"] != 5.0 ||
		fmt.Sprint(m.Tools[0]["allowed_callers"]) != "[direct]" {
		t.Errorf("tools = %v", m.Tools)
	}
	for _, bad := range []string{"E1", "pricing", "Contact Us", "senior engineering", "numberOfEmployees", "250", briefKey} {
		if bytes.Contains(reqs[2].Body, []byte(bad)) {
			t.Errorf("research request holds %q", bad)
		}
	}
}

func TestBriefResearchCleansWebText(t *testing.T) {
	evil := llmtest.Result{URL: hitAbout.URL, Title: "About \x1b]52;c;cHduZWQ=\x07 Example Corp"}
	js := `{"summary":[{"text":"Example Corp \u001b]52;c;eA==\u0007 makes software.","sources":[1]}],"size":null,"industry":null,` +
		`"signals":[],"news":[],"sources":[{"url":"` + hitAbout.URL + `","title":"t"}]}`
	for _, flag := range []string{"", "--json"} {
		ai := llmtest.New(t, narration(sPricing), llmtest.Message(`{"unsupported":[]}`, 1, 1), research(js, evil))
		var args []string
		if flag != "" {
			args = append(args, flag)
		}
		out, errOut, code := briefLLM(t, briefServer(t, "Contact Us Form"), ai, true, args...)
		if code != 0 {
			t.Fatalf("exit %d: %s", code, errOut)
		}
		if strings.ContainsAny(out+errOut, "\x1b\x07") || strings.Contains(out, `\u001b`) || strings.Contains(out, `\u0007`) {
			t.Errorf("%q: web text reached the output with control characters:\n%s", flag, out)
		}
		if flag == "" && !strings.Contains(out, "## Sources\n1. <"+hitAbout.URL+"> — About \uFFFD\\]52;c;cHduZWQ=\uFFFD Example Corp\n") {
			t.Errorf("stdout:\n%s", out)
		}
	}
}

func TestUnavailableNote(t *testing.T) {
	for reason, want := range map[string]string{
		"max_uses_exceeded":               "_Research unavailable: max_uses_exceeded._",
		"the API did not answer in time.": "_Research unavailable: the API did not answer in time._",
	} {
		if got := unavailableNote(reason); got != want {
			t.Errorf("unavailableNote(%q) = %q, want %q", reason, got, want)
		}
	}
}

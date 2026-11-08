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

	"github.com/canonical/snap-mkto/internal/llmtest"
	"github.com/canonical/snap-mkto/internal/marketotest"
)

const briefLead = `{"success":true,"result":[{"id":1001,"updatedAt":"2026-10-01T12:00:00Z","firstName":"Jane","lastName":"Doe",
"email":"jane.doe@example.com","title":"VP of Platform Engineering","company":"Example Corp","industry":"Software",
"country":"Germany","numberOfEmployees":250,"mobilePhone__c":"+49 30 901820"}]}`

func briefActs(formAsset string) string {
	return `{"success":true,"moreResult":false,"result":[
{"id":1,"leadId":1001,"activityDate":"2026-11-01T10:20:00Z","activityTypeId":1,"primaryAttributeValue":"www.example.com/pricing?mkt_tok=abc123SECRET&utm_source=x",
 "attributes":[{"name":"Client IP Address","value":"192.0.2.10"}]},
{"id":2,"leadId":1001,"activityDate":"2026-11-02T11:30:00Z","activityTypeId":2,"primaryAttributeValue":"` + formAsset + `","attributes":[]},
{"id":3,"leadId":1001,"activityDate":"2026-11-03T08:00:00Z","activityTypeId":46,"primaryAttributeValue":"Called +1 555 010 0199","attributes":[]}]}`
}

func briefServer(t *testing.T, formAsset string) *marketotest.Server {
	t.Helper()
	s := marketotest.New(t)
	s.Handle(leadsPath, marketotest.Response{Body: briefLead})
	s.Handle(typesPath, marketotest.Response{Body: "activity_types.json"})
	s.Handle(tokenPath, marketotest.Response{Body: "pagingtoken_ok.json"})
	s.Handle(actsPath, marketotest.Response{Body: briefActs(formAsset)})
	return s
}

func brief(t *testing.T, s *marketotest.Server, llm string, args ...string) (string, string, int) {
	t.Helper()
	return fakeCLIFull(t, s, fixedNow, "", llm, map[string]string{"MKTO_LLM_API_KEY": "sk-ant-TOPSECRET"},
		append([]string{"brief", "jane.doe@example.com"}, args...)...)
}

// briefStatusField is the [why] section that lets the Lead Status rows of briefStoryServer through.
const briefStatusField = "[why]\nstatus_field = \"Lead Status\"\n"

// briefStoryServer is briefServer plus a score change, two data value changes (one the
// status field), a program status change and an Email Delivered row (T37).
func briefStoryServer(t *testing.T) *marketotest.Server {
	t.Helper()
	s := briefServer(t, "Contact Us Form")
	s.Handle(actsPath, marketotest.Response{Body: strings.TrimSuffix(briefActs("Contact Us Form"), "]}") + `,
{"id":4,"leadId":1001,"activityDate":"2026-11-03T09:00:00Z","activityTypeId":7,"primaryAttributeValue":"November Newsletter","attributes":[]},
{"id":5,"leadId":1001,"activityDate":"2026-11-02T11:31:00Z","activityTypeId":100,"primaryAttributeValue":"Behavior Score","attributes":[
 {"name":"Change Value","value":"+25"},{"name":"Old Value","value":"60"},{"name":"New Value","value":"85"},{"name":"Reason","value":"Filled out form"}]},
{"id":6,"leadId":1001,"activityDate":"2026-11-02T11:32:00Z","activityTypeId":13,"primaryAttributeValue":"Lead Status","attributes":[
 {"name":"Old Value","value":"Nurture"},{"name":"New Value","value":"MQL"},{"name":"Reason","value":"Changed by smart campaign Score Threshold"}]},
{"id":7,"leadId":1001,"activityDate":"2026-11-02T11:33:00Z","activityTypeId":13,"primaryAttributeValue":"Mobile Phone","attributes":[
 {"name":"Old Value","value":""},{"name":"New Value","value":"+49 30 901820"}]},
{"id":8,"leadId":1001,"activityDate":"2026-11-04T15:00:00Z","activityTypeId":22,"primaryAttributeValue":"Webinar Q4","attributes":[
 {"name":"Old Status","value":"Registered"},{"name":"New Status","value":"Attended"},{"name":"Success","value":true},{"name":"Reason","value":"Checked in"}]}]}`})
	return s
}

func TestShowPayloadGolden(t *testing.T) {
	s := briefStoryServer(t)
	out, errOut, code := brief(t, s, briefStatusField, "--show-payload") // [llm] absent: enabled=false; the fake ignores activityTypeIds; --web would only add 2 types to the batches
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	golden(t, "brief_show_payload.txt", out)
	for _, bad := range []string{"jane", "Jane", "example.com/in", "+49", "192.0.2.10", "mkt_tok", "abc123", "555", "TOPSECRET",
		"Filled out", "Score Threshold", "Checked in", "Registered", "Mobile Phone", "Email Delivered", "November Newsletter"} {
		if strings.Contains(out+errOut, bad) {
			t.Errorf("output leaks %q", bad)
		}
	}
	for _, m := range s.Methods() {
		if m != http.MethodGet { // Marketo is GET-only; --show-payload calls no LLM
			t.Errorf("unexpected method %s", m)
		}
	}
	if got := s.Query(tokenPath).Get("sinceDatetime"); got != "2026-08-09T12:00:00Z" {
		t.Errorf("default window sinceDatetime = %q", got)
	}
}

func TestBriefShowPayloadNoResearch(t *testing.T) {
	out, errOut, code := brief(t, briefStoryServer(t), briefStatusField, "--show-payload", "--no-research")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	full, err := os.ReadFile(filepath.Join("..", "..", "testdata", "golden", "brief_show_payload.txt"))
	if err != nil {
		t.Fatal(err)
	}
	want, _, _ := strings.Cut(string(full), "\n\n# Research query")
	if strings.Contains(out, "Research query") || out != want+"\n" {
		t.Errorf("stdout:\n%s", out)
	}
}

func TestBriefShowPayloadRedacts(t *testing.T) {
	s := briefServer(t, "Jane Contact Form")
	out, errOut, code := brief(t, s, "[llm]\nenabled = true\n", "--show-payload")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.HasPrefix(out, "# Redacted before sending: lead's first name in events[1].asset\n# Narrate payload") ||
		!strings.Contains(out, `"asset": "{redacted} Contact Form"`) {
		t.Errorf("stdout:\n%s", out)
	}
	if strings.Contains(out+errOut, "Jane") {
		t.Error("output leaks the name")
	}
}

// TestBriefShowPayloadURLRedaction: a URL segment ScrubURL replaced is listed, not "nothing".
func TestBriefShowPayloadURLRedaction(t *testing.T) {
	s := marketotest.New(t)
	s.Handle(leadsPath, marketotest.Response{Body: briefLead})
	s.Handle(typesPath, marketotest.Response{Body: "activity_types.json"})
	s.Handle(tokenPath, marketotest.Response{Body: "pagingtoken_ok.json"})
	s.Handle(actsPath, marketotest.Response{Body: `{"success":true,"moreResult":false,"result":[
{"id":1,"leadId":1001,"activityDate":"2026-11-01T10:20:00Z","activityTypeId":1,"primaryAttributeValue":"www.example.com/u/bob.smith%2540example.org","attributes":[]}]}`})
	out, errOut, code := brief(t, s, "", "--show-payload")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.HasPrefix(out, "# Redacted before sending: email or token in URL in events[0].url\n") ||
		!strings.Contains(out, `"url": "https://www.example.com/u/{redacted}"`) || strings.Contains(out, "bob.smith") {
		t.Errorf("stdout:\n%s", out)
	}
}

func TestBriefOnlyWebTypes(t *testing.T) {
	s := briefServer(t, "Contact Us Form")
	out, errOut, code := fakeCLIFull(t, s, fixedNow, "activity_types = [\"Visit Webpage\", \"Click Link\"]\n", "", nil,
		"brief", "jane.doe@example.com", "--show-payload")
	if code != 1 || out != "" || errOut != "no activity types left to fetch: activity_types only has web types; add --web to include them\n" {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	if s.Hits(actsPath) != 0 {
		t.Error("fetched activities")
	}
}

// TestBriefVerboseListsRedactions: a normal run sends the redacted payload and, with --verbose, lists what was replaced.
func TestBriefVerboseListsRedactions(t *testing.T) {
	for _, verbose := range []bool{false, true} {
		s := briefServer(t, "Jane Contact Form")
		ai := llmtest.New(t, narration(sPricing), llmtest.Message(`{"unsupported":[]}`, 1, 1))
		args := []string{"--no-research"}
		if verbose {
			args = append(args, "--verbose")
		}
		out, errOut, code := briefLLM(t, s, ai, true, args...)
		if code != 0 {
			t.Fatalf("exit %d: %s", code, errOut)
		}
		line := "Redacted before sending: lead's first name in events[1].asset\n"
		if strings.Contains(errOut, line) != verbose {
			t.Errorf("verbose=%t stderr:\n%s", verbose, errOut)
		}
		for i, r := range ai.Requests() {
			if bytes.Contains(r.Body, []byte("Jane")) {
				t.Errorf("request %d holds the name", i)
			}
		}
		if strings.Contains(out+errOut, "Jane") {
			t.Error("output leaks the name")
		}
	}
}

const webNote = "note: skipping web visits and link clicks (slow on large Marketo instances); add --web to include them\n"

// TestBriefWebTypes: brief skips Visit Webpage and Click Link unless --web is given.
func TestBriefWebTypes(t *testing.T) {
	for _, c := range []struct {
		name, extra string
		args        []string
		web, note   bool
	}{
		{"default has no web types", "", nil, false, false},
		{"--web adds them to a list without", "activity_types = [\"Fill Out Form\", \"Open Email\"]\n", []string{"--web"}, true, false},
		{"user list skips web types", webTypes, nil, false, true},
		{"--web includes them", webTypes, []string{"--web"}, true, false},
		{"no note without web types", "activity_types = [\"Fill Out Form\", \"Open Email\"]\n", nil, false, false},
		{"case-insensitive", "activity_types = [\" visit WEBPAGE \", \"click link\", \"Open Email\"]\n", nil, false, true},
	} {
		s := briefServer(t, "Contact Us Form")
		_, errOut, code := fakeCLIFull(t, s, fixedNow, c.extra, "", nil,
			append([]string{"brief", "jane.doe@example.com", "--show-payload"}, c.args...)...)
		if code != 0 {
			t.Fatalf("%s: exit %d: %s", c.name, code, errOut)
		}
		ids := strings.Split(s.Query(actsPath).Get("activityTypeIds"), ",")
		if slices.Contains(ids, "1") != c.web || slices.Contains(ids, "3") != c.web {
			t.Errorf("%s: activityTypeIds = %v", c.name, ids)
		}
		if n := strings.Count(errOut, webNote); n != map[bool]int{true: 1, false: 0}[c.note] {
			t.Errorf("%s: note printed %d times; stderr:\n%s", c.name, n, errOut)
		}
	}
}

func TestBriefOffInlineSteps(t *testing.T) {
	for _, c := range []struct {
		llm  string
		code int
		msg  string
	}{
		{"[llm]\nenabled = false\n", 1, "mkto brief is off. To turn it on, set [llm] enabled = true and api_key (or MKTO_LLM_API_KEY) in "},
	} {
		s := briefServer(t, "x")
		out, errOut, code := brief(t, s, c.llm)
		if code != c.code || out != "" || !strings.HasPrefix(errOut, c.msg) {
			t.Errorf("%q: exit %d, stdout %q, stderr %q", c.llm, code, out, errOut)
		}
		if want := filepath.Join("config.toml") + ". Preview what would be sent first: mkto brief <email> --show-payload." +
			" Data-protection sign-off: https://github.com/canonical/snap-mkto/blob/main/docs/LLM_LAYER.md\n"; !strings.HasSuffix(errOut, want) {
			t.Errorf("stderr = %q, want suffix %q", errOut, want)
		}
		if strings.Contains(errOut, "jane") {
			t.Error("lead email echoed")
		}
		if s.Hits(leadsPath)+s.Hits(typesPath)+s.Hits(actsPath) != 0 || len(s.Methods()) != 0 {
			t.Errorf("gate made Marketo calls: %v", s.Methods())
		}
		if strings.Contains(errOut, "TOPSECRET") {
			t.Error("api key leaked")
		}
	}
}

func TestBriefDaysValidated(t *testing.T) {
	if _, _, code := brief(t, briefServer(t, "x"), "", "--show-payload", "--days", "0"); code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
}

const briefKey = "sk-ant-TOPSECRET"

// briefLLM runs `mkto brief` with [llm] enabled and the LLM pointed at ai.
// keyEnv false leaves MKTO_LLM_API_KEY unset.
func briefLLM(t *testing.T, s *marketotest.Server, ai *llmtest.Server, keyEnv bool, args ...string) (string, string, int) {
	t.Helper()
	return briefLLMConfig(t, s, ai, keyEnv, "model = \"test-model\"\n", args...)
}

// briefLLMConfig is briefLLM with extra lines in the [llm] section.
func briefLLMConfig(t *testing.T, s *marketotest.Server, ai *llmtest.Server, keyEnv bool, llmExtra string, args ...string) (string, string, int) {
	t.Helper()
	return briefLLMIn(t, t.TempDir(), s, ai, keyEnv, llmExtra, args...)
}

// briefLLMIn is briefLLMConfig with the config file in dir.
func briefLLMIn(t *testing.T, dir string, s *marketotest.Server, ai *llmtest.Server, keyEnv bool, llmExtra string, args ...string) (string, string, int) {
	t.Helper()
	cfg := "[marketo]\nbase_url = \"https://x.mktorest.com\"\nclient_id = \"id\"\nclient_secret = \"s3cret\"\n" +
		"[llm]\nenabled = true\n" + llmExtra
	p := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(p, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{}
	if keyEnv {
		env["MKTO_LLM_API_KEY"] = briefKey
	}
	var out, errb bytes.Buffer
	e := Env{
		Stdout: &out, Stderr: &errb,
		Getenv: func(k string) string { return env[k] },
		HTTP:   &http.Client{Transport: toFake{s.URL}},
		Now:    fixedNow,
		Sleep:  func(context.Context, time.Duration) error { return nil },
	}
	if ai != nil {
		e.LLMBaseURL = ai.URL
	}
	// No --web: the fake returns briefActs (with its Visit Webpage row) whatever types are asked.
	code := run(append([]string{"--config", p, "brief", "jane.doe@example.com"}, args...), e)
	for _, leak := range []string{briefKey, "TOPSECRET", "s3cret"} {
		if strings.Contains(out.String()+errb.String(), leak) {
			t.Errorf("output contains a secret (%d chars)", len(leak))
		}
	}
	return out.String(), errb.String(), code
}

func narration(sentences ...string) llmtest.Response {
	return llmtest.Message(`{"sentences":[`+strings.Join(sentences, ",")+`]}`, 120, 45)
}

const (
	sPricing = `{"text":"The lead visited the pricing page [E1].","cites":["E1"]}`
	sForm    = `{"text":"A day later the lead filled out the contact form [E2], which may indicate buying interest.","cites":["E2"]}`
)

func TestBriefGolden(t *testing.T) {
	for _, c := range []struct{ golden, flag string }{{"brief.md", ""}, {"brief.json", "--json"}} {
		s := briefServer(t, "Contact Us Form")
		ai := llmtest.New(t, narration(sPricing, sForm), llmtest.Message(`{"unsupported":[]}`, 200, 8), research(researchOK), llmtest.Pitch(pitchOK))
		var args []string
		if c.flag != "" {
			args = append(args, c.flag)
		}
		out, errOut, code := briefLLMIn(t, playbookDir(t, llmtest.Playbook), s, ai, true, "model = \"test-model\"\n", args...)
		if code != 0 || errOut != "" {
			t.Fatalf("%s: exit %d, stderr %q", c.golden, code, errOut)
		}
		golden(t, c.golden, out)
		if n := len(ai.Requests()); n != 4 {
			t.Errorf("%s: %d LLM requests, want narrate + verify + research + pitch", c.golden, n)
		}
		if c.flag == "--json" && !json.Valid([]byte(out)) {
			t.Error("--json output is not JSON")
		}
	}
}

// briefPayload is the narrate payload briefServer's lead and activities redact to.
const briefPayload = `{"lead":{"company":"Example Corp","industry":"Software","country":"Germany","numberOfEmployees":250,` +
	`"role":"senior engineering leader"},"events":[` +
	`{"id":"E1","date":"2026-11-01T10:00:00Z","type":"Visit Webpage","url":"https://www.example.com/pricing"},` +
	`{"id":"E2","date":"2026-11-02T11:00:00Z","type":"Fill Out Form","asset":"Contact Us Form"},` +
	`{"id":"E3","date":"2026-11-03T08:00:00Z","type":"Interesting Moment"}]}`

func TestBriefSendsOnlyThePayload(t *testing.T) {
	s := briefServer(t, "Contact Us Form")
	ai := llmtest.New(t, narration(sPricing), llmtest.Message(`{"unsupported":[]}`, 1, 1))
	if _, errOut, code := briefLLM(t, s, ai, true); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	reqs := ai.Requests()
	if len(reqs) == 0 {
		t.Fatal("no LLM request")
	}
	var m struct {
		Messages []struct {
			Content []struct{ Text string }
		}
	}
	if err := json.Unmarshal(reqs[0].Body, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Messages) != 1 || len(m.Messages[0].Content) != 1 {
		t.Fatalf("narrate request has %d messages, want one with one text block", len(m.Messages))
	}
	if got, want := m.Messages[0].Content[0].Text, "Payload:\n```json\n"+briefPayload+"\n```"; got != want {
		t.Errorf("narrate user message:\n%s\nwant:\n%s", got, want)
	}
	for i, r := range reqs {
		for _, bad := range []string{"jane", "Jane", "Doe", "901820", "+49", "192.0.2.10", "mkt_tok", "abc123", "555", "s3cret", "TOPSECRET", "1001"} {
			if bytes.Contains(r.Body, []byte(bad)) {
				t.Errorf("request %d body holds lead data %q", i, bad)
			}
		}
	}
}

func TestBriefRemovesClaims(t *testing.T) {
	s := briefServer(t, "Contact Us Form")
	ai := llmtest.New(t, narration(sPricing,
		`{"text":"The lead called sales.","cites":[]}`,
		`{"text":"The lead read the blog [E99].","cites":["E99"]}`,
		`{"text":"The lead is ready to buy [E2].","cites":["E2"]}`,
	), llmtest.Message(`{"unsupported":[1]}`, 200, 8))
	out, errOut, code := briefLLM(t, s, ai, true, "--verbose", "--no-research")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(out, "## What happened\nThe lead visited the pricing page [E1].\n\n## How Canonical could help\n_No playbook.md in ") {
		t.Errorf("stdout:\n%s", out)
	}
	for _, want := range []string{
		"Removed claims:\n- The lead called sales. (no valid citation)\n- The lead read the blog [E99]. (no valid citation)\n- The lead is ready to buy [E2]. (unsupported)\n",
		"llm narrate test-model 120/45 tokens ", "llm verify test-model 200/8 tokens ", "llm total 320/53 tokens\n",
	} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr lacks %q:\n%s", want, errOut)
		}
	}
	reqs := ai.Requests()
	if len(reqs) != 2 || bytes.Contains(reqs[1].Body, []byte("E99")) || bytes.Contains(reqs[1].Body, []byte("called sales")) {
		t.Error("verify must see only the sentences that passed the citation check")
	}
	if strings.Contains(out, "E99") || strings.Contains(out, "ready to buy") {
		t.Error("removed claims reached stdout")
	}
}

func TestBriefJSONRemoved(t *testing.T) {
	s := briefServer(t, "Contact Us Form")
	ai := llmtest.New(t, narration(`{"text":"The lead read the blog [E99].","cites":["E99"]}`))
	out, errOut, code := briefLLM(t, s, ai, true, "--json", "--no-research")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if len(ai.Requests()) != 1 {
		t.Error("verify must be skipped when no sentence is left")
	}
	var got struct {
		Sentences []any
		Removed   []struct{ Text, Reason string }
		Usage     map[string]struct{ In, Out int }
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.Sentences == nil || len(got.Sentences) != 0 || len(got.Removed) != 1 || got.Removed[0].Reason != "no valid citation" {
		t.Errorf("json = %s", out)
	}
	if got.Usage["verify"].In != 0 || got.Usage["total"].In != 120 {
		t.Errorf("usage = %+v", got.Usage)
	}
	if !strings.Contains(out, `"sentences": []`) {
		t.Errorf("sentences must be [], not null:\n%s", out)
	}
}

func TestBriefNarrateRefused(t *testing.T) {
	s := briefServer(t, "Contact Us Form")
	ai := llmtest.New(t, llmtest.Refusal("cyber"))
	out, errOut, code := briefLLM(t, s, ai, true, "--no-research")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(out, "## What happened\n_The model declined to write this section._\n\n## How Canonical could help\n") {
		t.Errorf("stdout:\n%s", out)
	}
	if len(ai.Requests()) != 1 {
		t.Error("verify must not run after a narrate refusal")
	}
}

func TestBriefVerifyRefused(t *testing.T) {
	s := briefServer(t, "Contact Us Form")
	ai := llmtest.New(t, narration(sPricing), llmtest.Refusal("cyber"))
	out, errOut, code := briefLLM(t, s, ai, true, "--no-research")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(out, "## What happened\nThe lead visited the pricing page [E1].\n\n_Not verified: the model declined the check._\n\n## How Canonical could help\n") {
		t.Errorf("stdout:\n%s", out)
	}
}

func TestBriefLLMError(t *testing.T) {
	s := briefServer(t, "Contact Us Form")
	ai := llmtest.New(t, llmtest.Response{Status: 401, Body: `{"type":"error","error":{"type":"authentication_error","message":"bad key ` + briefKey + `"}}`})
	out, errOut, code := briefLLM(t, s, ai, true)
	if code != 1 || out != "" || errOut != "Anthropic API error 401: authentication_error\n" {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
}

func TestBriefMissingKey(t *testing.T) {
	s := briefServer(t, "Contact Us Form")
	out, errOut, code := briefLLM(t, s, nil, false)
	if code != 1 || out != "" || !strings.HasPrefix(errOut, "llm.api_key is not set (or set MKTO_LLM_API_KEY) in ") ||
		!strings.HasSuffix(errOut, "config.toml\n") {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	if len(s.Methods()) != 0 {
		t.Errorf("missing key still called Marketo: %v", s.Methods())
	}
}

func TestBriefCleansModelText(t *testing.T) {
	s := briefServer(t, "Contact Us Form")
	ai := llmtest.New(t, narration(`{"text":"The lead visited \u001b]52;c;cHduZWQ=\u0007 the pricing page [E1].","cites":["E1"]}`),
		llmtest.Message(`{"unsupported":[]}`, 1, 1))
	out, errOut, code := briefLLM(t, s, ai, true, "--verbose")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if strings.ContainsAny(out+errOut, "\x1b\x07") {
		t.Error("model text reached the terminal with control characters")
	}
	if !strings.Contains(out, "The lead visited \uFFFD\\]52;c;cHduZWQ=\uFFFD the pricing page [E1].") {
		t.Errorf("stdout:\n%s", out)
	}
}

func briefJSON(t *testing.T, out string) (declined string, verified bool) {
	t.Helper()
	var got struct {
		Declined *string
		Verified *bool
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.Declined == nil || got.Verified == nil {
		t.Fatalf("--json lacks declined or verified:\n%s", out)
	}
	return *got.Declined, *got.Verified
}

func TestBriefJSONRefusalState(t *testing.T) {
	for _, c := range []struct {
		name     string
		replies  []llmtest.Response
		declined string
		verified bool
	}{
		{"ok", []llmtest.Response{narration(sPricing), llmtest.Message(`{"unsupported":[]}`, 1, 1)}, "", true},
		{"nothing to verify", []llmtest.Response{narration(`{"text":"x [E99].","cites":["E99"]}`)}, "", true},
		{"narrate", []llmtest.Response{llmtest.Refusal("cyber")}, "narrate", false},
		{"verify", []llmtest.Response{narration(sPricing), llmtest.Refusal("cyber")}, "verify", false},
	} {
		out, errOut, code := briefLLM(t, briefServer(t, "Contact Us Form"), llmtest.New(t, c.replies...), true, "--json")
		if code != 0 {
			t.Fatalf("%s: exit %d: %s", c.name, code, errOut)
		}
		if d, v := briefJSON(t, out); d != c.declined || v != c.verified {
			t.Errorf("%s: declined %q verified %v, want %q %v", c.name, d, v, c.declined, c.verified)
		}
	}
}

func TestBriefUnknownCompanyNoSentences(t *testing.T) {
	s := briefServer(t, "Contact Us Form")
	s.Handle(leadsPath, marketotest.Response{Body: strings.Replace(briefLead, `"company":"Example Corp",`, "", 1)})
	ai := llmtest.New(t, narration(`{"text":"The lead read the blog [E99].","cites":["E99"]}`))
	out, errOut, code := briefLLM(t, s, ai, true)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.HasPrefix(out, "# Lead brief · unknown company · last 90 days\n\n## What happened\n_No sentence passed the citation checks._\n\n"+
		"## About unknown company\n_No company name to research._\n\n## How Canonical could help\n_No playbook.md in ") {
		t.Errorf("stdout:\n%s", out)
	}
	if n := len(ai.Requests()); n != 1 {
		t.Errorf("%d LLM requests, want narrate only: no company means no research", n)
	}
}

func TestBriefStrayCites(t *testing.T) {
	s := briefServer(t, "Contact Us Form")
	ai := llmtest.New(t, narration(sPricing,
		`{"text":"The lead saw the form [E3 ].","cites":["E2"]}`,
		`{"text":"The lead saw the form (E3).","cites":["E2"]}`,
		`{"text":"The lead saw the form in E3.","cites":["E2"]}`,
	), llmtest.Message(`{"unsupported":[]}`, 1, 1))
	out, errOut, code := briefLLM(t, s, ai, true, "--verbose")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if strings.Contains(out, "saw the form") {
		t.Errorf("a sentence with an unchecked id reached stdout:\n%s", out)
	}
	if n := strings.Count(errOut, "(no valid citation)"); n != 3 {
		t.Errorf("%d sentences removed for no valid citation, want 3:\n%s", n, errOut)
	}
}

func TestBriefProviderAndModelChecked(t *testing.T) {
	for _, c := range []struct{ extra, msg string }{
		{"provider = \"openai\"\nmodel = \"m\"\n", `llm.provider "openai" is not supported (only "anthropic") in `},
		{"model = \" \"\n", "llm.model is not set: set it in the [llm] section of "},
	} {
		s := briefServer(t, "Contact Us Form")
		out, errOut, code := briefLLMConfig(t, s, nil, true, c.extra)
		if code != 1 || out != "" || !strings.HasPrefix(errOut, c.msg) || !strings.HasSuffix(errOut, "config.toml\n") {
			t.Errorf("%q: exit %d, stdout %q, stderr %q", c.extra, code, out, errOut)
		}
		if len(s.Methods()) != 0 {
			t.Errorf("%q: still called Marketo", c.extra)
		}
	}
}

func TestBriefDefaultModel(t *testing.T) {
	ai := llmtest.New(t, narration(sPricing), llmtest.Message(`{"unsupported":[]}`, 1, 1))
	if _, errOut, code := briefLLMConfig(t, briefServer(t, "Contact Us Form"), ai, true, ""); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !bytes.Contains(ai.Requests()[0].Body, []byte(`"model":"claude-sonnet-5-5"`)) {
		t.Error("[llm] without model must use the template default")
	}
}

func TestBriefSkipsMissingExtraField(t *testing.T) {
	for _, verbose := range []bool{false, true} {
		s := briefServer(t, "Contact Us Form")
		s.Handle(leadsPath, marketotest.Response{Body: `{"success":false,"errors":[{"code":"1006","message":"Field 'numberOfEmployees' not found"}]}`},
			marketotest.Response{Body: strings.Replace(briefLead, `"numberOfEmployees":250,`, "", 1)})
		args := []string{"--show-payload"}
		if verbose {
			args = append(args, "--verbose")
		}
		out, errOut, code := brief(t, s, "", args...)
		if code != 0 {
			t.Fatalf("exit %d: %s", code, errOut)
		}
		if got := s.Hits(leadsPath); got != 2 {
			t.Errorf("leads hits = %d, want 2 (one retry)", got)
		}
		if f := strings.Split(s.Query(leadsPath).Get("fields"), ","); slices.Contains(f, "numberOfEmployees") || !slices.Contains(f, "industry") {
			t.Errorf("retry fields = %v", f)
		}
		if strings.Contains(out, "numberOfEmployees") || !strings.Contains(out, `"company": "Example Corp"`) {
			t.Errorf("payload:\n%s", out)
		}
		note := "note: field \"numberOfEmployees\" isn't on this Marketo instance; skipped\n"
		if strings.Contains(errOut, note) != verbose {
			t.Errorf("verbose=%t stderr: %s", verbose, errOut)
		}
	}
}

func TestBriefLeadFieldsMissingStillErrors(t *testing.T) {
	s := briefServer(t, "Contact Us Form")
	s.Handle(leadsPath, marketotest.Response{Body: `{"success":false,"errors":[{"code":"1006","message":"Field 'createdAt' not found"}]}`})
	_, errOut, code := brief(t, s, "", "--show-payload")
	if code != 1 || !strings.Contains(errOut, `field "createdAt" not found: fix lead_fields in `) {
		t.Errorf("exit %d, stderr %s", code, errOut)
	}
	if got := s.Hits(leadsPath); got != 1 {
		t.Errorf("leads hits = %d, want 1", got)
	}
}

// TestBriefIgnoresProfileFields: brief's fetch never grows with profile_fields (the redact allowlist is fixed).
func TestBriefIgnoresProfileFields(t *testing.T) {
	s := briefServer(t, "Contact Us")
	if _, errOut, code := brief(t, s, "", "--show-payload"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	f := strings.Split(s.Query(leadsPath).Get("fields"), ",")
	for _, k := range []string{"leadScore", "leadStatus", "leadOwnerEmailAddress", "sfdcLeadOwnerId", "unsubscribed", "emailInvalid", "sfdcLeadId", "sfdcContactId", "sfdcAccountId"} {
		if slices.Contains(f, k) {
			t.Errorf("brief requested %s: %v", k, f)
		}
	}
}

func TestBriefOneLeadLookup(t *testing.T) {
	s := briefServer(t, "Contact Us Form")
	if _, errOut, code := brief(t, s, "", "--show-payload", "--no-research"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if n := s.Hits(leadsPath); n != 1 {
		t.Errorf("leads.json hits = %d, want 1", n)
	}
}

// TestBriefEventsShowChange: the Events table shows what the model saw for a change.
func TestBriefEventsShowChange(t *testing.T) {
	ai := llmtest.New(t, narration(sPricing), llmtest.Message(`{"unsupported":[]}`, 1, 1))
	out, errOut, code := briefLLMConfig(t, briefStoryServer(t), ai, true, "model = \"test-model\"\n"+briefStatusField, "--no-research")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	for _, want := range []string{"Change Score                  Behavior Score +25 (60→85)\n", "Lead Status Nurture→MQL\n", "Webinar Q4 → Attended\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
}

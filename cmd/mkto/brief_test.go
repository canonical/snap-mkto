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

func TestBriefShowPayloadGolden(t *testing.T) {
	s := briefServer(t, "Contact Us Form")
	out, errOut, code := brief(t, s, "", "--show-payload") // [llm] absent: enabled=false
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	golden(t, "brief_show_payload.txt", out)
	for _, bad := range []string{"jane", "Jane", "example.com/in", "+49", "192.0.2.10", "mkt_tok", "abc123", "555", "TOPSECRET"} {
		if strings.Contains(out+errOut, bad) {
			t.Errorf("output leaks %q", bad)
		}
	}
	for _, m := range s.Methods() {
		if m != http.MethodGet && m != http.MethodPost {
			t.Errorf("unexpected method %s", m)
		}
	}
	if got := s.Query(tokenPath).Get("sinceDatetime"); got != "2026-08-09T12:00:00Z" {
		t.Errorf("default window sinceDatetime = %q", got)
	}
}

func TestBriefShowPayloadNoResearch(t *testing.T) {
	out, errOut, code := brief(t, briefServer(t, "Contact Us Form"), "", "--show-payload", "--no-research")
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

func TestBriefShowPayloadGuardAbort(t *testing.T) {
	s := briefServer(t, "Jane Contact Form")
	out, errOut, code := brief(t, s, "[llm]\nenabled = true\n", "--show-payload")
	if code != 1 || out != "" {
		t.Fatalf("exit %d, stdout %q", code, out)
	}
	if !strings.Contains(errOut, "events[1].asset") || strings.Contains(errOut, "Jane") {
		t.Errorf("stderr = %q", errOut)
	}
}

func TestBriefGates(t *testing.T) {
	for _, c := range []struct {
		llm  string
		code int
		msg  string
	}{
		{"[llm]\nenabled = false\n", 1, "mkto brief is off. Read the sign-off section in docs/LLM_LAYER.md, then set [llm] enabled = true in "},
	} {
		s := briefServer(t, "x")
		out, errOut, code := brief(t, s, c.llm)
		if code != c.code || out != "" || !strings.HasPrefix(errOut, c.msg) {
			t.Errorf("%q: exit %d, stdout %q, stderr %q", c.llm, code, out, errOut)
		}
		if strings.Contains(c.msg, "is off") && !strings.HasSuffix(errOut, filepath.Join("config.toml")+".\n") {
			t.Errorf("stderr = %q", errOut)
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

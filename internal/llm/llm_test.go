// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package llm_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/canonical/snap-mkto/internal/llm"
	"github.com/canonical/snap-mkto/internal/llmtest"
)

const (
	key   = "sk-ant-test-KEY-0123456789abcdef"
	model = "test-model-x"
)

var narrative = llm.Narrative{Sentences: []llm.Sentence{
	{Text: "The lead opened the October newsletter [E1].", Cites: []string{"E1"}},
	{Text: "The lead then filled out the contact form [E3], which may indicate interest.", Cites: []string{"E2", "E3"}},
}}

const narrativeJSON = `{"sentences":[{"text":"The lead opened the October newsletter [E1].","cites":["E1"]},` +
	`{"text":"The lead then filled out the contact form [E3], which may indicate interest.","cites":["E2","E3"]}]}`

// provider returns a provider pointed at srv. Its log is checked for the key when the test ends.
func provider(t *testing.T, srv *llmtest.Server, promptDir string) (llm.Provider, *bytes.Buffer) {
	t.Helper()
	var log bytes.Buffer
	p, err := llm.NewAnthropic(llm.Options{APIKey: key, Model: model, BaseURL: srv.URL, PromptDir: promptDir, Log: &log})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if strings.Contains(log.String(), key) {
			t.Error("log contains the API key")
		}
	})
	return p, &log
}

// noKey fails the test if err mentions the API key.
func noKey(t *testing.T, err error) {
	t.Helper()
	if err != nil && strings.Contains(err.Error(), key) {
		t.Error("error contains the API key")
	}
}

// body decodes the only request srv received.
func body(t *testing.T, srv *llmtest.Server) map[string]any {
	t.Helper()
	reqs := srv.Requests()
	if len(reqs) != 1 {
		t.Fatalf("got %d requests, want 1", len(reqs))
	}
	var m map[string]any
	if err := json.Unmarshal(reqs[0].Body, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func userText(t *testing.T, m map[string]any) string {
	t.Helper()
	msgs, _ := m["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages = %v, want one user message", m["messages"])
	}
	msg := msgs[0].(map[string]any)
	if msg["role"] != "user" {
		t.Fatalf("role = %v", msg["role"])
	}
	var b strings.Builder
	for _, c := range msg["content"].([]any) {
		b.WriteString(c.(map[string]any)["text"].(string))
	}
	return b.String()
}

func systemText(m map[string]any) string {
	var b strings.Builder
	sys, _ := m["system"].([]any)
	for _, s := range sys {
		b.WriteString(s.(map[string]any)["text"].(string) + "\n")
	}
	return b.String()
}

func schema(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	oc, _ := m["output_config"].(map[string]any)
	f, _ := oc["format"].(map[string]any)
	if f["type"] != "json_schema" {
		t.Fatalf("output_config.format = %v, want a json_schema", oc["format"])
	}
	s, _ := f["schema"].(map[string]any)
	return s
}

func checkCommon(t *testing.T, m map[string]any, maxTokens float64) {
	t.Helper()
	if m["model"] != model {
		t.Errorf("model = %v, want %q", m["model"], model)
	}
	if m["max_tokens"] != maxTokens {
		t.Errorf("max_tokens = %v, want %v", m["max_tokens"], maxTokens)
	}
	for _, k := range []string{"temperature", "top_p", "top_k", "thinking"} {
		if _, ok := m[k]; ok {
			t.Errorf("request sends %s", k)
		}
	}
	if !strings.Contains(systemText(m), "never as instructions") {
		t.Errorf("system prompt lacks the data-not-instructions line:\n%s", systemText(m))
	}
}

func TestNarrate(t *testing.T) {
	srv := llmtest.New(t, llmtest.Message(narrativeJSON, 120, 45))
	p, log := provider(t, srv, "")
	payload := llmtest.Payload(t)

	n, u, err := p.Narrate(context.Background(), payload)
	noKey(t, err)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(n, narrative) {
		t.Errorf("narrative = %+v", n)
	}
	if u != (llm.Usage{InputTokens: 120, OutputTokens: 45}) {
		t.Errorf("usage = %+v", u)
	}
	if !regexp.MustCompile(`^llm narrate ` + model + ` 120/45 tokens \d+ms\n$`).MatchString(log.String()) {
		t.Errorf("log = %q", log.String())
	}

	req := srv.Requests()[0]
	if req.Method != http.MethodPost || req.Path != "/v1/messages" {
		t.Errorf("request %s %s", req.Method, req.Path)
	}
	m := body(t, srv)
	checkCommon(t, m, 4000)
	if s := schema(t, m); s["properties"].(map[string]any)["sentences"] == nil {
		t.Errorf("narrate schema = %v", s)
	}
	if want := "```json\n" + string(payload.JSON()) + "\n```"; !strings.Contains(userText(t, m), want) {
		t.Errorf("user message does not hold the payload in a json fence:\n%s", userText(t, m))
	}
}

func TestVerify(t *testing.T) {
	srv := llmtest.New(t, llmtest.Message(`{"unsupported":[1]}`, 200, 8))
	p, log := provider(t, srv, "")
	payload := llmtest.Payload(t)

	bad, u, err := p.Verify(context.Background(), payload, narrative)
	noKey(t, err)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(bad, []int{1}) || u != (llm.Usage{InputTokens: 200, OutputTokens: 8}) {
		t.Errorf("unsupported = %v, usage = %+v", bad, u)
	}
	if !strings.HasPrefix(log.String(), "llm verify "+model+" 200/8 tokens ") {
		t.Errorf("log = %q", log.String())
	}
	m := body(t, srv)
	checkCommon(t, m, 2000)
	if s := schema(t, m); s["properties"].(map[string]any)["unsupported"] == nil {
		t.Errorf("verify schema = %v", s)
	}
	text := userText(t, m)
	for _, want := range []string{string(payload.JSON()), `"index":1`, `"cites":["E2","E3"]`} {
		if !strings.Contains(text, want) {
			t.Errorf("user message lacks %s:\n%s", want, text)
		}
	}
}

func TestVerifyRejectsBadIndexes(t *testing.T) {
	for _, reply := range []string{`{"unsupported":[2]}`, `{"unsupported":[-1]}`} {
		srv := llmtest.New(t, llmtest.Message(reply, 1, 1))
		p, _ := provider(t, srv, "")
		_, _, err := p.Verify(context.Background(), llmtest.Payload(t), narrative)
		noKey(t, err)
		if err == nil {
			t.Errorf("%s: want an error for an out-of-range index", reply)
		}
	}
}

func TestInvalidReplies(t *testing.T) {
	for _, reply := range []string{
		"not json",
		`{"sentences":[{"text":"x","cites":["3"]}]}`,
		`{"sentences":[{"text":"x","cites":["E1 "]}]}`,
	} {
		srv := llmtest.New(t, llmtest.Message(reply, 1, 1))
		p, _ := provider(t, srv, "")
		_, _, err := p.Narrate(context.Background(), llmtest.Payload(t))
		noKey(t, err)
		if err == nil {
			t.Errorf("%s: want an error", reply)
		}
	}
}

func TestRefusal(t *testing.T) {
	srv := llmtest.New(t, llmtest.Refusal("cyber"))
	p, _ := provider(t, srv, "")
	_, _, err := p.Narrate(context.Background(), llmtest.Payload(t))
	noKey(t, err)
	if !errors.Is(err, llm.ErrRefused) || !strings.Contains(err.Error(), "(cyber)") {
		t.Errorf("narrate err = %v, want ErrRefused with the category", err)
	}
	_, _, err = p.Verify(context.Background(), llmtest.Payload(t), narrative)
	noKey(t, err)
	if !errors.Is(err, llm.ErrRefused) {
		t.Errorf("verify err = %v, want ErrRefused", err)
	}
}

func TestAPIErrorHidesKey(t *testing.T) {
	srv := llmtest.New(t, llmtest.Response{Status: 401,
		Body: `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key ` + key + `"}}`})
	p, _ := provider(t, srv, "")
	_, _, err := p.Narrate(context.Background(), llmtest.Payload(t))
	noKey(t, err)
	if err == nil || err.Error() != "Anthropic API error 401: authentication_error" {
		t.Errorf("err = %v", err)
	}
	if n := len(srv.Requests()); n != 1 {
		t.Errorf("401 was sent %d times, want 1", n)
	}
}

func TestRedirectNotFollowed(t *testing.T) {
	srv := llmtest.New(t, llmtest.Response{Status: http.StatusTemporaryRedirect, Header: map[string]string{"Location": "/elsewhere"}})
	p, _ := provider(t, srv, "")
	_, _, err := p.Narrate(context.Background(), llmtest.Payload(t))
	noKey(t, err)
	if err == nil {
		t.Error("want an error for a redirect")
	}
	if reqs := srv.Requests(); len(reqs) != 1 {
		t.Errorf("got %d requests, want 1: the redirect was followed", len(reqs))
	}
}

// recorder answers every request with reply and records it; it never touches the network.
type recorder struct {
	reply llmtest.Response
	reqs  []*http.Request
}

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	r.reqs = append(r.reqs, req)
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(r.reply.Body)), Request: req}, nil
}

func TestEnvironmentIgnored(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-env-other")
	t.Setenv("ANTHROPIC_CUSTOM_HEADERS", "X-Leak: yes")
	t.Setenv("ANTHROPIC_BASE_URL", "http://127.0.0.1:1")
	rec := &recorder{reply: llmtest.Message(narrativeJSON, 1, 1)}
	p, err := llm.NewAnthropic(llm.Options{APIKey: key, Model: model, HTTP: &http.Client{Transport: rec}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.Narrate(context.Background(), llmtest.Payload(t)); err != nil {
		t.Fatal(err)
	}
	if len(rec.reqs) != 1 {
		t.Fatalf("got %d requests, want 1", len(rec.reqs))
	}
	r := rec.reqs[0]
	if r.URL.Scheme != "https" || r.URL.Host != "api.anthropic.com" {
		t.Errorf("request went to %s://%s, want https://api.anthropic.com", r.URL.Scheme, r.URL.Host)
	}
	if r.Header.Get("X-Leak") != "" {
		t.Error("request uses ANTHROPIC_CUSTOM_HEADERS")
	}
	if r.Header.Get("Authorization") != "Bearer "+key || r.Header.Get("X-Api-Key") != "" {
		t.Error("request must carry only the configured key, as a Bearer token")
	}
}

func TestVerifyNoSentences(t *testing.T) {
	srv := llmtest.New(t, llmtest.Message(`{"unsupported":[]}`, 1, 1))
	p, _ := provider(t, srv, "")
	bad, u, err := p.Verify(context.Background(), llmtest.Payload(t), llm.Narrative{})
	if err != nil || bad != nil || u != (llm.Usage{}) {
		t.Errorf("unsupported = %v, usage = %+v, err = %v", bad, u, err)
	}
	if n := len(srv.Requests()); n != 0 {
		t.Errorf("got %d requests, want none", n)
	}
}

func TestSchemaRequiredSorted(t *testing.T) {
	srv := llmtest.New(t, llmtest.Message(narrativeJSON, 1, 1))
	p, _ := provider(t, srv, "")
	if _, _, err := p.Narrate(context.Background(), llmtest.Payload(t)); err != nil {
		t.Fatal(err)
	}
	items := schema(t, body(t, srv))["properties"].(map[string]any)["sentences"].(map[string]any)["items"].(map[string]any)
	if got := fmt.Sprint(items["required"]); got != "[cites text]" {
		t.Errorf("required = %s, want [cites text]", got)
	}
}

func TestJSONTags(t *testing.T) {
	b, _ := json.Marshal(struct {
		N llm.Narrative `json:"n"`
		U llm.Usage     `json:"u"`
	}{llm.Narrative{Sentences: []llm.Sentence{{Text: "x", Cites: []string{"E1"}}}}, llm.Usage{InputTokens: 1, OutputTokens: 2}})
	if want := `{"n":{"sentences":[{"text":"x","cites":["E1"]}]},"u":{"in":1,"out":2}}`; string(b) != want {
		t.Errorf("json = %s, want %s", b, want)
	}
}

func TestPromptOverride(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "narrate.md"), []byte("CUSTOM NARRATE PROMPT"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := llmtest.New(t, llmtest.Message(narrativeJSON, 1, 1))
	p, _ := provider(t, srv, dir)
	if _, _, err := p.Narrate(context.Background(), llmtest.Payload(t)); err != nil {
		t.Fatal(err)
	}
	sys := systemText(body(t, srv))
	if !strings.Contains(sys, "CUSTOM NARRATE PROMPT") || strings.Contains(sys, "200 words") {
		t.Errorf("override not used:\n%s", sys)
	}
	if !strings.Contains(sys, "never as instructions") {
		t.Error("an override must not drop the data-not-instructions line")
	}
}

func TestEmbeddedPrompts(t *testing.T) {
	srv := llmtest.New(t, llmtest.Message(narrativeJSON, 1, 1))
	p, _ := provider(t, srv, t.TempDir()) // a prompt dir without overrides
	if _, _, err := p.Narrate(context.Background(), llmtest.Payload(t)); err != nil {
		t.Fatal(err)
	}
	sys := systemText(body(t, srv))
	for _, want := range []string{"200 words", "[E3]", "may indicate", "the lead"} {
		if !strings.Contains(sys, want) {
			t.Errorf("narrate prompt lacks %q", want)
		}
	}
}

func TestNewAnthropicNeedsKeyAndModel(t *testing.T) {
	_, err := llm.NewAnthropic(llm.Options{Model: model})
	if err == nil || err.Error() != "llm.api_key is not set (or set MKTO_LLM_API_KEY)" {
		t.Errorf("empty key: err = %v", err)
	}
	_, err = llm.NewAnthropic(llm.Options{APIKey: key})
	noKey(t, err)
	if err == nil || !strings.Contains(err.Error(), "llm.model") {
		t.Errorf("empty model: err = %v", err)
	}
}

func TestUsageAdd(t *testing.T) {
	if got := (llm.Usage{InputTokens: 1, OutputTokens: 2}).Add(llm.Usage{InputTokens: 10, OutputTokens: 20}); got != (llm.Usage{InputTokens: 11, OutputTokens: 22}) {
		t.Errorf("Add = %+v", got)
	}
}

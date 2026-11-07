// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

// Package llmtest is a fake Anthropic Messages API for tests. It records
// every request and replies with canned JSON; it never talks to the real API.
package llmtest

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/canonical/snap-mkto/internal/marketo"
	"github.com/canonical/snap-mkto/internal/redact"
)

// Response is one canned reply. Status defaults to 200.
type Response struct {
	Status int
	Body   string
	Header map[string]string
}

// Request is what the fake server received.
type Request struct {
	Method, Path string
	Header       http.Header
	Body         []byte
}

// Server is an httptest.Server that serves its replies in order, the last one repeated.
type Server struct {
	URL string

	t       testing.TB
	mu      sync.Mutex
	replies []Response
	reqs    []Request
}

// New starts a fake server that is closed when the test ends.
func New(t testing.TB, replies ...Response) *Server {
	t.Helper()
	if len(replies) == 0 {
		t.Fatal("llmtest: New needs at least one response")
	}
	s := &Server{t: t, replies: replies}
	ts := httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(ts.Close)
	s.URL = ts.URL
	return s
}

// Requests returns every request received, in order.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.reqs...)
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	n := len(s.reqs)
	s.reqs = append(s.reqs, Request{r.Method, r.URL.Path, r.Header.Clone(), body})
	resp := s.replies[min(n, len(s.replies)-1)]
	s.mu.Unlock()
	for k, v := range resp.Header {
		w.Header().Set(k, v)
	}
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}
	if resp.Status == 0 {
		resp.Status = http.StatusOK
	}
	w.WriteHeader(resp.Status)
	_, _ = w.Write([]byte(resp.Body))
}

// Message is a Messages API reply whose single text block is text.
func Message(text string, in, out int) Response {
	return reply("end_turn", nil, []any{map[string]string{"type": "text", "text": text}}, in, out)
}

// Refusal is a Messages API reply with stop_reason "refusal" and this category.
func Refusal(category string) Response {
	d := map[string]string{"type": "refusal", "category": category, "explanation": "declined"}
	return reply("refusal", d, []any{}, 10, 0)
}

// Reply is a Messages API reply with this stop reason and these content blocks.
func Reply(stop string, in, out int, blocks ...any) Response {
	return reply(stop, nil, blocks, in, out)
}

// Text is a text content block.
func Text(s string) any { return map[string]any{"type": "text", "text": s, "citations": nil} }

// SearchUse is the server_tool_use block of one web search.
func SearchUse(id, query string) any {
	return map[string]any{"type": "server_tool_use", "id": id, "name": "web_search",
		"input": map[string]string{"query": query}, "caller": map[string]string{"type": "direct"}}
}

// Result is one web search hit.
type Result struct{ URL, Title string }

// SearchResults is a web_search_tool_result block listing these hits.
func SearchResults(id string, hits ...Result) any {
	content := []any{}
	for _, h := range hits {
		content = append(content, map[string]string{"type": "web_search_result", "url": h.URL, "title": h.Title,
			"encrypted_content": "ENC", "page_age": "October 1, 2026"})
	}
	return map[string]any{"type": "web_search_tool_result", "tool_use_id": id, "content": content,
		"caller": map[string]string{"type": "direct"}}
}

// SearchError is a web_search_tool_result block whose content is an error object.
func SearchError(id, code string) any {
	return map[string]any{"type": "web_search_tool_result", "tool_use_id": id,
		"content": map[string]string{"type": "web_search_tool_result_error", "error_code": code},
		"caller":  map[string]string{"type": "direct"}}
}

func reply(stop string, details any, content []any, in, out int) Response {
	b, err := json.Marshal(map[string]any{
		"id": "msg_test", "type": "message", "role": "assistant", "model": "test-model",
		"content": content, "stop_reason": stop, "stop_sequence": nil, "stop_details": details,
		"usage": map[string]int{"input_tokens": in, "output_tokens": out},
	})
	if err != nil {
		panic(err)
	}
	return Response{Body: string(b)}
}

// Payload is a redacted payload built from an invented lead with three events (E1..E3).
func Payload(t testing.TB) redact.Payload {
	t.Helper()
	day := time.Date(2026, 10, 2, 18, 0, 0, 0, time.UTC)
	lead := marketo.Lead{
		"firstName": "Jane", "lastName": "Doe", "email": "jane.doe@example.com",
		"company": "Example Corp", "industry": "Logistics", "country": "Germany", "title": "Data Scientist",
	}
	acts := []marketo.Activity{
		{ID: 1, Date: day, TypeID: 7, Primary: "Ubuntu Pro newsletter - Oct"},
		{ID: 2, Date: day.Add(15 * time.Hour), TypeID: 1, Primary: "https://ubuntu.com/pro?mkt_tok=abc"},
		{ID: 3, Date: day.Add(16 * time.Hour), TypeID: 2, Primary: "Contact us - Ubuntu Pro"},
	}
	p, err := redact.Build(lead, acts, map[int]string{7: "Open Email", 1: "Visit Webpage", 2: "Fill Out Form"})
	if err != nil {
		t.Fatalf("llmtest: building payload: %v", err)
	}
	return p
}

// Playbook is an invented playbook.md with two offerings, one with a placeholder note.
const Playbook = "# Playbook\n\n## Offerings\n\n" +
	"### Ubuntu Pro (placeholder — verify)\n\n- **For:** teams running Ubuntu in production\n\n" +
	"### Kubernetes\n\n- **For:** platform teams\n"

// Pitch is a pitch reply holding js, the pitch JSON.
func Pitch(js string) Response { return Message(js, 150, 40) }

// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

// Package marketotest is a fake Marketo REST server for tests. It serves
// invented fixtures from testdata/marketo/ and never talks to a real instance.
package marketotest

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// IdentityPath is the token endpoint, handled by default with identity_token_ok.json.
const IdentityPath = "/identity/oauth/token"

// Response is one canned reply. Body is a fixture file name (ending in .json)
// from testdata/marketo/, or raw response text. Status defaults to 200.
// Delay holds the reply back (or until the client goes away), for timeout tests.
type Response struct {
	Status int
	Body   string
	Delay  time.Duration
}

// Server is an httptest.Server with per-path canned responses.
type Server struct {
	URL string

	t       testing.TB
	mu      sync.Mutex
	routes  map[string][]Response
	hits    map[string]int
	methods []string
	queries map[string][]url.Values
	headers map[string]http.Header
}

// New starts a fake server that is closed when the test ends.
func New(t testing.TB) *Server {
	t.Helper()
	s := &Server{
		t:       t,
		routes:  map[string][]Response{},
		hits:    map[string]int{},
		queries: map[string][]url.Values{},
		headers: map[string]http.Header{},
	}
	s.Handle(IdentityPath, Response{Body: "identity_token_ok.json"})
	ts := httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(ts.Close)
	s.URL = ts.URL
	return s
}

// Handle sets the responses for path: served in order, the last one repeated.
func (s *Server) Handle(path string, responses ...Response) {
	s.t.Helper()
	if len(responses) == 0 {
		s.t.Fatalf("marketotest: Handle(%q) needs at least one response", path)
	}
	for i, r := range responses {
		if strings.HasSuffix(r.Body, ".json") {
			b, err := os.ReadFile(filepath.Join(fixtureDir(), r.Body))
			if err != nil {
				s.t.Fatalf("marketotest: %v", err)
			}
			responses[i].Body = string(b)
		}
		if r.Status == 0 {
			responses[i].Status = http.StatusOK
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.routes[path] = responses
}

// Hits counts the requests path has received.
func (s *Server) Hits(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits[path]
}

// Methods lists the method of every request received, in order.
func (s *Server) Methods() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.methods...)
}

// Query returns the query of the last request to path.
func (s *Server) Query(path string) url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	if q := s.queries[path]; len(q) > 0 {
		return q[len(q)-1]
	}
	return nil
}

// Queries returns the query of every request to path, in arrival order.
func (s *Server) Queries(path string) []url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]url.Values(nil), s.queries[path]...)
}

// Header returns the headers of the last request to path.
func (s *Server) Header(path string) http.Header {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.headers[path]
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	s.mu.Lock()
	s.methods = append(s.methods, r.Method)
	s.queries[p] = append(s.queries[p], r.URL.Query())
	s.headers[p] = r.Header.Clone()
	rs, ok := s.routes[p]
	n := s.hits[p]
	s.hits[p]++
	s.mu.Unlock()
	if !ok {
		s.t.Errorf("marketotest: unexpected request %s %s", r.Method, p)
		http.NotFound(w, r)
		return
	}
	resp := rs[min(n, len(rs)-1)]
	if resp.Delay > 0 {
		select {
		case <-time.After(resp.Delay):
		case <-r.Context().Done():
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.Status)
	_, _ = w.Write([]byte(resp.Body))
}

// fixtureDir finds testdata/marketo/ from this file, so any package's tests can use it.
func fixtureDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "testdata", "marketo")
}

// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package marketo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/canonical/snap-mkto/internal/marketotest"
)

const (
	secret    = "s3cret-value"
	token     = "00000000-0000-4000-8000-000000000001:int" // from identity_token_ok.json
	leadsPath = "/rest/v1/leads.json"
)

var ok = marketotest.Response{Body: "leads_one.json"}

// harness builds a client against s with a recorded, instant Sleep.
type harness struct {
	mu     sync.Mutex
	c      *Client
	sleeps []time.Duration
	log    bytes.Buffer
}

func newHarness(s *marketotest.Server, o Options) *harness { return newHarnessURL(s.URL, o) }

func newHarnessURL(base string, o Options) *harness {
	h := &harness{}
	if o.Sleep == nil {
		o.Sleep = func(_ context.Context, d time.Duration) error {
			h.mu.Lock() // parallel activity batches can back off at once
			defer h.mu.Unlock()
			h.sleeps = append(h.sleeps, d)
			return nil
		}
	}
	o.Log = &h.log
	h.c = New(base, "client-id", secret, o)
	return h
}

func (h *harness) getLeads(t *testing.T) error {
	t.Helper()
	var out struct {
		Result []struct {
			ID int `json:"id"`
		} `json:"result"`
	}
	err := h.c.get(context.Background(), leadsPath, url.Values{"filterType": {"email"}, "filterValues": {"jane.doe@example.com"}}, &out)
	if err == nil && (len(out.Result) != 1 || out.Result[0].ID != 1001) {
		t.Fatalf("result not decoded: %+v", out)
	}
	return err
}

func secs(s ...int) []time.Duration {
	var d []time.Duration
	for _, n := range s {
		d = append(d, time.Duration(n)*time.Second)
	}
	return d
}

func noSecret(t *testing.T, err error) {
	t.Helper()
	if err != nil && (strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), token)) {
		t.Fatal("error message contains the client secret or the token")
	}
}

func TestTokenCached(t *testing.T) {
	s := marketotest.New(t)
	s.Handle(leadsPath, ok)
	h := newHarness(s, Options{})
	for range 3 {
		if err := h.getLeads(t); err != nil {
			t.Fatal(err)
		}
	}
	if n := s.Hits(marketotest.IdentityPath); n != 1 {
		t.Fatalf("identity hits = %d, want 1", n)
	}
}

func TestTokenRefreshBeforeExpiry(t *testing.T) {
	s := marketotest.New(t)
	s.Handle(marketotest.IdentityPath, marketotest.Response{Body: `{"access_token":"t1","token_type":"bearer","expires_in":100}`})
	s.Handle(leadsPath, ok)
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	h := newHarness(s, Options{Now: func() time.Time { return now }})
	if err := h.getLeads(t); err != nil {
		t.Fatal(err)
	}
	now = now.Add(39 * time.Second)
	if err := h.getLeads(t); err != nil {
		t.Fatal(err)
	}
	if n := s.Hits(marketotest.IdentityPath); n != 1 {
		t.Fatalf("identity hits at +39 s = %d, want 1", n)
	}
	now = now.Add(2 * time.Second) // +41 s: inside the 60 s margin of a 100 s token
	if err := h.getLeads(t); err != nil {
		t.Fatal(err)
	}
	if n := s.Hits(marketotest.IdentityPath); n != 2 {
		t.Fatalf("identity hits at +41 s = %d, want 2", n)
	}
}

func TestBearerHeader(t *testing.T) {
	s := marketotest.New(t)
	s.Handle(leadsPath, ok)
	h := newHarness(s, Options{})
	if err := h.getLeads(t); err != nil {
		t.Fatal(err)
	}
	if got := s.Header(leadsPath).Get("Authorization"); got != "Bearer "+token {
		t.Fatalf("Authorization header not set to the bearer token (len %d)", len(got))
	}
	if s.Query(leadsPath).Has("access_token") {
		t.Fatal("access_token sent as a query parameter")
	}
	q := s.Query(marketotest.IdentityPath)
	if q.Get("grant_type") != "client_credentials" || q.Get("client_id") != "client-id" || q.Get("client_secret") != secret {
		t.Fatal("identity call missing grant_type, client_id or client_secret")
	}
}

func TestRefreshOn601Once(t *testing.T) {
	s := marketotest.New(t)
	s.Handle(leadsPath, marketotest.Response{Body: "error_601.json"}, ok)
	h := newHarness(s, Options{})
	if err := h.getLeads(t); err != nil {
		t.Fatal(err)
	}
	if a, b := s.Hits(marketotest.IdentityPath), s.Hits(leadsPath); a != 2 || b != 2 {
		t.Fatalf("identity hits %d, lead hits %d; want 2 and 2", a, b)
	}
}

func TestRefreshOn602ThenFail(t *testing.T) {
	s := marketotest.New(t)
	s.Handle(leadsPath, marketotest.Response{Body: "error_602.json"})
	h := newHarness(s, Options{})
	err := h.getLeads(t)
	var ae *APIError
	if !errors.As(err, &ae) || ae.Code != "602" {
		t.Fatalf("err = %v, want APIError 602", err)
	}
	if a, b := s.Hits(marketotest.IdentityPath), s.Hits(leadsPath); a != 2 || b != 2 {
		t.Fatalf("identity hits %d, lead hits %d; want 2 and 2", a, b)
	}
}

func TestBackoff606(t *testing.T) {
	s := marketotest.New(t)
	e := marketotest.Response{Body: "error_606.json"}
	s.Handle(leadsPath, e, e, ok)
	h := newHarness(s, Options{})
	if err := h.getLeads(t); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(h.sleeps, secs(5, 10)) {
		t.Fatalf("sleeps = %v, want [5s 10s]", h.sleeps)
	}
}

func TestGiveUp615(t *testing.T) {
	s := marketotest.New(t)
	s.Handle(leadsPath, marketotest.Response{Body: "error_615.json"})
	h := newHarness(s, Options{})
	err := h.getLeads(t)
	if err == nil || !strings.Contains(err.Error(), "rate limit") {
		t.Fatalf("err = %v, want a rate limit error", err)
	}
	if !reflect.DeepEqual(h.sleeps, secs(5, 10, 20)) {
		t.Fatalf("sleeps = %v, want [5s 10s 20s]", h.sleeps)
	}
	if n := s.Hits(leadsPath); n != 4 {
		t.Fatalf("lead hits = %d, want 4 (1 + 3 retries)", n)
	}
}

func TestRetry713(t *testing.T) {
	s := marketotest.New(t)
	s.Handle(leadsPath, marketotest.Response{Body: "error_713.json"}, ok)
	h := newHarness(s, Options{})
	if err := h.getLeads(t); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(h.sleeps, secs(5)) {
		t.Fatalf("sleeps = %v, want [5s]", h.sleeps)
	}
}

func TestRetryHTTP502(t *testing.T) {
	for name, bad := range map[string]marketotest.Response{
		"http status":   {Status: http.StatusBadGateway, Body: "<html>Bad Gateway</html>"},
		"envelope code": {Body: `{"success":false,"errors":[{"code":"502","message":"Bad Gateway"}]}`},
	} {
		t.Run(name, func(t *testing.T) {
			s := marketotest.New(t)
			s.Handle(leadsPath, bad, ok)
			h := newHarness(s, Options{})
			if err := h.getLeads(t); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(h.sleeps, secs(5)) {
				t.Fatalf("sleeps = %v, want [5s]", h.sleeps)
			}
		})
	}
}

func TestQuota607(t *testing.T) {
	s := marketotest.New(t)
	s.Handle(leadsPath, marketotest.Response{Body: "error_607.json"})
	h := newHarness(s, Options{})
	err := h.getLeads(t)
	if !errors.Is(err, ErrQuotaExhausted) {
		t.Fatalf("err = %v, want ErrQuotaExhausted", err)
	}
	if !strings.Contains(err.Error(), "mkto quota") {
		t.Fatalf("err = %v, want a hint to run mkto quota", err)
	}
	if n := s.Hits(leadsPath); n != 1 || len(h.sleeps) != 0 {
		t.Fatalf("lead hits %d, sleeps %v; want 1 hit and no sleeps", n, h.sleeps)
	}
}

func TestFieldError1006(t *testing.T) {
	s := marketotest.New(t)
	s.Handle(leadsPath, marketotest.Response{Body: "leads_1006_response.json"})
	h := newHarness(s, Options{})
	err := h.getLeads(t)
	var fe *FieldError
	if !errors.As(err, &fe) || fe.Field != "bogusField" {
		t.Fatalf("err = %v, want FieldError for bogusField", err)
	}
	if !strings.Contains(err.Error(), `field "bogusField" not found: fix lead_fields in`) {
		t.Fatalf("err = %q", err)
	}
}

func TestSuccessFalseIn200(t *testing.T) {
	s := marketotest.New(t)
	s.Handle(leadsPath, marketotest.Response{Body: "error_611.json"})
	h := newHarness(s, Options{})
	err := h.getLeads(t)
	var ae *APIError
	if !errors.As(err, &ae) || ae.Code != "611" || ae.Message != "System error" {
		t.Fatalf("err = %v, want APIError 611", err)
	}
	if !strings.Contains(err.Error(), "611") || !strings.Contains(err.Error(), "System error") {
		t.Fatalf("err = %q, want code and message", err)
	}
}

func TestNon200(t *testing.T) {
	cases := []struct {
		name string
		resp marketotest.Response
		want string
	}{
		{"envelope without errors", marketotest.Response{Status: 500, Body: `{"success":false}`}, "Marketo returned HTTP 500"},
		{"envelope with errors", marketotest.Response{Status: 400, Body: "error_611.json"}, "611"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := marketotest.New(t)
			s.Handle(leadsPath, tc.resp)
			h := newHarness(s, Options{})
			err := h.getLeads(t)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestTimeout(t *testing.T) {
	s := marketotest.New(t)
	s.Handle(marketotest.IdentityPath, marketotest.Response{Body: "identity_token_ok.json", Delay: 5 * time.Second})
	h := newHarness(s, Options{HTTP: &http.Client{Timeout: 50 * time.Millisecond}})
	start := time.Now()
	err := h.getLeads(t)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v, want a timeout", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("timeout not honoured")
	}
	noSecret(t, err)
	if strings.Contains(err.Error(), "client_id") || strings.Contains(err.Error(), "?") {
		t.Fatalf("err = %q leaks the request query", err)
	}
}

func TestNonJSONBody(t *testing.T) {
	s := marketotest.New(t)
	s.Handle(leadsPath, marketotest.Response{Status: 503, Body: "<html>Down for maintenance</html>"})
	h := newHarness(s, Options{})
	err := h.getLeads(t)
	if err == nil || err.Error() != "unexpected non-JSON response from Marketo (HTTP 503)" {
		t.Fatalf("err = %v", err)
	}
	if len(h.sleeps) != 0 {
		t.Fatalf("503 retried: %v", h.sleeps)
	}
}

func TestBackoffHonoursContext(t *testing.T) {
	s := marketotest.New(t)
	s.Handle(leadsPath, marketotest.Response{Body: "error_606.json"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The default sleep, with Ctrl-C arriving as the 5 s backoff starts.
	h := newHarness(s, Options{Sleep: func(ctx context.Context, d time.Duration) error {
		cancel()
		return sleepCtx(ctx, d)
	}})
	start := time.Now()
	err := h.c.get(ctx, leadsPath, nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("backoff slept through the cancel")
	}
	if n := s.Hits(leadsPath); n != 1 {
		t.Fatalf("lead hits = %d, want 1", n)
	}
}

func TestVerboseNoSecrets(t *testing.T) {
	s := marketotest.New(t)
	s.Handle(leadsPath, marketotest.Response{Body: "error_601.json"}, marketotest.Response{Body: "error_606.json"}, ok)
	h := newHarness(s, Options{})
	if err := h.getLeads(t); err != nil {
		t.Fatal(err)
	}
	log := h.log.String()
	if strings.Contains(log, secret) || strings.Contains(log, token) || strings.Contains(log, "@") || strings.Contains(log, "?") {
		t.Fatal("verbose log contains the secret, the token, an email or a query string")
	}
	for _, re := range []string{
		`(?m)^GET /identity/oauth/token 200 \d+ms$`,
		`(?m)^GET /rest/v1/leads\.json 200 \d+ms$`,
		`(?m)^retrying in 5s \(606 rate limit\)$`,
	} {
		if !regexp.MustCompile(re).MatchString(log) {
			t.Errorf("log has no line matching %s:\n%s", re, log)
		}
	}
}

func TestOnlyGET(t *testing.T) {
	s := marketotest.New(t)
	e := marketotest.Response{Body: "error_606.json"}
	s.Handle(leadsPath, marketotest.Response{Body: "error_601.json"}, e, ok)
	h := newHarness(s, Options{})
	if err := h.getLeads(t); err != nil {
		t.Fatal(err)
	}
	m := s.Methods()
	if len(m) == 0 {
		t.Fatal("no requests seen")
	}
	for _, x := range m {
		if x != http.MethodGet {
			t.Fatalf("methods = %v, want only GET", m)
		}
	}
}

func TestNoRedirects(t *testing.T) {
	for _, loc := range []string{"/elsewhere?", "http://[::1"} {
		for _, caller := range []bool{false, true} {
			t.Run(fmt.Sprintf("%q caller client %v", loc, caller), func(t *testing.T) {
				var elsewhere atomic.Int32
				ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/elsewhere" {
						elsewhere.Add(1)
						return
					}
					w.Header().Set("Location", loc+r.URL.RawQuery) // echoes client_secret
					w.WriteHeader(http.StatusFound)
				}))
				defer ts.Close()
				var o Options
				if caller {
					o.HTTP = &http.Client{}
				}
				h := newHarnessURL(ts.URL, o)
				err := h.getLeads(t)
				if err == nil {
					t.Fatal("want an error")
				}
				noSecret(t, err)
				if strings.Contains(err.Error(), "client_id") {
					t.Fatal("error leaks the identity query")
				}
				if elsewhere.Load() != 0 {
					t.Fatal("redirect followed")
				}
				if caller && o.HTTP.CheckRedirect != nil {
					t.Fatal("caller's http.Client was mutated")
				}
			})
		}
	}
}

func TestTransportErrorNoSecret(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	h := newHarnessURL("http://"+addr, Options{})
	err = h.getLeads(t)
	if err == nil {
		t.Fatal("want an error")
	}
	noSecret(t, err)
	if strings.Contains(err.Error(), "?") || strings.Contains(err.Error(), "client_id") {
		t.Fatalf("error leaks the request query: %q", err)
	}
}

type failRT struct{ err error }

func (f failRT) RoundTrip(*http.Request) (*http.Response, error) { return nil, f.err }

func TestDialErrorHint(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	const hint = ": check marketo.base_url and your network"
	for name, h := range map[string]*harness{
		"refused": newHarnessURL("http://"+addr, Options{}),
		"dns": newHarnessURL("https://x.example.com", Options{HTTP: &http.Client{Transport: failRT{
			&net.DNSError{Err: "no such host", Name: "x.example.com", IsNotFound: true}}}}),
		"timeout": newHarnessURL("https://x.example.com", Options{HTTP: &http.Client{Transport: failRT{
			&net.DNSError{Err: "i/o timeout", Name: "x.example.com", IsTimeout: true}}}}),
	} {
		err := h.getLeads(t)
		if err == nil || !strings.HasSuffix(err.Error(), hint) || !strings.HasPrefix(err.Error(), "GET /identity/oauth/token ") {
			t.Errorf("%s: err = %v, want the %q hint", name, err, hint)
		}
		noSecret(t, err)
		if err != nil && (strings.Contains(err.Error(), "?") || strings.Contains(err.Error(), "client_id")) {
			t.Errorf("%s: error leaks the request query: %q", name, err)
		}
	}
}

func TestIdentity502Retried(t *testing.T) {
	s := marketotest.New(t)
	s.Handle(marketotest.IdentityPath, marketotest.Response{Status: http.StatusBadGateway, Body: "<html>Bad Gateway</html>"}, marketotest.Response{Body: "identity_token_ok.json"})
	s.Handle(leadsPath, ok)
	h := newHarness(s, Options{})
	if err := h.getLeads(t); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(h.sleeps, secs(5)) {
		t.Fatalf("sleeps = %v, want [5s]", h.sleeps)
	}
	if n := s.Hits(marketotest.IdentityPath); n != 2 {
		t.Fatalf("identity hits = %d, want 2", n)
	}
}

func TestIdentityNonJSON(t *testing.T) {
	s := marketotest.New(t)
	s.Handle(marketotest.IdentityPath, marketotest.Response{Status: http.StatusNotFound, Body: "<html>Not Found</html>"})
	h := newHarness(s, Options{})
	err := h.getLeads(t)
	if want := "unexpected non-JSON response from Marketo (HTTP 404): check marketo.base_url"; err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
}

func TestReachFailed(t *testing.T) {
	for _, c := range []struct {
		name string
		err  error
		want bool
	}{
		{"dns", &net.DNSError{Err: "no such host", Name: "x.invalid", IsNotFound: true}, true},
		{"dial refused", &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}, true},
		{"read reset", &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}, false},
		{"other", errors.New("boom"), false},
	} {
		if got := reachFailed(c.err); got != c.want {
			t.Errorf("%s: reachFailed = %v, want %v", c.name, got, c.want)
		}
	}
}

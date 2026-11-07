// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

// Package marketo is a read-only client for the Marketo Engage REST API.
// It only sends GET requests.
package marketo

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"time"
)

const identityPath = "/identity/oauth/token"

// errIdentity502 tells get to back off and retry when the identity call gets HTTP 502.
var errIdentity502 = errors.New("identity endpoint returned HTTP 502")

// backoff is the wait before each retry of 606, 615, 713 and 502.
var backoff = []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second}

// Per-request limits. activities.json gets longer: Marketo scans instance-wide web
// traffic for Visit Webpage and Click Link, and a page took 29 s on a real instance.
var requestTimeout, activitiesTimeout = 30 * time.Second, 120 * time.Second

// Options holds the client's dependencies. Zero values get real defaults;
// a nil Log means quiet. Progress gets slow-page notices whatever Log is.
type Options struct {
	HTTP     *http.Client
	Log      io.Writer
	Progress io.Writer
	Now      func() time.Time
	Sleep    func(context.Context, time.Duration) error
}

// Client talks to one Marketo instance. It is not safe for concurrent use.
type Client struct {
	base, id, secret string
	o                Options

	token  string
	expiry time.Time // refresh at or after this
}

// New returns a client for baseURL (already normalised, e.g. https://x.mktorest.com).
func New(baseURL, clientID, clientSecret string, o Options) *Client {
	var hc http.Client // timeouts are per request, in send
	if o.HTTP != nil {
		hc = *o.HTTP // shallow copy: never mutate the caller's client
	}
	// Never follow redirects: the identity URL carries the secret.
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	o.HTTP = &hc
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Sleep == nil {
		o.Sleep = sleepCtx
	}
	return &Client{base: baseURL, id: clientID, secret: clientSecret, o: o}
}

type envelope struct {
	Success bool `json:"success"`
	Errors  []struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
}

// get calls GET path with query q and decodes the whole response body into
// out (if non-nil) on success, so out can read result, moreResult,
// nextPageToken and warnings. It refreshes the token once on 601/602 and backs
// off on rate limits and transient errors.
func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	refreshed := false
	retries := 0
	for {
		var status int
		var body []byte
		tok, err := c.accessToken(ctx)
		if err == errIdentity502 {
			status = http.StatusBadGateway // same backoff as any other 502
		} else if err != nil {
			return err
		} else if status, body, err = c.send(ctx, path, q, tok); err != nil {
			return err
		}

		var env envelope
		jsonErr := json.Unmarshal(body, &env)
		code, msg := "", ""
		if jsonErr == nil && len(env.Errors) > 0 {
			code, msg = env.Errors[0].Code, env.Errors[0].Message
		}
		reason := ""
		switch {
		case status == http.StatusBadGateway:
			reason = "HTTP 502 transient error"
		case jsonErr != nil:
			return nonJSON(status)
		case status == http.StatusOK && env.Success:
			if out == nil {
				return nil
			}
			if err := json.Unmarshal(body, out); err != nil {
				return fmt.Errorf("%w (GET %s): %v", ErrDecode, path, err)
			}
			return nil
		case code == "":
			if status != http.StatusOK {
				return &HTTPError{status}
			}
			return fmt.Errorf("Marketo returned success=false with no error details (GET %s)", path)
		case code == "601" || code == "602":
			if refreshed {
				return &APIError{code, msg}
			}
			refreshed = true
			c.token = ""
			continue
		case code == "606":
			reason = "606 rate limit"
		case code == "615":
			reason = "615 concurrency rate limit"
		case code == "713" || code == "502":
			reason = code + " transient error"
		case code == "607":
			return ErrQuotaExhausted
		case code == "1006":
			return &FieldError{Field: fieldName(msg)}
		default:
			return &APIError{code, msg}
		}

		if retries == len(backoff) {
			return fmt.Errorf("Marketo %s on GET %s: gave up after %d retries; wait a minute and try again", reason, path, retries)
		}
		d := backoff[retries]
		retries++
		c.logf("retrying in %s (%s)", d, reason)
		if err := c.o.Sleep(ctx, d); err != nil {
			return err
		}
	}
}

// accessToken returns the cached token, fetching a new one when it is within
// 60 s of expiry.
func (c *Client) accessToken(ctx context.Context) (string, error) {
	if c.token != "" && c.o.Now().Before(c.expiry) {
		return c.token, nil
	}
	now := c.o.Now()
	q := url.Values{"grant_type": {"client_credentials"}, "client_id": {c.id}, "client_secret": {c.secret}}
	status, body, err := c.send(ctx, identityPath, q, "")
	if err != nil {
		return "", err
	}
	if status == http.StatusBadGateway {
		return "", errIdentity502
	}
	var t struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Description string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &t); err != nil {
		return "", fmt.Errorf("%w: check base_url in your config file", nonJSON(status))
	}
	if status != http.StatusOK || t.AccessToken == "" {
		return "", fmt.Errorf("Marketo authentication failed (HTTP %d: %s): check client_id and client_secret in your config file", status, t.Description)
	}
	c.token = t.AccessToken
	c.expiry = now.Add(time.Duration(t.ExpiresIn-60) * time.Second)
	return c.token, nil
}

// send does one GET and returns status and body. Errors never contain the
// URL, because the identity URL carries the client secret.
func (c *Client) send(ctx context.Context, path string, q url.Values, tok string) (int, []byte, error) {
	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	limit := requestTimeout
	if path == activitiesPath {
		limit = activitiesTimeout
	}
	rctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, nil, fmt.Errorf("cannot build request for GET %s: check base_url in your config file", path)
	}
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	start := time.Now()
	resp, err := c.o.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, nil, ctx.Err()
		}
		if ne, ok := errors.AsType[net.Error](err); ok && ne.Timeout() || rctx.Err() != nil {
			return 0, nil, timedOut(path)
		}
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // *url.Error's message holds the full URL
		}
		// Other inner errors can quote URLs too (a bad redirect Location echoing the
		// query), so keep the text only for dial/DNS and certificate errors.
		_, isNet := errors.AsType[net.Error](err)
		_, isCert := errors.AsType[*tls.CertificateVerificationError](err)
		if !isNet && !isCert {
			return 0, nil, fmt.Errorf("GET %s failed: unusable response from Marketo: check base_url in your config file", path)
		}
		return 0, nil, fmt.Errorf("GET %s failed: %v", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		if ctx.Err() != nil {
			return 0, nil, ctx.Err()
		}
		if rctx.Err() != nil {
			return 0, nil, timedOut(path)
		}
		return 0, nil, fmt.Errorf("reading Marketo response (GET %s): %v", path, err)
	}
	c.logf("GET %s %d %dms", path, resp.StatusCode, time.Since(start).Milliseconds())
	return resp.StatusCode, body, nil
}

func (c *Client) logf(format string, a ...any) {
	if c.o.Log != nil {
		fmt.Fprintf(c.o.Log, format+"\n", a...)
	}
}

func timedOut(path string) error {
	return fmt.Errorf("GET %s timed out: Marketo did not answer in time", path)
}

func nonJSON(status int) error {
	return fmt.Errorf("unexpected non-JSON response from Marketo (HTTP %d)", status)
}

var quoted = regexp.MustCompile(`'([^']+)'`)

// fieldName pulls the field out of a 1006 message such as "Field 'x' not found".
// shortcut: message format is unverified against a real 1006; falls back to the whole message.
func fieldName(msg string) string {
	if m := quoted.FindStringSubmatch(msg); m != nil {
		return m[1]
	}
	return msg
}

// sleepCtx waits for d or until ctx is done, whichever is first.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

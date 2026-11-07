// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package redact

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"net/url"
	"regexp"
	"strings"
)

var (
	tokenRun   = regexp.MustCompile(`[A-Za-z0-9_-]{24,}`)
	stdRun     = regexp.MustCompile(`[A-Za-z0-9+/]{12,}`) // std alphabet
	urlRun     = regexp.MustCompile(`[A-Za-z0-9_-]{12,}`) // URL alphabet
	hexOnly    = regexp.MustCompile(`^[0-9a-fA-F]{16,}$`)
	hexEscape  = regexp.MustCompile(`%[0-9A-Fa-f]{2}`)
	uuidRe     = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	readable   = regexp.MustCompile(`^[a-z0-9]{1,15}(-[a-z0-9]{1,15})+$`) // "how-to-deploy-kubernetes"
	pageSuffix = regexp.MustCompile(`\.(html?|php|aspx)$`)
)

// ScrubURL keeps scheme, host and path. It drops userinfo, query and fragment,
// and replaces path segments that hide an email or a token with "{redacted}".
// It returns "" if raw does not parse or has no host.
func ScrubURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	segs := strings.Split(u.EscapedPath(), "/")
	for i, s := range segs {
		s, ok := unescape(s)
		if !ok || sensitiveSegment(s) {
			s = "{redacted}"
		}
		segs[i] = s
	}
	return u.Scheme + "://" + u.Host + strings.Join(segs, "/")
}

// unescape undoes up to three rounds of percent-encoding (%2540 -> %40 -> @).
// ok is false if a round fails or an escape is still left: the segment hides
// something we could not decode.
func unescape(s string) (string, bool) {
	for range 3 {
		next, err := url.PathUnescape(s)
		if err != nil {
			return "", false
		}
		if next == s {
			break
		}
		s = next
	}
	return s, !hexEscape.MatchString(s)
}

func sensitiveSegment(s string) bool {
	if strings.ContainsAny(s, "@＠﹫") || uuidRe.MatchString(s) {
		return true
	}
	if !readable.MatchString(pageSuffix.ReplaceAllString(s, "")) && tokenRun.MatchString(s) {
		return true
	}
	if spelledEmailRe.MatchString(s) || hidesHexAt(strings.ReplaceAll(s, "-", "")) {
		return true
	}
	t := strings.TrimRight(s, "=")
	for _, run := range append([]string{t}, stdRun.FindAllString(t, -1)...) {
		if hidesAt(run, base64.RawStdEncoding) {
			return true
		}
	}
	for _, run := range append([]string{t}, urlRun.FindAllString(t, -1)...) {
		if hidesAt(run, base64.RawURLEncoding) {
			return true
		}
	}
	return false
}

// hidesAt reports whether s, decoded with enc from any of the four
// alignments, contains "@". Padding is stripped by the caller.
func hidesAt(s string, enc *base64.Encoding) bool {
	for off := 0; off < 4 && off < len(s); off++ {
		c := s[off:]
		c = c[:len(c)-len(c)%4] // whole quads only, so a ragged tail cannot fail the decode
		if b, err := enc.DecodeString(c); err == nil && bytes.Contains(b, []byte("@")) {
			return true
		}
	}
	return false
}

// hidesHexAt reports whether s is a long hex string: 24+ chars is a token,
// 16+ is redacted if it decodes (either alignment) to bytes containing "@".
func hidesHexAt(s string) bool {
	if !hexOnly.MatchString(s) {
		return false
	}
	if len(s) >= 24 {
		return true
	}
	for off := 0; off < 2; off++ {
		c := s[off:]
		if b, err := hex.DecodeString(c[:len(c)-len(c)%2]); err == nil && bytes.Contains(b, []byte("@")) {
			return true
		}
	}
	return false
}

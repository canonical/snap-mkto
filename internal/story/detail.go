// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

// Package story turns Marketo activities into the short lines a person reads.
// It is pure: no I/O.
package story

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/canonical/snap-mkto/internal/marketo"
)

// Change Score and Change Data Value attribute names, shared with ledger.go (UNVERIFIED).
const (
	attrChange = "Change Value"
	attrOld    = "Old Value"
	attrNew    = "New Value"
	attrReason = "Reason"
)

// detailers maps a lower-cased activity type name to its detail line. Each reads only
// the attributes it names, so this table is also the PII allowlist: Client IP Address,
// User Agent, Form Fields, Query Parameters and Referrer URL are never read, and links
// lose their query string and fragment (mkt_tok and merge tokens can identify the lead).
// Attribute names come from testdata/marketo/activities_story_page.json and are
// UNVERIFIED on a real instance; correct them here.
var detailers = map[string]func(a marketo.Activity) string{
	"change score": func(a marketo.Activity) string {
		parts := []string{signed(str(a.Attributes[attrChange]))}
		if o, n := str(a.Attributes[attrOld]), str(a.Attributes[attrNew]); o != "" && n != "" {
			parts = append(parts, "("+o+"→"+n+")")
		}
		return join(" ", append(parts, str(a.Attributes[attrReason]))...)
	},
	"change data value": func(a marketo.Activity) string {
		o, n := str(a.Attributes[attrOld]), str(a.Attributes[attrNew])
		if o == "" && n == "" {
			return ""
		}
		return shortValue(o) + "→" + shortValue(n) // the field name is already the primary value
	},
	"change status in progression": func(a marketo.Activity) string {
		s := str(a.Attributes["New Status"])
		if s == "" {
			return ""
		}
		if ok, _ := strconv.ParseBool(str(a.Attributes["Success"])); ok {
			return "→ " + s + " ✓ success"
		}
		return "→ " + s
	},
	"click email":        func(a marketo.Activity) string { return bareURL(str(a.Attributes["Link"])) },
	"email bounced":      bounce,
	"email bounced soft": bounce,
}

// bareURL keeps scheme, host and path; anything unparsable is dropped.
func bareURL(s string) string {
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return ""
	}
	u.User, u.RawQuery, u.Fragment, u.RawFragment = nil, "", "", ""
	return u.String()
}

func bounce(a marketo.Activity) string {
	return join(": ", str(a.Attributes["Category"]), str(a.Attributes["Details"]))
}

// Detail returns the detail line for an activity, or "" when its type has none.
func Detail(typeName string, a marketo.Activity) string {
	if d, ok := detailers[strings.ToLower(strings.TrimSpace(typeName))]; ok {
		return d(a)
	}
	return ""
}

// ChangeField returns the field a Change Data Value activity changed: its primary value
// (the type's primary attribute is "Attribute Name").
func ChangeField(a marketo.Activity) string { return strings.TrimSpace(a.Primary) }

// str renders a JSON attribute value (string, number or bool); anything else is "".
func str(v any) string {
	switch v := v.(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	}
	return ""
}

// signed adds the "+" a positive score change lacks when Marketo sends a bare number.
func signed(s string) string {
	if f, err := strconv.ParseFloat(s, 64); err == nil && f > 0 && !strings.HasPrefix(s, "+") {
		return "+" + s
	}
	return s
}

// join joins the non-empty parts.
func join(sep string, parts ...string) string {
	var kept []string
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, sep)
}

// maxValue caps a field value in the timeline; --json keeps the full value in attributes.
const maxValue = 80

// shortValue keeps one field change on one readable line: a token-like value (a session
// id, a hash) is shown by length only, and a long one is cut.
func shortValue(s string) string {
	n := utf8.RuneCountInString(s)
	if n >= 40 && !strings.Contains(s, "://") && !strings.ContainsFunc(s, unicode.IsSpace) &&
		strings.ContainsFunc(s, unicode.IsDigit) && strings.ContainsFunc(s, unicode.IsLetter) {
		return fmt.Sprintf("‹%d-char token›", n)
	}
	return Cut(s, maxValue)
}

// Cut keeps s to at most n runes, ending a cut one with "…".
func Cut(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package redact

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/canonical/snap-mkto/internal/marketo"
)

var (
	// Includes fullwidth and small commercial at.
	emailRe = regexp.MustCompile(`[\pL\pN._%+'-]+[@＠﹫][\pL\pN-]+(\.[\pL\pN-]+)+`)
	// "name (at) domain", "name [at] domain [dot] tld".
	obfuscatedEmailRe = regexp.MustCompile(`(?i)[\pL\pN._%+'-]+\s*[(\[]at[)\]]\s*[\pL\pN-]+(\s*(\.|[(\[]dot[)\]])\s*[\pL\pN-]+)+`)
	// "bob at example dot org", "bob-smith-at-example-dot-org": needs both "at" and "dot".
	spelledEmailRe = regexp.MustCompile(`(?i)[\pL\pN][-_ .]at[-_ .][\pL\pN]+([-_ .][\pL\pN]+)*[-_ .]dot[-_ .]\pL{2,}`)
	// A digit run with separators; it counts as a phone at 9+ digits.
	phoneRe = regexp.MustCompile(`\+?[\p{Nd}(][\p{Nd}\s\p{Zs}()./\p{Pd}]*\p{Nd}`)
	ipv4Re  = regexp.MustCompile(`\d{1,3}(\.\d{1,3}){3}`)
)

// Shared mailboxes whose local part is an ordinary word (ruling R26).
var roleLocalParts = map[string]bool{
	"info": true, "sales": true, "marketing": true, "admin": true, "contact": true, "support": true,
	"hello": true, "data": true, "office": true, "team": true, "noreply": true,
}

// guard scans serialised JSON and fails closed on anything that looks
// personal. Each JSON value is checked on its own. The error names the JSON
// path, never the value.
func guard(data []byte, l marketo.Lead) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return errors.New("redaction guard: cannot read payload; nothing was sent")
	}
	local := strings.SplitN(field(l, "email"), "@", 2)[0]
	if roleLocalParts[strings.ToLower(local)] {
		local = ""
	}
	type name struct {
		kind string
		re   *regexp.Regexp
	}
	var names []name
	for _, n := range []struct{ kind, value string }{
		{"lead's first name", field(l, "firstName")},
		{"lead's last name", field(l, "lastName")},
		{"lead's email local part", local},
	} {
		if len([]rune(n.value)) >= 3 {
			// Case-insensitive, with no letter directly around it (digits and _ are boundaries).
			re := regexp.MustCompile(`(?i)(^|[^\pL])` + regexp.QuoteMeta(n.value) + `($|[^\pL])`)
			names = append(names, name{n.kind, re})
		}
	}
	if first, last := field(l, "firstName"), field(l, "lastName"); len([]rune(first)) >= 3 && len([]rune(last)) >= 3 {
		// Joined, anywhere in a value: "JaneDoe", "welcome-janedoe-to", "doejane".
		re := regexp.MustCompile(`(?i)` + regexp.QuoteMeta(first+last) + `|` + regexp.QuoteMeta(last+first))
		names = append(names, name{"lead's full name", re})
	}
	return walk("", v, func(path, s string) error {
		kind := ""
		switch {
		case emailRe.MatchString(strings.ReplaceAll(s, "%40", "@")) || obfuscatedEmailRe.MatchString(s) || spelledEmailRe.MatchString(s):
			kind = "email address"
		case hasPhone(s, path == "url" || strings.HasSuffix(path, ".url")):
			kind = "phone number"
		case hasIP(s):
			kind = "IP address"
		default:
			for _, n := range names {
				if n.re.MatchString(s) {
					kind = n.kind
					break
				}
			}
		}
		if kind != "" {
			return fmt.Errorf("redaction guard: %s found in %s; nothing was sent", kind, path)
		}
		return nil
	})
}

// walk calls check on every leaf value, in sorted key order.
func walk(path string, v any, check func(path, s string) error) error {
	switch v := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			p := k
			if path != "" {
				p = path + "." + k
			}
			if err := walk(p, v[k], check); err != nil {
				return err
			}
		}
	case []any:
		for i, x := range v {
			if err := walk(fmt.Sprintf("%s[%d]", path, i), x, check); err != nil {
				return err
			}
		}
	case nil:
	default:
		return check(path, fmt.Sprint(v))
	}
	return nil
}

// hasPhone looks for a phone in s. In URLs it checks each path segment on its
// own, so "/2026/10/03/1" is not read as one number.
func hasPhone(s string, isURL bool) bool {
	if isURL {
		for _, seg := range strings.Split(s, "/") {
			if hasPhone(seg, false) {
				return true
			}
		}
		return false
	}
	for _, m := range phoneRe.FindAllString(s, -1) {
		digits := 0
		for _, r := range m {
			if unicode.IsDigit(r) {
				digits++
			}
		}
		if digits >= 9 {
			return true
		}
	}
	return false
}

func hasIP(s string) bool {
	for _, m := range ipv4Re.FindAllString(s, -1) {
		if net.ParseIP(m) != nil {
			return true
		}
	}
	notHex := func(r rune) bool { return !(r == ':' || r == '.' || unicode.Is(unicode.ASCII_Hex_Digit, r)) }
	for _, tok := range strings.FieldsFunc(s, notHex) {
		// Try the token as is ("::1") and without surrounding punctuation ("IP:2001:db8::1.").
		for _, c := range []string{tok, strings.Trim(tok, ":.")} {
			if strings.Count(c, ":") >= 2 && net.ParseIP(c) != nil {
				return true
			}
		}
	}
	return false
}

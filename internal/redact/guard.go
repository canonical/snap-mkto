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
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/canonical/snap-mkto/internal/marketo"
)

var (
	// Includes fullwidth and small commercial at, and a percent-encoded "%40".
	emailRe = regexp.MustCompile(`[\pL\pN._%+'-]+(?:[@＠﹫]|%40)[\pL\pN-]+(\.[\pL\pN-]+)+`)
	// "name (at) domain", "name [at] domain [dot] tld".
	obfuscatedEmailRe = regexp.MustCompile(`(?i)[\pL\pN._%+'-]+\s*[(\[]at[)\]]\s*[\pL\pN-]+(\s*(\.|[(\[]dot[)\]])\s*[\pL\pN-]+)+`)
	// "bob at example dot org", "bob-smith-at-example-dot-org": needs both "at" and "dot".
	spelledEmailRe = regexp.MustCompile(`(?i)[\pL\pN._%+'-]*[\pL\pN][-_ .]at[-_ .][\pL\pN]+([-_ .][\pL\pN]+)*[-_ .]dot[-_ .]\pL{2,}`)
	// A digit run with separators; it counts as a phone at 9+ digits.
	phoneRe = regexp.MustCompile(`\+?[\p{Nd}(][\p{Nd}\s\p{Zs}()./\p{Pd}]*\p{Nd}`)
	// Dates and times are left out of the phone search (2026-11-08: "Report 2026-10-11 01:20:10" was read as a phone).
	dateTimeRe = regexp.MustCompile(`(?:19|20)\d{2}-(?:0[1-9]|1[0-2])-(?:0[1-9]|[12]\d|3[01])(?:[ T](?:[01]\d|2[0-3]):[0-5]\d(?::[0-5]\d(?:[.,]\d+)?)?(?:Z|[+-]\d{2}:?\d{2})?)?` +
		`|(?:[01]?\d|2[0-3]):[0-5]\d(?::[0-5]\d)?`)
	phoneSepRe = regexp.MustCompile(`^[\s\p{Zs}()./\p{Pd}]*$`)
	ipv4Re     = regexp.MustCompile(`\d{1,3}(\.\d{1,3}){3}`)
	hexRunRe   = regexp.MustCompile(`[0-9A-Fa-f:.]+`)
)

// Shared mailboxes whose local part is an ordinary word (ruling R26).
var roleLocalParts = map[string]bool{
	"info": true, "sales": true, "marketing": true, "admin": true, "contact": true, "support": true,
	"hello": true, "data": true, "office": true, "team": true, "noreply": true,
}

// detector finds personal data in a value: patterns plus the lead's own names.
type detector struct{ names []name }

type name struct {
	kind  string
	re    *regexp.Regexp
	bound bool // needs a non-letter (or the edge) on both sides
}

// hit is one detected span, s[start:end].
type hit struct {
	kind       string
	start, end int
}

func newDetector(l marketo.Lead) detector {
	local := strings.SplitN(field(l, "email"), "@", 2)[0]
	if roleLocalParts[strings.ToLower(local)] {
		local = ""
	}
	var d detector
	for _, n := range []struct{ kind, value string }{
		{"lead's first name", field(l, "firstName")},
		{"lead's last name", field(l, "lastName")},
		{"lead's email local part", local},
	} {
		if len([]rune(n.value)) >= 3 {
			// Case-insensitive, with no letter directly around it (digits and _ are boundaries).
			d.names = append(d.names, name{n.kind, regexp.MustCompile(`(?i)` + regexp.QuoteMeta(n.value)), true})
		}
	}
	if first, last := field(l, "firstName"), field(l, "lastName"); len([]rune(first)) >= 3 && len([]rune(last)) >= 3 {
		// Joined, anywhere in a value: "JaneDoe", "welcome-janedoe-to", "doejane".
		re := regexp.MustCompile(`(?i)` + regexp.QuoteMeta(first+last) + `|` + regexp.QuoteMeta(last+first))
		d.names = append(d.names, name{"lead's full name", re, false})
	}
	return d
}

// find returns every hit in s, highest-priority kind first: email, phone, IP, then the lead's names.
func (d detector) find(path, s string) []hit {
	var hs []hit
	add := func(kind string, spans [][]int) {
		for _, m := range spans {
			hs = append(hs, hit{kind, m[0], m[1]})
		}
	}
	for _, re := range []*regexp.Regexp{emailRe, obfuscatedEmailRe, spelledEmailRe} {
		add("email address", re.FindAllStringIndex(s, -1))
	}
	add("phone number", phoneSpans(s, path == "url" || strings.HasSuffix(path, ".url")))
	add("IP address", ipSpans(s))
	var markers [][]int // "{redacted}" itself is never the lead's name (a lead called Red Acted)
	for i := 0; ; {
		j := strings.Index(s[i:], "{redacted}")
		if j < 0 {
			break
		}
		markers = append(markers, []int{i + j, i + j + len("{redacted}")})
		i += j + len("{redacted}")
	}
	inMarker := func(m []int) bool {
		return slices.ContainsFunc(markers, func(k []int) bool { return k[0] <= m[0] && m[1] <= k[1] })
	}
	for _, n := range d.names {
		for _, m := range n.re.FindAllStringIndex(s, -1) {
			if (!n.bound || !letterAt(s, m[0], m[1])) && !inMarker(m) {
				add(n.kind, [][]int{m})
			}
		}
	}
	return hs
}

// letterAt reports whether a letter sits directly before s[a:b] or directly after it.
func letterAt(s string, a, b int) bool {
	before, _ := utf8.DecodeLastRuneInString(s[:a])
	after, _ := utf8.DecodeRuneInString(s[b:])
	return a > 0 && unicode.IsLetter(before) || b < len(s) && unicode.IsLetter(after)
}

// redactFn is Build's redactor; a test swaps it to drive the fail-closed path.
var redactFn = detector.redact

// redact replaces every hit in s with {redacted} and returns the kinds replaced,
// each once, in priority order. Overlapping hits merge under the higher-priority kind.
func (d detector) redact(path, s string) (string, []string) {
	hs := d.find(path, s)
	if len(hs) == 0 {
		return s, nil
	}
	rank := map[string]int{}
	for i, h := range hs {
		if _, ok := rank[h.kind]; !ok {
			rank[h.kind] = i
		}
	}
	sort.SliceStable(hs, func(i, j int) bool { return hs[i].start < hs[j].start })
	var spans []hit
	for _, h := range hs {
		if n := len(spans) - 1; n >= 0 && h.start < spans[n].end {
			spans[n].end = max(spans[n].end, h.end)
			if rank[h.kind] < rank[spans[n].kind] {
				spans[n].kind = h.kind
			}
			continue
		}
		spans = append(spans, h)
	}
	var b strings.Builder
	var kinds []string
	at := 0
	for _, h := range spans {
		b.WriteString(s[at:h.start] + "{redacted}")
		at = h.end
		if !slices.Contains(kinds, h.kind) {
			kinds = append(kinds, h.kind)
		}
	}
	b.WriteString(s[at:])
	slices.SortStableFunc(kinds, func(a, b string) int { return rank[a] - rank[b] })
	return b.String(), kinds
}

// guard scans serialised JSON and fails closed on anything that looks
// personal. Each JSON value is checked on its own. The error names the JSON
// path, never the value.
func (d detector) guard(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return errors.New("redaction guard: cannot read payload; nothing was sent")
	}
	return walk("", v, func(path, s string) error {
		if hs := d.find(path, s); len(hs) > 0 {
			return fmt.Errorf("redaction guard: %s found in %s; nothing was sent", hs[0].kind, path)
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

// phoneSpans returns the phones in s: digit runs of 9+ digits. Dates and
// times are cut out first, so they do not count, but the digit runs directly
// around a cut date (joined through phone separators) are added up: 9+ digits
// there make the runs and the date one phone ("555-0100 2026-10-11 199").
// In URLs each path segment is checked on its own, so "/2026/10/03/1" is not one number.
func phoneSpans(s string, isURL bool) [][]int {
	var out [][]int
	type run struct{ start, end, digits int }
	search := func(a, b int) []run {
		var rs []run
		for _, m := range phoneRe.FindAllStringIndex(s[a:b], -1) {
			r := run{a + m[0], a + m[1], 0}
			for _, c := range s[r.start:r.end] {
				if unicode.IsDigit(c) {
					r.digits++
				}
			}
			if r.digits >= 9 {
				out = append(out, []int{r.start, r.end})
			}
			rs = append(rs, r)
		}
		return rs
	}
	region := func(a, b int) {
		var cuts [][]int
		for _, m := range dateTimeRe.FindAllStringIndex(s[a:b], -1) {
			if m0, m1 := a+m[0], a+m[1]; !digitAround(s, m0, m1) {
				cuts = append(cuts, []int{m0, m1})
			}
		}
		// runs[i] are the digit runs between cut i-1 and cut i.
		runs := make([][]run, len(cuts)+1)
		from := a
		for i, c := range cuts {
			runs[i] = search(from, c[0])
			from = c[1]
		}
		runs[len(cuts)] = search(from, b)
		for i, c := range cuts {
			start, end, digits := c[0], c[1], 0
			if rs := runs[i]; len(rs) > 0 && phoneSepRe.MatchString(s[rs[len(rs)-1].end:c[0]]) {
				start, digits = rs[len(rs)-1].start, rs[len(rs)-1].digits
			}
			if rs := runs[i+1]; len(rs) > 0 && phoneSepRe.MatchString(s[c[1]:rs[0].start]) {
				end, digits = rs[0].end, digits+rs[0].digits
			}
			if digits >= 9 {
				out = append(out, []int{start, end})
			}
		}
	}
	if !isURL {
		region(0, len(s))
		return out
	}
	a := 0
	for _, seg := range strings.Split(s, "/") {
		region(a, a+len(seg))
		a += len(seg) + 1
	}
	return out
}

// digitAround reports whether a digit or ':' sits directly around s[a:b], so the
// date or time there is part of a longer number ("12:34:56:78", "fe80::12:34").
func digitAround(s string, a, b int) bool {
	before, _ := utf8.DecodeLastRuneInString(s[:a])
	after, _ := utf8.DecodeRuneInString(s[b:])
	edge := func(r rune) bool { return unicode.IsDigit(r) || r == ':' }
	return a > 0 && edge(before) || b < len(s) && edge(after)
}

func ipSpans(s string) [][]int {
	var out [][]int
	for _, m := range ipv4Re.FindAllStringIndex(s, -1) {
		if net.ParseIP(s[m[0]:m[1]]) != nil {
			out = append(out, m)
		}
	}
	for _, m := range hexRunRe.FindAllStringIndex(s, -1) {
		tok := s[m[0]:m[1]]
		// Try the token as is ("::1") and without surrounding punctuation ("IP:2001:db8::1.").
		trimmed := strings.Trim(tok, ":.")
		lead := len(tok) - len(strings.TrimLeft(tok, ":."))
		for _, c := range [][]int{m, {m[0] + lead, m[0] + lead + len(trimmed)}} {
			if ip := s[c[0]:c[1]]; strings.Count(ip, ":") >= 2 && net.ParseIP(ip) != nil {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

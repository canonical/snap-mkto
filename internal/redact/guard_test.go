// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package redact

import (
	"slices"
	"strings"
	"testing"

	"github.com/canonical/snap-mkto/internal/marketo"
)

var jane = marketo.Lead{"firstName": "Jane", "lastName": "Doe", "email": "jane.doe@example.com"}

const (
	kEmail = "email address"
	kPhone = "phone number"
	kIP    = "IP address"
	kFirst = "lead's first name"
	kLast  = "lead's last name"
	kFull  = "lead's full name"
	kLocal = "lead's email local part"
)

// TestRedactValue replaces exactly the detected span and keeps the rest of the value.
func TestRedactValue(t *testing.T) {
	for _, tc := range []struct {
		name, path, in, value, want string // value must not survive; want "" means unchanged
		kinds                       []string
	}{
		{"clean", "events[0].asset", "November Newsletter", "", "", nil},
		{"ISO date is not a phone", "events[0].date", "2026-10-03T09:00:00Z", "", "", nil},
		{"eight digits pass", "asset", "Webinar 2026-11-05", "", "", nil},
		{"email", "lead.company", "ask jane.d@example.org", "jane.d@example.org", "ask {redacted}", []string{kEmail}},
		{"phone with separators", "events[0].asset", "call +1 (555) 010-0199", "+1 (555) 010-0199", "call {redacted}", []string{kPhone}},
		{"phone dotted", "asset", "555.010.0199", "555.010.0199", "{redacted}", []string{kPhone}},
		{"IPv4", "events[1].asset", "Webinar 192.0.2.55 recap", "192.0.2.55", "Webinar {redacted} recap", []string{kIP}},
		{"IPv6", "url", "see 2001:db8::1 now", "2001:db8::1", "see {redacted} now", []string{kIP}},
		{"first name", "events[0].asset", "Follow-up for Jane", "Jane", "Follow-up for {redacted}", []string{kFirst}},
		{"first name any case", "asset", "FOLLOW-UP FOR JANE", "JANE", "FOLLOW-UP FOR {redacted}", []string{kFirst}},
		{"first name inside a longer word passes", "asset", "Janet's webinar", "", "", nil},
		{"last name", "lead.company", "Doe Holdings", "Doe", "{redacted} Holdings", []string{kLast}},
		// B: digits and _ are boundaries.
		{"name joined by underscores", "asset", "Followup_Jane_Doe", "Jane", "Followup_{redacted}_{redacted}", []string{kFirst, kLast}},
		{"last name between underscores", "asset", "promo_doe_2026", "doe", "promo_{redacted}_2026", []string{kLast}},
		{"name in a URL path", "url", "https://www.example.com/welcome/jane_doe/", "jane", "https://www.example.com/welcome/{redacted}_{redacted}/", []string{kFirst, kLast}},
		{"name followed by digits", "asset", "jane2026 promo", "jane", "{redacted}2026 promo", []string{kFirst}},
		// D: Unicode and obfuscated emails.
		{"unicode local part", "asset", "zoë@example.org", "zoë@example.org", "{redacted}", []string{kEmail}},
		{"unicode domain", "asset", "bob@exämple.org", "bob@exämple.org", "{redacted}", []string{kEmail}},
		{"fullwidth at", "asset", "bob.smith＠example.org", "bob.smith＠example.org", "{redacted}", []string{kEmail}},
		{"small at", "asset", "bob.smith﹫example.org", "bob.smith﹫example.org", "{redacted}", []string{kEmail}},
		{"(at)", "asset", "write to bob.smith(at)example.org", "bob.smith", "write to {redacted}", []string{kEmail}},
		{"[at] [dot]", "asset", "bob.smith [AT] example [dot] org", "bob.smith", "{redacted}", []string{kEmail}},
		// E: Unicode digits and separators.
		{"no-break spaces", "asset", "+44 20 7946 0958", "7946", "{redacted}", []string{kPhone}},
		{"en dashes", "asset", "555–010–0199", "0199", "{redacted}", []string{kPhone}},
		{"slash", "asset", "Tel 030/12345678", "12345678", "Tel {redacted}", []string{kPhone}},
		{"fullwidth digits", "asset", "５５５０１００１９９", "５５５", "{redacted}", []string{kPhone}},
		{"percent-encoded at", "asset", "bob%40example.org", "bob%40example.org", "{redacted}", []string{kEmail}},
		{"phone in one URL segment", "url", "https://www.example.com/call/+44-20-7946-0958", "7946", "https://www.example.com/call/{redacted}", []string{kPhone}},
		{"URL segments are not joined into a phone", "events[0].url", "https://www.example.com/p/12345/67890", "", "", nil},
		{"spelled-out email", "asset", "write to bob at example dot org", "bob", "write to {redacted}", []string{kEmail}},
		{"spelled-out email with dashes", "asset", "bob-smith-at-example-dot-org", "bob-smith", "{redacted}", []string{kEmail}},
		{"at without dot passes", "asset", "look at this webinar", "", "", nil},
		{"at without dot in a slug passes", "url", "https://www.example.com/what-is-confidential-computing", "", "", nil},
		{"joined name", "asset", "Welcome JaneDoe", "JaneDoe", "Welcome {redacted}", []string{kFull}},
		{"joined name in a slug", "url", "https://www.example.com/welcome-janedoe-to-ubuntu-pro", "janedoe", "https://www.example.com/welcome-{redacted}-to-ubuntu-pro", []string{kFull}},
		{"joined name reversed", "asset", "doejane", "doejane", "{redacted}", []string{kFull}},
		// F: IPv6 next to punctuation.
		{"IPv6 then a full stop", "asset", "from 2001:db8::1.", "2001:db8::1", "from {redacted}.", []string{kIP}},
		{"IPv6 after a colon", "asset", "IP:2001:db8::1", "2001:db8::1", "IP:{redacted}", []string{kIP}},
		// 2026-11-08: dates and times are not phones (a real-instance false positive).
		{"date-time in an asset name", "events[0].asset", "Validation Report 2026-10-11 01:20:10.Email 1", "", "", nil},
		{"real-instance asset shape", "events[0].asset", "github-actions-marketo-testing.Validation Report 2026-10-11 01:20:10.Email 1", "", "", nil},
		{"phone next to a date", "events[0].asset", "Call 555-010-0199 after 2026-10-11 09:00", "555-010-0199", "Call {redacted} after 2026-10-11 09:00", []string{kPhone}},
		{"time and date are not joined", "asset", "Standup 10:30 2026-11-05 recap", "", "", nil},
		{"two dates are not joined", "asset", "Webinar 2026-11-05 2026-11-06", "", "", nil},
		{"date-time with zone", "asset", "Export 2026-10-11T01:20:10.123+02:00 done", "", "", nil},
		// A phone split around a date or time is still a phone (security review, 2026-11-08).
		{"phone around a date", "asset", "555-0100 2026-10-11 199", "0100", "{redacted}", []string{kPhone}},
		{"phone with spaces around a date", "asset", "555 010 2026-10-11 0199", "0199", "{redacted}", []string{kPhone}},
		{"year outside 19xx/20xx is not a date", "asset", "1555-01-01 0199", "0199", "{redacted}", []string{kPhone}},
		{"phone around a date in a URL segment", "url", "https://www.example.com/c/555-0100-2026-10-11-199", "0100", "https://www.example.com/c/{redacted}", []string{kPhone}},
		{"phone around a time", "asset", "555-0100 10:30 199", "0100", "{redacted}", []string{kPhone}},
		{"international phone", "asset", "+44 20 7946 0958", "7946", "{redacted}", []string{kPhone}},
		{"email inside an asset keeps the rest", "events[0].asset", "Invite for bob.smith@example.org (EMEA)", "bob.smith", "Invite for {redacted} (EMEA)", []string{kEmail}},
		{"IPv6 in an asset", "events[0].asset", "Webinar 2001:db8::7 replay", "2001:db8::7", "Webinar {redacted} replay", []string{kIP}},
	} {
		got, kinds := newDetector(jane).redact(tc.path, tc.in)
		want := tc.want
		if want == "" {
			want = tc.in
		}
		if got != want {
			t.Errorf("%s: got %q, want %q", tc.name, got, want)
		}
		if !slices.Equal(kinds, tc.kinds) {
			t.Errorf("%s: kinds %q, want %q", tc.name, kinds, tc.kinds)
		}
		if tc.value != "" && strings.Contains(strings.ToLower(got), strings.ToLower(tc.value)) {
			t.Errorf("%s: the value survived", tc.name)
		}
		if hits := newDetector(jane).find(tc.path, got); len(hits) > 0 {
			t.Errorf("%s: the redacted value still has a hit (%s)", tc.name, hits[0].kind)
		}
	}
}

// TestGuardFailsClosed: the final guard still names the kind and JSON path, never the value.
func TestGuardFailsClosed(t *testing.T) {
	for _, tc := range []struct{ name, json, value, want string }{
		{"clean", `{"events":[{"id":"E1","date":"2026-10-03T09:00:00Z","type":"Open Email","asset":"November Newsletter"}]}`, "", ""},
		{"ISO dates are separate values", `{"events":[{"date":"2026-10-03T09:00:00Z"},{"date":"2026-10-04T10:00:00Z"},{"date":"2026-10-05T11:00:00Z"}]}`, "", ""},
		{"email", `{"lead":{"company":"ask jane.d@example.org"}}`, "jane.d@example.org", "redaction guard: email address found in lead.company; nothing was sent"},
		{"IPv4", `{"events":[{"asset":"x"},{"asset":"Webinar 192.0.2.55 recap"}]}`, "192.0.2.55", "redaction guard: IP address found in events[1].asset; nothing was sent"},
	} {
		err := newDetector(jane).guard([]byte(tc.json))
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: unexpected error: %v", tc.name, err)
		case tc.want != "" && (err == nil || err.Error() != tc.want):
			t.Errorf("%s: error = %v, want %q", tc.name, err, tc.want)
		case err != nil && strings.Contains(err.Error(), tc.value):
			t.Errorf("%s: error leaks the value", tc.name)
		}
	}
}

func TestRedactShortNamesIgnored(t *testing.T) {
	l := marketo.Lead{"firstName": "Al", "lastName": "Ng", "email": "al@example.com"}
	if got, kinds := newDetector(l).redact("asset", "Al Ng: AI at scale"); got != "Al Ng: AI at scale" || kinds != nil {
		t.Fatalf("names shorter than 3 must not be checked: %q %v", got, kinds)
	}
}

func TestRedactEmailLocalPart(t *testing.T) {
	for _, tc := range []struct{ email, in, want string }{
		{"jd.ops@example.com", "https://www.example.com/team/jd.ops", "https://www.example.com/team/{redacted}"},
		{"jd4821@example.com", "https://www.example.com/u/jd4821_ref", "https://www.example.com/u/{redacted}_ref"},
	} {
		l := marketo.Lead{"firstName": "Jane", "lastName": "Doe", "email": tc.email}
		got, kinds := newDetector(l).redact("url", tc.in)
		if got != tc.want || !slices.Equal(kinds, []string{kLocal}) {
			t.Errorf("%s: got %q %v, want %q", tc.email, got, kinds, tc.want)
		}
	}
}

func TestRedactRoleLocalPartSkipped(t *testing.T) {
	l := marketo.Lead{"firstName": "Jane", "lastName": "Doe", "email": "Marketing@example.com"}
	if got, kinds := newDetector(l).redact("lead.role", "senior marketing leader"); got != "senior marketing leader" || kinds != nil {
		t.Fatalf("role mailbox local part must not be checked: %q %v", got, kinds)
	}
}

// TestRedactMarkerIsNotAName: a lead named Red Acted must not match the {redacted} marker itself.
func TestRedactMarkerIsNotAName(t *testing.T) {
	l := marketo.Lead{"firstName": "Red", "lastName": "Acted", "email": "r.acted@example.com"}
	d := newDetector(l)
	if hs := d.find("asset", "Call {redacted} today"); len(hs) > 0 {
		t.Errorf("the marker counts as %s", hs[0].kind)
	}
	if got, kinds := d.redact("asset", "Hi RedActed, call 555-010-0199"); got != "Hi {redacted}, call {redacted}" || !slices.Equal(kinds, []string{kPhone, kFull}) {
		t.Errorf("got %q %v", got, kinds)
	}
	l["email"] = "redacted@example.com"
	p, err := Build(l, []marketo.Activity{act(1, 10, at(1, 9, 0), "Call 555-010-0199", nil)}, types, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(p.JSON()); !strings.Contains(got, `"asset":"Call {redacted}"`) {
		t.Errorf("payload %s", got)
	}
}

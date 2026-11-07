// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package redact

import (
	"strings"
	"testing"

	"github.com/canonical/snap-mkto/internal/marketo"
)

var jane = marketo.Lead{"firstName": "Jane", "lastName": "Doe", "email": "jane.doe@example.com"}

func TestGuard(t *testing.T) {
	for _, tc := range []struct {
		name, json, value, want string // want "" means the guard passes
	}{
		{"clean", `{"events":[{"id":"E1","date":"2026-10-03T09:00:00Z","type":"Open Email","asset":"November Newsletter"}]}`, "", ""},
		{"ISO date is not a phone", `{"date":"2026-10-03T09:00:00Z"}`, "", ""},
		{"eight digits pass", `{"asset":"Webinar 2026-11-05"}`, "", ""},
		{"email", `{"lead":{"company":"ask jane.d@example.org"}}`, "jane.d@example.org", "redaction guard: email address found in lead.company; nothing was sent"},
		{"phone with separators", `{"events":[{"asset":"call +1 (555) 010-0199"}]}`, "+1 (555) 010-0199", "redaction guard: phone number found in events[0].asset; nothing was sent"},
		{"phone dotted", `{"asset":"555.010.0199"}`, "555.010.0199", "redaction guard: phone number found in asset; nothing was sent"},
		{"IPv4", `{"events":[{"asset":"x"},{"asset":"Webinar 192.0.2.55 recap"}]}`, "192.0.2.55", "redaction guard: IP address found in events[1].asset; nothing was sent"},
		{"IPv6", `{"url":"see 2001:db8::1 now"}`, "2001:db8::1", "redaction guard: IP address found in url; nothing was sent"},
		{"first name", `{"events":[{"asset":"Follow-up for Jane"}]}`, "Jane", "redaction guard: lead's first name found in events[0].asset; nothing was sent"},
		{"first name any case", `{"asset":"FOLLOW-UP FOR JANE"}`, "JANE", "redaction guard: lead's first name found in asset; nothing was sent"},
		{"first name inside a longer word passes", `{"asset":"Janet's webinar"}`, "", ""},
		{"last name", `{"lead":{"company":"Doe Holdings"}}`, "Doe", "redaction guard: lead's last name found in lead.company; nothing was sent"},
		// B: digits and _ are boundaries.
		{"name joined by underscores", `{"asset":"Followup_Jane_Doe"}`, "Jane", "redaction guard: lead's first name found in asset; nothing was sent"},
		{"last name between underscores", `{"asset":"promo_doe_2026"}`, "doe", "redaction guard: lead's last name found in asset; nothing was sent"},
		{"name in a URL path", `{"url":"https://www.example.com/welcome/jane_doe/"}`, "jane", "redaction guard: lead's first name found in url; nothing was sent"},
		{"name followed by digits", `{"asset":"jane2026 promo"}`, "jane", "redaction guard: lead's first name found in asset; nothing was sent"},
		// D: Unicode and obfuscated emails.
		{"unicode local part", `{"asset":"zoë@example.org"}`, "zoë@example.org", "redaction guard: email address found in asset; nothing was sent"},
		{"unicode domain", `{"asset":"bob@exämple.org"}`, "bob@exämple.org", "redaction guard: email address found in asset; nothing was sent"},
		{"fullwidth at", `{"asset":"bob.smith＠example.org"}`, "bob.smith＠example.org", "redaction guard: email address found in asset; nothing was sent"},
		{"small at", `{"asset":"bob.smith﹫example.org"}`, "bob.smith﹫example.org", "redaction guard: email address found in asset; nothing was sent"},
		{"(at)", `{"asset":"write to bob.smith(at)example.org"}`, "bob.smith", "redaction guard: email address found in asset; nothing was sent"},
		{"[at] [dot]", `{"asset":"bob.smith [AT] example [dot] org"}`, "bob.smith", "redaction guard: email address found in asset; nothing was sent"},
		// E: Unicode digits and separators.
		{"no-break spaces", "{\"asset\":\"+44\u00a020\u00a07946\u00a00958\"}", "7946", "redaction guard: phone number found in asset; nothing was sent"},
		{"en dashes", `{"asset":"555–010–0199"}`, "0199", "redaction guard: phone number found in asset; nothing was sent"},
		{"slash", `{"asset":"Tel 030/12345678"}`, "12345678", "redaction guard: phone number found in asset; nothing was sent"},
		{"fullwidth digits", `{"asset":"５５５０１００１９９"}`, "５５５", "redaction guard: phone number found in asset; nothing was sent"},
		{"ISO dates are separate values", `{"events":[{"date":"2026-10-03T09:00:00Z"},{"date":"2026-10-04T10:00:00Z"},{"date":"2026-10-05T11:00:00Z"}]}`, "", ""},
		{"percent-encoded at", `{"asset":"bob%40example.org"}`, "bob%40example.org", "redaction guard: email address found in asset; nothing was sent"},
		{"phone in one URL segment", `{"url":"https://www.example.com/call/+44-20-7946-0958"}`, "7946", "redaction guard: phone number found in url; nothing was sent"},
		{"URL segments are not joined into a phone", `{"events":[{"url":"https://www.example.com/p/12345/67890"}]}`, "", ""},
		{"spelled-out email", `{"asset":"write to bob at example dot org"}`, "bob", "redaction guard: email address found in asset; nothing was sent"},
		{"spelled-out email with dashes", `{"asset":"bob-smith-at-example-dot-org"}`, "bob-smith", "redaction guard: email address found in asset; nothing was sent"},
		{"at without dot passes", `{"asset":"look at this webinar"}`, "", ""},
		{"at without dot in a slug passes", `{"url":"https://www.example.com/what-is-confidential-computing"}`, "", ""},
		{"joined name", `{"asset":"Welcome JaneDoe"}`, "JaneDoe", "redaction guard: lead's full name found in asset; nothing was sent"},
		{"joined name in a slug", `{"url":"https://www.example.com/welcome-janedoe-to-ubuntu-pro"}`, "janedoe", "redaction guard: lead's full name found in url; nothing was sent"},
		{"joined name reversed", `{"asset":"doejane"}`, "doejane", "redaction guard: lead's full name found in asset; nothing was sent"},
		// F: IPv6 next to punctuation.
		{"IPv6 then a full stop", `{"asset":"from 2001:db8::1."}`, "2001:db8::1", "redaction guard: IP address found in asset; nothing was sent"},
		{"IPv6 after a colon", `{"asset":"IP:2001:db8::1"}`, "2001:db8::1", "redaction guard: IP address found in asset; nothing was sent"},
	} {
		err := guard([]byte(tc.json), jane)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: unexpected error: %v", tc.name, err)
		case tc.want != "" && (err == nil || err.Error() != tc.want):
			t.Errorf("%s: error = %v, want %q", tc.name, err, tc.want)
		case err != nil && strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.value)):
			t.Errorf("%s: error leaks the value", tc.name)
		}
	}
}

func TestGuardShortNamesIgnored(t *testing.T) {
	l := marketo.Lead{"firstName": "Al", "lastName": "Ng", "email": "al@example.com"}
	if err := guard([]byte(`{"asset":"Al Ng: AI at scale"}`), l); err != nil {
		t.Fatalf("names shorter than 3 must not be checked: %v", err)
	}
}

func TestGuardCompanyQuery(t *testing.T) {
	q := Payload{lead: profile{Company: "Jane's Bakery"}}.Company()
	err := guard(q.JSON(), jane)
	if err == nil || err.Error() != "redaction guard: lead's first name found in company; nothing was sent" {
		t.Fatalf("error = %v", err)
	}
}

func TestGuardEmailLocalPart(t *testing.T) {
	l := marketo.Lead{"firstName": "Jane", "lastName": "Doe", "email": "jd.ops@example.com"}
	err := guard([]byte(`{"url":"https://www.example.com/team/jd.ops"}`), l)
	if err == nil || err.Error() != "redaction guard: lead's email local part found in url; nothing was sent" {
		t.Fatalf("error = %v", err)
	}
}

func TestGuardEmailLocalPartBoundary(t *testing.T) {
	l := marketo.Lead{"firstName": "Jane", "lastName": "Doe", "email": "jd4821@example.com"}
	err := guard([]byte(`{"url":"https://www.example.com/u/jd4821_ref"}`), l)
	if err == nil || err.Error() != "redaction guard: lead's email local part found in url; nothing was sent" {
		t.Fatalf("error = %v", err)
	}
}

func TestGuardRoleLocalPartSkipped(t *testing.T) {
	l := marketo.Lead{"firstName": "Jane", "lastName": "Doe", "email": "Marketing@example.com"}
	if err := guard([]byte(`{"role":"senior marketing leader"}`), l); err != nil {
		t.Fatalf("role mailbox local part must not be checked: %v", err)
	}
}

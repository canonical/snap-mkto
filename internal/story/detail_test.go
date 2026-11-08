// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package story

import (
	"fmt"
	"strings"
	"testing"

	"github.com/canonical/snap-mkto/internal/marketo"
)

func TestDetailTable(t *testing.T) {
	for _, c := range []struct {
		name, typ, primary string
		attrs              map[string]any
		want               string
	}{
		{"score strings", "Change Score", "Behavior Score", map[string]any{"Change Value": "+25", "Old Value": "60", "New Value": "85", "Reason": "Filled out form"}, "+25 (60→85) Filled out form"},
		{"score numbers", "Change Score", "", map[string]any{"Change Value": float64(25), "Old Value": float64(60), "New Value": float64(85)}, "+25 (60→85)"},
		{"score negative", "change score", "", map[string]any{"Change Value": "-10", "Old Value": float64(85), "New Value": "75", "Reason": "Email bounced"}, "-10 (85→75) Email bounced"},
		{"score fraction", "Change Score", "", map[string]any{"Change Value": 2.5}, "+2.5"},
		{"score unsigned string", "Change Score", "", map[string]any{"Change Value": "5"}, "+5"},
		{"score no old", "Change Score", "", map[string]any{"Change Value": "+5", "New Value": "5"}, "+5"},
		{"score missing", "Change Score", "", map[string]any{}, ""},
		{"score wrong type", "Change Score", "", map[string]any{"Change Value": map[string]any{"x": 1}, "Reason": true}, "true"},
		{"change", "Change Data Value", "Lead Status", map[string]any{"Old Value": "Nurture", "New Value": "MQL", "Reason": "r", "Source": "Campaign"}, "Nurture→MQL"},
		{"change numbers", "Change Data Value", "Number of Employees", map[string]any{"Old Value": float64(50), "New Value": float64(1200)}, "50→1200"},
		{"change first set", "Change Data Value", "Lead Status", map[string]any{"New Value": "MQL"}, "→MQL"},
		{"change missing", "Change Data Value", "Lead Status", map[string]any{}, ""},
		{"progression", "Change Status in Progression", "Example Webinar 2026", map[string]any{"Old Status": "Registered", "New Status": "Attended", "Success": "true"}, "→ Attended ✓ success"},
		{"progression bool", "Change Status in Progression", "", map[string]any{"New Status": "Attended", "Success": true}, "→ Attended ✓ success"},
		{"progression no success", "Change Status in Progression", "", map[string]any{"New Status": "Registered", "Success": "false"}, "→ Registered"},
		{"progression missing", "Change Status in Progression", "", map[string]any{"Success": true}, ""},
		{"click", "Click Email", "Example Welcome Email", map[string]any{"Link": "https://www.example.com/whitepaper", "Device": "Computer"}, "https://www.example.com/whitepaper"},
		{"click query dropped", "Click Email", "x", map[string]any{"Link": "https://u:p@www.example.com/wp?utm_source=secretcampaign&mkt_tok=abc#frag"}, "https://www.example.com/wp"},
		{"click not a url", "Click Email", "x", map[string]any{"Link": "not a link"}, ""},
		{"click missing", "Click Email", "", map[string]any{}, ""},
		{"bounce", "Email Bounced", "", map[string]any{"Category": "hardbounce", "Details": "550 5.1.1 mailbox unknown"}, "hardbounce: 550 5.1.1 mailbox unknown"},
		{"soft bounce", "Email Bounced Soft", "", map[string]any{"Category": "softbounce", "Details": "452 4.2.2 mailbox full"}, "softbounce: 452 4.2.2 mailbox full"},
		{"bounce category only", "Email Bounced", "", map[string]any{"Category": float64(1)}, "1"},
		{"form", "Fill Out Form", "Contact Us Form", map[string]any{"Webpage ID": "15"}, ""},
		{"unknown", "Something Else", "x", map[string]any{"Link": "https://www.example.com/"}, ""},
		{"nil attrs", "Change Score", "", nil, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := Detail(c.typ, marketo.Activity{Primary: c.primary, Attributes: c.attrs}); got != c.want {
				t.Errorf("Detail = %q, want %q", got, c.want)
			}
		})
	}
}

func TestChangeField(t *testing.T) {
	if got := ChangeField(marketo.Activity{Primary: " Lead Status "}); got != "Lead Status" {
		t.Errorf("ChangeField = %q", got)
	}
}

// TestDetailNeverReadsPII: every detailer, fed the attributes it reads plus the PII
// ones, never echoes a PII value.
func TestDetailNeverReadsPII(t *testing.T) {
	pii := map[string]any{
		"Client IP Address": "192.0.2.10",
		"User Agent":        "Mozilla/5.0 (X11; Linux x86_64) ExampleBrowser/1.0",
		"Form Fields":       `{"Email":"jane.doe@example.com"}`,
		"Query Parameters":  "utm_source=secretcampaign",
		"Referrer URL":      "https://referrer.example.org/private",
	}
	for typ := range detailers {
		attrs := map[string]any{
			"Change Value": "+1", "Old Value": "a", "New Value": "b", "Reason": "r", "Old Status": "x", "New Status": "y",
			"Success": "true", "Link": "https://www.example.com/?mkt_tok=secrettoken", "Category": "c", "Details": "d",
		}
		for k, v := range pii {
			attrs[k] = v
		}
		got := Detail(typ, marketo.Activity{Primary: "p", Attributes: attrs})
		if got == "" {
			t.Errorf("%s: empty detail; the test no longer exercises it", typ)
		}
		for k, v := range pii {
			if strings.Contains(got, v.(string)) || strings.Contains(got, "192.0.2") || strings.Contains(got, "secrettoken") {
				t.Errorf("%s: detail contains the %s value", typ, k)
			}
		}
	}
}

func TestDetailShortensLongAndTokenValues(t *testing.T) {
	token := strings.Repeat("Example0Token.", 8) // invented: 112 chars, no spaces, letters and digits
	long := strings.Repeat("2026-09-10 08:04:20: New conversion URL: https://example.com/x ", 40)
	for _, c := range []struct{ old, new, want string }{
		{token, token + "b", fmt.Sprintf("‹%d-char token›→‹%d-char token›", len(token), len(token)+1)},
		{"Truncated Because of Excessive Length", long, "Truncated Because of Excessive Length→" + string([]rune(long)[:maxValue-1]) + "…"},
		{"Nurture", "MQL", "Nurture→MQL"},
		{"https://www.example.com/a/very/long/path/that/goes/on/and/on/and/on", "x", "https://www.example.com/a/very/long/path/that/goes/on/and/on/and/on→x"},
	} {
		got := Detail("Change Data Value", marketo.Activity{Primary: "Session Id", Attributes: map[string]any{"Old Value": c.old, "New Value": c.new}})
		if got != c.want {
			t.Errorf("Detail(%.20q…) =\n %q\nwant\n %q", c.old, got, c.want)
		}
	}
}

func TestCut(t *testing.T) {
	for _, c := range []struct {
		in   string
		n    int
		want string
	}{
		{"", 5, ""},
		{"short", 5, "short"},
		{"longer", 5, "long…"},
		{"ééééééé", 4, "ééé…"}, // runes, not bytes
	} {
		if got := Cut(c.in, c.n); got != c.want {
			t.Errorf("Cut(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
}

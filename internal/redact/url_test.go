// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package redact

import "testing"

func TestScrubURL(t *testing.T) {
	for _, tc := range []struct{ name, raw, want string }{
		{"plain", "https://www.example.com/pricing", "https://www.example.com/pricing"},
		{"mkt_tok query", "https://www.example.com/pricing?mkt_tok=MDAwLUFCQy0xMjMAAAGExampleTok", "https://www.example.com/pricing"},
		{"prefilled form email in query", "https://www.example.com/contact?email=jane.doe%40example.com&first=Jane", "https://www.example.com/contact"},
		{"fragment", "https://www.example.com/docs#jane", "https://www.example.com/docs"},
		{"email in path", "https://www.example.com/u/jane.doe@example.com/prefs", "https://www.example.com/u/{redacted}/prefs"},
		{"escaped email in path", "https://www.example.com/u/jane.doe%40example.com", "https://www.example.com/u/{redacted}"},
		{"token segment", "https://www.example.com/r/AbCdEfGhIjKlMnOpQrStUvWx/next", "https://www.example.com/r/{redacted}/next"},
		{"23 chars kept", "https://www.example.com/r/AbCdEfGhIjKlMnOpQrStUvW", "https://www.example.com/r/AbCdEfGhIjKlMnOpQrStUvW"},
		{"userinfo", "https://jane:s3cret@www.example.com/a", "https://www.example.com/a"},
		{"port kept", "https://www.example.com:8443/a", "https://www.example.com:8443/a"},
		{"no path", "https://www.example.com", "https://www.example.com"},
		{"no scheme", "www.example.com/pricing", ""},
		{"relative", "/pricing?utm_source=example", ""},
		{"mailto", "mailto:jane.doe@example.com", ""},
		{"parse failure", "https://www.example.com/%zz", ""},
		{"empty", "", ""},
		{"double-escaped email", "https://www.example.com/u/bob.smith%2540example.org/x", "https://www.example.com/u/{redacted}/x"},
		{"double-escaped short email", "https://www.example.com/u/jd%2540example.com", "https://www.example.com/u/{redacted}"},
		{"base64 email padded", "https://www.example.com/u/Ym9iLnNtaXRoQGV4YW1wbGUub3Jn==", "https://www.example.com/u/{redacted}"},
		{"base64 short email", "https://www.example.com/u/Ym9iQGV4YW1wbGUub3Jn", "https://www.example.com/u/{redacted}"},
		{"jsessionid", "https://www.example.com/pricing;jsessionid=1A2B3C4D5E6F7G8H9I0J1K2L3M", "https://www.example.com/{redacted}"},
		{"token file name", "https://www.example.com/r/AbCdEfGhIjKlMnOpQrStUvWx.html", "https://www.example.com/r/{redacted}"},
		{"fullwidth at", "https://www.example.com/u/bob＠example.org", "https://www.example.com/u/{redacted}"},
		// Round 2: escapes that never fully decode, base64 at an offset, readable slugs.
		{"invalid escape after an email", "https://www.example.com/u/jd%2540example.com%25zz", "https://www.example.com/u/{redacted}"},
		{"four levels of escaping", "https://www.example.com/u/jd%25252540example.com", "https://www.example.com/u/{redacted}"},
		{"base64 after a dash", "https://www.example.com/u/x-Ym9iQGV4YW1wbGUub3Jn", "https://www.example.com/u/{redacted}"},
		{"base64 after a plus", "https://www.example.com/u/ab+Ym9iQGV4YW1wbGUub3Jn", "https://www.example.com/u/{redacted}"},
		{"uuid", "https://www.example.com/order/550e8400-e29b-41d4-a716-446655440000", "https://www.example.com/order/{redacted}"},
		{"mixed-case token", "https://www.example.com/r/abc123DEF456ghi789JKL012mno", "https://www.example.com/r/{redacted}"},
		{"32 hex chars", "https://www.example.com/r/0123456789abcdef0123456789abcdef", "https://www.example.com/r/{redacted}"},
		// Round 3: mixed base64 alphabets, hex, spelled-out emails.
		{"base64 with _ and +", "https://www.example.com/r/a_b+Ym9iQGV4YW1wbGUub3Jn", "https://www.example.com/r/{redacted}"},
		{"base64 with - and +", "https://www.example.com/r/x-y+Ym9iQGV4YW1wbGUub3Jn", "https://www.example.com/r/{redacted}"},
		{"base64 with + then -", "https://www.example.com/r/ab+cd-Ym9iQGV4YW1wbGUub3Jn", "https://www.example.com/r/{redacted}"},
		{"hex email in hyphen pieces", "https://www.example.com/r/6b696d4061636d6-52e636f6d", "https://www.example.com/r/{redacted}"},
		{"spelled-out email", "https://www.example.com/u/bob-smith-at-example-dot-org", "https://www.example.com/u/{redacted}"},
		{"at without dot kept", "https://www.example.com/blog/look-at-this", "https://www.example.com/blog/look-at-this"},
		{"long slug kept", "https://www.example.com/blog/how-to-deploy-kubernetes-on-ubuntu", "https://www.example.com/blog/how-to-deploy-kubernetes-on-ubuntu"},
		{"slug kept 2", "https://www.example.com/what-is-confidential-computing", "https://www.example.com/what-is-confidential-computing"},
		{"slug kept 3", "https://www.example.com/2026-state-of-open-source-report", "https://www.example.com/2026-state-of-open-source-report"},
		{"slug with .html kept", "https://www.example.com/blog/long-slug.html", "https://www.example.com/blog/long-slug.html"},
		{"slug kept", "https://www.example.com/pricing-and-plans-2026", "https://www.example.com/pricing-and-plans-2026"},
		{"blog slug kept", "https://www.example.com/blog/kubernetes-at-scale", "https://www.example.com/blog/kubernetes-at-scale"},
	} {
		if got := ScrubURL(tc.raw); got != tc.want {
			t.Errorf("%s: ScrubURL(%q) = %q, want %q", tc.name, tc.raw, got, tc.want)
		}
	}
}

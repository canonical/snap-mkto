// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package output

import "testing"

func TestClean(t *testing.T) {
	for in, want := range map[string]string{
		"Café 日本":        "Café 日本",
		"a\x1b]0;x\x07b": "a�]0;x�b",
		"a\u009bb":       "a�b",
		"a\nb\r\tc":      "a�b��c",
		"a\u202eb":       "a�b",
		"a\u200bb\ufeff": "a�b�",
		"a\u2066b\u2069": "a�b�",
	} {
		if got := Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
}

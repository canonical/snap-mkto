// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package redact

import (
	"slices"
	"strings"
	"unicode"
)

// Function keywords, checked in order: the first function that matches wins.
var functions = []struct {
	name     string
	keywords []string
}{
	{"marketing", []string{"marketing"}}, // before engineering: "Platform Marketing Manager"
	{"engineering", []string{"engineer", "engineering", "developer", "devops", "sre", "platform"}},
	{"data", []string{"data", "analytics", "scientist"}},
	{"security", []string{"security"}},
	{"IT", []string{"it", "infrastructure", "sysadmin", "technology"}},
	{"sales", []string{"sales", "account"}},
	{"product", []string{"product"}},
}

// TitleBucket generalises a job title to "<seniority> <function>" so the raw
// title never reaches an LLM. Unknown function or empty title: "unknown role".
func TitleBucket(title string) string {
	words := strings.FieldsFunc(strings.ToLower(title), func(r rune) bool { return !unicode.IsLetter(r) })
	has := func(w ...string) bool {
		return slices.ContainsFunc(words, func(x string) bool { return slices.Contains(w, x) })
	}

	function := ""
	for _, f := range functions {
		if has(f.keywords...) {
			function = f.name
			break
		}
	}
	switch {
	case function == "":
		return "unknown role"
	case has("chief", "vp", "svp", "evp", "president", "head", "director") || slices.ContainsFunc(words, isCxO):
		return "senior " + function + " leader"
	case has("manager", "lead"):
		return "mid-level " + function + " manager"
	}
	return "individual contributor, " + function
}

// isCxO matches C-level abbreviations such as cto, cio, ceo and the literal cxo.
func isCxO(w string) bool { return len(w) == 3 && w[0] == 'c' && w[2] == 'o' }

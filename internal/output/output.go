// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

// Package output renders command results as tables or JSON.
package output

import (
	"encoding/json"
	"io"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"
)

// Clean replaces control characters (ESC, BEL, C1, newlines, tabs) and Unicode format
// characters (Cf: bidi overrides, zero-width) with U+FFFD, so Marketo, web and model text
// can't drive the terminal or hide what it shows.
func Clean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) { // Cf: bidi overrides, zero-width chars
			return '\uFFFD'
		}
		return r
	}, s)
}

// LeadJSON is the --json shape of one lead.
type LeadJSON struct {
	ID     int            `json:"id"`
	Fields map[string]any `json:"fields"`
}

// WriteJSON writes v indented; map keys are sorted by encoding/json, so output is stable.
func WriteJSON(w io.Writer, v any) error {
	e := json.NewEncoder(w)
	e.SetIndent("", "  ")
	return e.Encode(v)
}

// Table writes rows as aligned columns.
func Table(w io.Writer, rows [][]string) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, r := range rows {
		for i, c := range r {
			if i > 0 {
				_, _ = tw.Write([]byte("\t"))
			}
			_, _ = tw.Write([]byte(Clean(c)))
		}
		_, _ = tw.Write([]byte("\n"))
	}
	return tw.Flush()
}

// ActivityJSON is the --json shape of `mkto activity`. Slices are never null.
type ActivityJSON struct {
	Lead     LeadJSON         `json:"lead"`
	Since    time.Time        `json:"since"`
	Events   []EventJSON      `json:"events"`
	Programs []MembershipJSON `json:"programs"`
	Notes    []string         `json:"notes"`
}

// EventJSON is one activity; Date is UTC.
type EventJSON struct {
	ID         int            `json:"id"`
	Date       time.Time      `json:"date"`
	Type       string         `json:"type"`
	Primary    string         `json:"primary"`
	Attributes map[string]any `json:"attributes"`
}

// MembershipJSON is one program membership; Name is empty if it was not looked up,
// and Date (UTC) is omitted when Marketo sent none.
type MembershipJSON struct {
	ID     int       `json:"id"`
	Name   string    `json:"name"`
	Status string    `json:"status"`
	Date   time.Time `json:"date,omitzero"`
}

// ProgramJSON is one program in `mkto programs --json`; CreatedAt is UTC, omitted when unknown.
type ProgramJSON struct {
	ID        int       `json:"id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	Channel   string    `json:"channel"`
	Workspace string    `json:"workspace"`
	CreatedAt time.Time `json:"createdAt,omitzero"`
}

// UsageDayJSON is one day in `mkto quota --json`.
type UsageDayJSON struct {
	Date  string `json:"date"`
	Total int    `json:"total"`
}

// QuotaJSON is the --json shape of `mkto quota`; Percent has one decimal.
type QuotaJSON struct {
	Date      string         `json:"date"`
	Used      int            `json:"used"`
	Limit     int            `json:"limit"`
	Remaining int            `json:"remaining"`
	Percent   float64        `json:"percent"`
	Last7Days []UsageDayJSON `json:"last7days"`
}

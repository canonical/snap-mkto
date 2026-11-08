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
	// Lists is present only with `lead --lists` (then [] when empty).
	Lists *[]ListJSON `json:"lists,omitempty"`
	// LastTouch (UTC) and Touches (counts by activity type name) are the window's touches,
	// in `account` and `leads --activity` only; Email Delivered is not a touch.
	LastTouch time.Time      `json:"lastTouch,omitzero"`
	Touches   map[string]int `json:"touches,omitempty"`
}

// ListJSON is one static list membership; Added (UTC) is omitted when Marketo sent none.
type ListJSON struct {
	ID    int       `json:"id"`
	Added time.Time `json:"added,omitzero"`
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

// EventJSON is one activity; Date is UTC. Detail is the text timeline's detail column
// without its ` via "<campaign>"`: Campaign is the smart campaign's name, when looked up.
type EventJSON struct {
	ID         int            `json:"id"`
	Date       time.Time      `json:"date"`
	Type       string         `json:"type"`
	Primary    string         `json:"primary"`
	PrimaryID  int            `json:"primaryId,omitempty"`
	CampaignID int            `json:"campaignId,omitempty"`
	Campaign   string         `json:"campaign,omitempty"`
	Detail     string         `json:"detail,omitempty"`
	Attributes map[string]any `json:"attributes"`
}

// MembershipJSON is one program membership; Name is empty if it was not looked up,
// and Date (UTC) is omitted when Marketo sent none.
type MembershipJSON struct {
	ID     int       `json:"id"`
	Name   string    `json:"name"`
	Status string    `json:"status"`
	Date   time.Time `json:"date,omitzero"`
	// AcquiredBy: the program that created the lead; Success: the lead reached program success.
	AcquiredBy bool `json:"acquiredBy,omitempty"`
	Success    bool `json:"success,omitempty"`
	// Title and Link: the program's name_tokens and link_tokens values, when it has them.
	Title string `json:"title,omitempty"`
	Link  string `json:"link,omitempty"`
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

// TypeJSON is one activity type in `mkto types --json`. Attributes is never null.
type TypeJSON struct {
	ID          int      `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Primary     string   `json:"primary"`
	Attributes  []string `json:"attributes"`
}

// FieldJSON is one lead field in `mkto fields --json`.
type FieldJSON struct {
	Name     string `json:"name"`
	Label    string `json:"label"`
	Type     string `json:"type"`
	Length   int    `json:"length"`
	ReadOnly bool   `json:"readOnly"`
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
	// ByUser and Errors are present only with --by-user / --errors (then [] when empty).
	ByUser *[]UserUsageJSON  `json:"byUser,omitempty"`
	Errors *[]ErrorCountJSON `json:"errors,omitempty"`
}

// UserUsageJSON is one API user's calls today in `mkto quota --by-user --json`.
type UserUsageJSON struct {
	UserID string `json:"userId"`
	Count  int    `json:"count"`
}

// ErrorCountJSON is one error code's count today in `mkto quota --errors --json`.
type ErrorCountJSON struct {
	Code  string `json:"code"`
	Count int    `json:"count"`
}

// DoctorJSON is the --json shape of `mkto doctor`. Checks is never null.
type DoctorJSON struct {
	Checks []CheckJSON `json:"checks"`
}

// CheckJSON is one doctor check. Status is "ok", "warn" or "fail".
type CheckJSON struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// WhyJSON is the --json shape of `mkto why`. Slices are never null; times are UTC.
type WhyJSON struct {
	Lead  LeadJSON   `json:"lead"`
	Rule  RuleJSON   `json:"rule"`
	Steps []StepJSON `json:"steps"`
	Marks []MarkJSON `json:"marks"`
	Notes []string   `json:"notes"`
}

// RuleJSON is the [why] rule the steps were judged by.
type RuleJSON struct {
	ScoreField  string   `json:"scoreField"`
	Threshold   int      `json:"threshold"`
	StatusField string   `json:"statusField"`
	MQLValues   []string `json:"mqlValues"`
	DQValues    []string `json:"dqValues"`
	NearHours   int      `json:"nearHours"`
}

// StepJSON is one score or status change; Kind is "score" or "status". Campaign is the
// smart campaign's name, when looked up.
type StepJSON struct {
	Date       time.Time `json:"date"`
	Kind       string    `json:"kind"`
	Field      string    `json:"field"`
	Old        string    `json:"old"`
	New        string    `json:"new"`
	Delta      int       `json:"delta,omitempty"`
	Reason     string    `json:"reason"`
	CampaignID int       `json:"campaignId,omitempty"`
	Campaign   string    `json:"campaign,omitempty"`
	ActivityID int       `json:"activityId"`
}

// MarkJSON is a threshold crossing or MQL/DQ change, with the other activities near it in
// time; with none near, Before holds the last ones before it, newest first.
type MarkJSON struct {
	Label  string      `json:"label"`
	Step   StepJSON    `json:"step"`
	Near   []EventJSON `json:"near"`
	Before []EventJSON `json:"before,omitempty"`
}

// LeadsJSON is the --json shape of `mkto leads`: leads in input order (several per
// email when Marketo has duplicates) and the emails with no lead. Slices are never null.
type LeadsJSON struct {
	Leads    []LeadJSON `json:"leads"`
	NotFound []string   `json:"notFound"`
}

// AccountJSON is the --json shape of `mkto account`. Total counts every lead found;
// Leads holds at most 200 of them, highest score first. Since is UTC.
type AccountJSON struct {
	Account string     `json:"account"`
	By      string     `json:"by"`
	Since   time.Time  `json:"since"`
	Total   int        `json:"total"`
	Leads   []LeadJSON `json:"leads"`
}

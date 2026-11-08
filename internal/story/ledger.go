// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package story

import (
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/canonical/snap-mkto/internal/marketo"
)

// Rules say which score and status changes matter. ScoreField and StatusField are
// matched case-insensitively against the activity's primary value, which Marketo fills
// with the field's display name ("Behavior Score", "Lead Status"), not its API name.
type Rules struct {
	ScoreField          string
	Threshold           int
	StatusField         string
	MQLValues, DQValues []string
	Near                time.Duration
}

// Step is one change to the score field (Kind "score") or the status field ("status").
type Step struct {
	Date       time.Time
	Kind       string
	Field      string
	Old, New   string
	Delta      int
	Reason     string
	CampaignID int
	ActivityID int
}

// Mark is a step worth pointing at: "crossed T", "dropped below T", "MQL" or "DQ", with
// the other activities near it in time (or, with none near, the last ones before it).
type Mark struct {
	Step  Step
	Label string
	Near  []marketo.Activity
	// Before: when Near is empty, the last 3 other activities before the step, newest first.
	Before []marketo.Activity
}

// Build lists the score and status steps in date order (then activity id) and marks the
// threshold crossings and MQL/DQ changes. A crossing is judged on the step's own Old and
// New values, never a running sum, and only when both are numbers. Attribute names are
// the same UNVERIFIED ones detail.go reads.
func Build(acts []marketo.Activity, typeName map[int]string, r Rules) (steps []Step, marks []Mark) {
	var others []marketo.Activity
	for _, a := range acts {
		kind := ""
		switch strings.ToLower(strings.TrimSpace(typeName[a.TypeID])) {
		case "change score":
			kind = "score"
			if !matches(a, r.ScoreField) {
				continue
			}
		case "change data value":
			kind = "status"
			if !matches(a, r.StatusField) {
				continue
			}
		default:
			others = append(others, a)
			continue
		}
		s := Step{Date: a.Date, Kind: kind, Field: ChangeField(a), Old: str(a.Attributes[attrOld]), New: str(a.Attributes[attrNew]),
			Reason: str(a.Attributes[attrReason]), CampaignID: a.CampaignID, ActivityID: a.ID}
		if kind == "score" {
			s.Delta = delta(a, s.Old, s.New)
		}
		steps = append(steps, s)
	}
	byDate := func(d1 time.Time, id1 int, d2 time.Time, id2 int) int {
		if c := d1.Compare(d2); c != 0 {
			return c
		}
		return id1 - id2
	}
	slices.SortFunc(steps, func(x, y Step) int { return byDate(x.Date, x.ActivityID, y.Date, y.ActivityID) })
	slices.SortFunc(others, func(x, y marketo.Activity) int { return byDate(x.Date, x.ID, y.Date, y.ID) })

	for _, s := range steps {
		label := ""
		if s.Kind == "score" {
			o, okO := num(s.Old)
			n, okN := num(s.New)
			t := float64(r.Threshold)
			switch {
			case !okO || !okN:
			case o < t && t <= n:
				label = "crossed " + strconv.Itoa(r.Threshold)
			case n < t && t <= o:
				label = "dropped below " + strconv.Itoa(r.Threshold)
			}
		} else if in(s.New, r.MQLValues) {
			label = "MQL"
		} else if in(s.New, r.DQValues) {
			label = "DQ"
		}
		if label == "" {
			continue
		}
		m := Mark{Step: s, Label: label}
		if r.Near > 0 {
			for _, a := range others {
				if d := a.Date.Sub(s.Date); -r.Near <= d && d <= r.Near {
					m.Near = append(m.Near, a)
				}
			}
		}
		for i := len(others) - 1; i >= 0 && len(m.Near) == 0 && len(m.Before) < maxBefore; i-- {
			if others[i].Date.Before(s.Date) {
				m.Before = append(m.Before, others[i])
			}
		}
		marks = append(marks, m)
	}
	return steps, marks
}

// maxBefore caps a mark's Before list.
const maxBefore = 3

// matches reports whether a's field is the configured one; an unset field matches nothing.
func matches(a marketo.Activity, field string) bool {
	field = strings.TrimSpace(field)
	return field != "" && strings.EqualFold(ChangeField(a), field)
}

// delta is "Change Value" when it is a number ("+25", "-10", 25), else New-Old, else 0.
func delta(a marketo.Activity, old, new string) int {
	if f, ok := num(str(a.Attributes[attrChange])); ok {
		return int(math.Round(f))
	}
	o, okO := num(old)
	n, okN := num(new)
	if !okO || !okN {
		return 0
	}
	return int(math.Round(n - o))
}

// num parses a finite number; "NaN" and "Inf" are not scores.
func num(s string) (float64, bool) {
	f, err := strconv.ParseFloat(s, 64)
	return f, err == nil && !math.IsNaN(f) && !math.IsInf(f, 0)
}

func in(v string, list []string) bool {
	return slices.ContainsFunc(list, func(s string) bool { return strings.EqualFold(strings.TrimSpace(s), v) })
}

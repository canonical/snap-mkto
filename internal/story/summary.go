// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package story

import (
	"fmt"
	"strings"
	"time"

	"github.com/canonical/snap-mkto/internal/marketo"
)

// Summary is the call card's view of a window: touches per type name, and the newest touch.
type Summary struct {
	Counts    map[string]int
	LastTouch time.Time
}

// Summarize counts touches by type name ("type N" when unknown). A touch is any activity
// except Email Delivered, which says nothing about the lead. LastTouch is never after now.
func Summarize(acts []marketo.Activity, typeName map[int]string, now time.Time) Summary {
	s := Summary{Counts: map[string]int{}}
	for _, a := range acts {
		name := typeName[a.TypeID]
		if strings.EqualFold(name, "Email Delivered") {
			continue
		}
		if name == "" {
			name = fmt.Sprintf("type %d", a.TypeID)
		}
		s.Counts[name]++
		if a.Date.After(s.LastTouch) {
			s.LastTouch = a.Date
		}
	}
	if s.LastTouch.After(now) {
		s.LastTouch = now
	}
	return s
}

// Ago says how long before now t was: under a minute (or in the future) is "just now",
// then "Nm ago", "Nh ago" and "Nd ago"; 30 days or more is t's date in t's own zone.
func Ago(t, now time.Time) string {
	switch d := now.Sub(t); {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", d/time.Minute)
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", d/time.Hour)
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", d/(24*time.Hour))
	}
	return t.Format("2006-01-02")
}

// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package story

import (
	"maps"
	"testing"
	"time"

	"github.com/canonical/snap-mkto/internal/marketo"
)

func TestAgo(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	tbilisi := time.FixedZone("+04", 4*3600)
	for _, c := range []struct {
		t    time.Time
		want string
	}{
		{now, "just now"},
		{now.Add(time.Minute), "just now"}, // a clock a little ahead is not "-1m ago"
		{now.Add(-59 * time.Second), "just now"},
		{now.Add(-time.Minute), "1m ago"},
		{now.Add(-59*time.Minute - 59*time.Second), "59m ago"},
		{now.Add(-time.Hour), "1h ago"},
		{now.Add(-23*time.Hour - 59*time.Minute), "23h ago"},
		{now.Add(-24 * time.Hour), "1d ago"},
		{now.Add(-48 * time.Hour), "2d ago"},
		{now.Add(-30*24*time.Hour + time.Second), "29d ago"},
		{now.Add(-30 * 24 * time.Hour), "2026-10-08"},
		{time.Date(2026, 9, 30, 22, 0, 0, 0, time.UTC).In(tbilisi), "2026-10-01"}, // the date is in t's zone
	} {
		if got := Ago(c.t, now); got != c.want {
			t.Errorf("Ago(now%+v) = %q, want %q", c.t.Sub(now), got, c.want)
		}
	}
}

func TestSummarizeSkipsDelivered(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	day := func(d int) time.Time { return time.Date(2026, 11, d, 9, 0, 0, 0, time.UTC) }
	names := map[int]string{7: "Email Delivered", 10: "Open Email", 11: "Click Email"}
	acts := []marketo.Activity{
		{TypeID: 10, Date: day(1)},
		{TypeID: 11, Date: day(2)},
		{TypeID: 10, Date: day(3)},
		{TypeID: 99, Date: day(4)},
		{TypeID: 7, Date: day(6)}, // the newest, but a delivery isn't a touch
	}
	s := Summarize(acts, names, now)
	if want := map[string]int{"Open Email": 2, "Click Email": 1, "type 99": 1}; !maps.Equal(s.Counts, want) {
		t.Errorf("Counts = %v, want %v", s.Counts, want)
	}
	if !s.LastTouch.Equal(day(4)) {
		t.Errorf("LastTouch = %v, want %v", s.LastTouch, day(4))
	}
	if s := Summarize(acts[4:], names, now); len(s.Counts) != 0 || !s.LastTouch.IsZero() {
		t.Errorf("only deliveries: %+v", s)
	}
	if s := Summarize([]marketo.Activity{{TypeID: 10, Date: now.Add(time.Hour)}}, names, now); !s.LastTouch.Equal(now) {
		t.Errorf("future activity: LastTouch = %v, want now", s.LastTouch)
	}
}

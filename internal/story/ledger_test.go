// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package story

import (
	"reflect"
	"testing"
	"time"

	"github.com/canonical/snap-mkto/internal/marketo"
)

var (
	ledgerTypes = map[int]string{1: "Visit Webpage", 2: "Fill Out Form", 22: "Change Score", 13: "Change Data Value", 104: "Change Status in Progression"}
	ledgerT0    = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	ledgerRules = Rules{ScoreField: "Behavior Score", Threshold: 80, StatusField: "Lead Status", MQLValues: []string{"MQL"}, DQValues: []string{"Disqualified", "Recycled"}}
)

func at(h int) time.Time { return ledgerT0.Add(time.Duration(h) * time.Hour) }

func score(id, h int, field string, attrs map[string]any) marketo.Activity {
	return marketo.Activity{ID: id, Date: at(h), TypeID: 22, Primary: field, CampaignID: 500 + id, Attributes: attrs}
}

func change(id, h int, field, old, new string) marketo.Activity {
	return marketo.Activity{ID: id, Date: at(h), TypeID: 13, Primary: field, Attributes: map[string]any{"Old Value": old, "New Value": new, "Reason": "Lead status campaign"}}
}

func labels(marks []Mark) []string {
	var out []string
	for _, m := range marks {
		out = append(out, m.Label)
	}
	return out
}

func TestLedgerCrossUp(t *testing.T) {
	for _, c := range []struct {
		name  string
		acts  []marketo.Activity
		delta []int
		want  []string
	}{
		{"strings", []marketo.Activity{
			score(2, 2, "Behavior Score", map[string]any{"Change Value": "+25", "Old Value": "60", "New Value": "85", "Reason": "Filled out form"}),
			score(1, 1, "Behavior Score", map[string]any{"Change Value": "+10", "Old Value": "50", "New Value": "60"}),
		}, []int{10, 25}, []string{"crossed 80"}},
		{"numbers land on threshold", []marketo.Activity{
			score(1, 1, "behavior score", map[string]any{"Change Value": float64(20), "Old Value": float64(60), "New Value": float64(80)}),
		}, []int{20}, []string{"crossed 80"}},
		{"delta from old/new", []marketo.Activity{
			score(1, 1, "Behavior Score", map[string]any{"Old Value": "70", "New Value": "90"}),
		}, []int{20}, []string{"crossed 80"}},
		{"already above", []marketo.Activity{
			score(1, 1, "Behavior Score", map[string]any{"Change Value": "+5", "Old Value": "80", "New Value": "85"}),
		}, []int{5}, nil},
		{"no change value nor numbers", []marketo.Activity{
			score(1, 1, "Behavior Score", map[string]any{"Old Value": "n/a", "New Value": "85"}),
		}, []int{0}, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			steps, marks := Build(c.acts, ledgerTypes, ledgerRules)
			var deltas []int
			for i, s := range steps {
				deltas = append(deltas, s.Delta)
				if s.Kind != "score" {
					t.Errorf("step %d Kind = %q, want score", i, s.Kind)
				}
				if i > 0 && s.Date.Before(steps[i-1].Date) {
					t.Errorf("steps not sorted by date")
				}
			}
			if !reflect.DeepEqual(deltas, c.delta) {
				t.Errorf("deltas = %v, want %v", deltas, c.delta)
			}
			if got := labels(marks); !reflect.DeepEqual(got, c.want) {
				t.Errorf("labels = %v, want %v", got, c.want)
			}
		})
	}
	steps, marks := Build([]marketo.Activity{score(7, 1, "Behavior Score", map[string]any{"Change Value": "+25", "Old Value": "60", "New Value": "85", "Reason": "Filled out form"})}, ledgerTypes, ledgerRules)
	want := Step{Date: at(1), Kind: "score", Field: "Behavior Score", Old: "60", New: "85", Delta: 25, Reason: "Filled out form", CampaignID: 507, ActivityID: 7}
	if len(steps) != 1 || steps[0] != want || len(marks) != 1 || marks[0].Step != want {
		t.Errorf("steps = %+v, marks = %+v, want step %+v", steps, marks, want)
	}
}

func TestLedgerDropThenRecross(t *testing.T) {
	acts := []marketo.Activity{
		score(3, 3, "Behavior Score", map[string]any{"Change Value": "+15", "Old Value": "75", "New Value": "90"}),
		score(1, 1, "Behavior Score", map[string]any{"Change Value": "+25", "Old Value": "60", "New Value": "85"}),
		score(2, 2, "Behavior Score", map[string]any{"Change Value": "-10", "Old Value": "85", "New Value": "75"}),
		// same date as id 2: ActivityID breaks the tie
		score(4, 2, "Behavior Score", map[string]any{"Change Value": "0", "Old Value": "75", "New Value": "75"}),
	}
	steps, marks := Build(acts, ledgerTypes, ledgerRules)
	var ids []int
	for _, s := range steps {
		ids = append(ids, s.ActivityID)
	}
	if want := []int{1, 2, 4, 3}; !reflect.DeepEqual(ids, want) {
		t.Errorf("step ids = %v, want %v", ids, want)
	}
	if got, want := labels(marks), []string{"crossed 80", "dropped below 80", "crossed 80"}; !reflect.DeepEqual(got, want) {
		t.Errorf("labels = %v, want %v", got, want)
	}
	if marks[1].Step.Delta != -10 {
		t.Errorf("drop Delta = %d, want -10", marks[1].Step.Delta)
	}
	// Leaving exactly the threshold is a drop; staying on it is not.
	_, marks = Build([]marketo.Activity{
		score(5, 1, "Behavior Score", map[string]any{"Old Value": "80", "New Value": "79"}),
		score(6, 2, "Behavior Score", map[string]any{"Old Value": "80", "New Value": "80"}),
	}, ledgerTypes, ledgerRules)
	if got, want := labels(marks), []string{"dropped below 80"}; !reflect.DeepEqual(got, want) {
		t.Errorf("edge labels = %v, want %v", got, want)
	}
}

func TestLedgerMissingOldValue(t *testing.T) {
	for _, c := range []struct {
		name  string
		attrs map[string]any
	}{
		{"no old", map[string]any{"Change Value": "+90", "New Value": "90"}},
		{"empty old", map[string]any{"Change Value": "+90", "Old Value": "", "New Value": "90"}},
		{"no new", map[string]any{"Change Value": "+90", "Old Value": "0"}},
		{"nil attrs", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			steps, marks := Build([]marketo.Activity{score(1, 1, "Behavior Score", c.attrs)}, ledgerTypes, ledgerRules)
			if len(steps) != 1 {
				t.Fatalf("steps = %d, want 1", len(steps))
			}
			if len(marks) != 0 {
				t.Errorf("marks = %v, want none", labels(marks))
			}
		})
	}
}

func TestLedgerStatusMQLAndDQ(t *testing.T) {
	acts := []marketo.Activity{
		change(2, 5, "Lead Status", "MQL", "disqualified"),
		change(1, 1, "lead status", "Nurture", "mql"),
		change(3, 6, "Lead Status", "Disqualified", "Nurture"),
		change(4, 7, "Job Title", "Engineer", "MQL"), // other data-value field
	}
	steps, marks := Build(acts, ledgerTypes, ledgerRules)
	if len(steps) != 3 {
		t.Fatalf("steps = %d, want 3", len(steps))
	}
	for _, s := range steps {
		if s.Kind != "status" || s.Delta != 0 || s.Reason != "Lead status campaign" {
			t.Errorf("step = %+v, want status, Delta 0, reason", s)
		}
	}
	if got, want := labels(marks), []string{"MQL", "DQ"}; !reflect.DeepEqual(got, want) {
		t.Errorf("labels = %v, want %v", got, want)
	}
	if marks[0].Step.Old != "Nurture" || marks[0].Step.New != "mql" {
		t.Errorf("MQL step = %+v", marks[0].Step)
	}
	// A value in both lists is a config error; MQL wins, on purpose.
	both := ledgerRules
	both.DQValues = append([]string{"MQL"}, both.DQValues...)
	if _, m := Build([]marketo.Activity{change(9, 1, "Lead Status", "Nurture", "MQL")}, ledgerTypes, both); len(m) != 1 || m[0].Label != "MQL" {
		t.Errorf("both lists: marks = %v", labels(m))
	}
}

func TestLedgerNearWindow(t *testing.T) {
	visit := func(id int, d time.Duration) marketo.Activity {
		return marketo.Activity{ID: id, Date: at(10).Add(d), TypeID: 1, Primary: "www.example.com/pricing"}
	}
	acts := []marketo.Activity{
		score(1, 10, "Behavior Score", map[string]any{"Change Value": "+25", "Old Value": "60", "New Value": "85"}),
		visit(11, 30*time.Minute),
		visit(12, -time.Hour), // exactly on the edge: included
		visit(13, -61*time.Minute),
		visit(14, 2*time.Hour),
		{ID: 15, Date: at(10), TypeID: 2, Primary: "Contact Us Form"},
		score(16, 10, "Demographic Score", map[string]any{"Change Value": "+5"}), // a Change Score row: never near
		change(17, 10, "Job Title", "a", "b"),                                    // a Change Data Value row: never near
		{ID: 18, Date: at(10), TypeID: 104, Primary: "Example Webinar 2026"},
	}
	for _, c := range []struct {
		name string
		near time.Duration
		want []int
	}{
		{"one hour", time.Hour, []int{12, 15, 18, 11}},
		{"zero", 0, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := ledgerRules
			r.Near = c.near
			_, marks := Build(acts, ledgerTypes, r)
			if len(marks) != 1 {
				t.Fatalf("marks = %v, want one", labels(marks))
			}
			var ids []int
			for _, a := range marks[0].Near {
				ids = append(ids, a.ID)
			}
			if !reflect.DeepEqual(ids, c.want) {
				t.Errorf("near ids = %v, want %v", ids, c.want)
			}
		})
	}
}

func TestLedgerOtherScoreFieldIgnored(t *testing.T) {
	acts := []marketo.Activity{
		score(1, 1, "Demographic Score", map[string]any{"Change Value": "+25", "Old Value": "60", "New Value": "85"}),
		score(2, 2, "", map[string]any{"Change Value": "+25", "Old Value": "60", "New Value": "85"}),
		{ID: 3, Date: at(3), TypeID: 1, Primary: "Behavior Score"}, // not a Change Score row
	}
	if steps, marks := Build(acts, ledgerTypes, ledgerRules); len(steps) != 0 || len(marks) != 0 {
		t.Errorf("steps = %+v, marks = %v, want none", steps, labels(marks))
	}
	// an unset field never matches an empty primary
	if steps, _ := Build(acts, ledgerTypes, Rules{Threshold: 80}); len(steps) != 0 {
		t.Errorf("unset rules: steps = %+v, want none", steps)
	}
}

// TestLedgerBefore: a mark with nothing near gets the last 3 other activities strictly
// before it, newest first; a mark with near events gets none.
func TestLedgerBefore(t *testing.T) {
	form := func(id, h int) marketo.Activity {
		return marketo.Activity{ID: id, Date: at(h), TypeID: 2, Primary: "Contact Us Form"}
	}
	cross := score(1, 100, "Behavior Score", map[string]any{"Old Value": "60", "New Value": "85"})
	r := ledgerRules
	r.Near = 2 * time.Hour
	for _, c := range []struct {
		name string
		acts []marketo.Activity
		near []int
		want []int
	}{
		{"last three, newest first", []marketo.Activity{cross, form(11, 10), form(12, 50), form(13, 90), form(14, 95), form(15, 110),
			score(16, 96, "Demographic Score", map[string]any{"Change Value": "+5"}), change(17, 97, "Job Title", "a", "b")}, nil, []int{14, 13, 12}},
		{"fewer than three", []marketo.Activity{cross, form(11, 10)}, nil, []int{11}},
		{"same time is near, not before", []marketo.Activity{cross, form(11, 100), form(12, 10)}, []int{11}, nil},
		{"only after", []marketo.Activity{cross, form(11, 110)}, nil, nil},
		{"none", []marketo.Activity{cross}, nil, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, marks := Build(c.acts, ledgerTypes, r)
			if len(marks) != 1 {
				t.Fatalf("marks = %v, want one", labels(marks))
			}
			ids := func(as []marketo.Activity) []int {
				var out []int
				for _, a := range as {
					out = append(out, a.ID)
				}
				return out
			}
			if got := ids(marks[0].Near); !reflect.DeepEqual(got, c.near) {
				t.Errorf("near ids = %v, want %v", got, c.near)
			}
			if got := ids(marks[0].Before); !reflect.DeepEqual(got, c.want) {
				t.Errorf("before ids = %v, want %v", got, c.want)
			}
		})
	}
	// Near 0 (no window): before is still filled.
	_, marks := Build([]marketo.Activity{cross, form(11, 99)}, ledgerTypes, ledgerRules)
	if len(marks) != 1 || len(marks[0].Before) != 1 {
		t.Errorf("near 0: marks = %+v", marks)
	}
}

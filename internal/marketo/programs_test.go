// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package marketo

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/canonical/snap-mkto/internal/marketotest"
)

func TestParseTime(t *testing.T) {
	want := time.Date(2026, 11, 1, 1, 8, 44, 0, time.UTC)
	for _, s := range []string{
		"2026-11-01T01:08:44Z+0000", // the asset API's form, seen on a real instance 2026-11-07
		"2026-11-01T01:08:44Z",
		"2026-11-01T01:08:44+0000",
		"2026-11-01T04:08:44+03:00",
		"2026-10-31T18:08:44Z-0700",
	} {
		got, err := parseTime(s)
		if err != nil || !got.Equal(want) {
			t.Errorf("parseTime(%q) = %v, %v; want %v", s, got, err, want)
		}
	}
	if _, err := parseTime("01/11/2026"); err == nil {
		t.Error("parseTime accepted a non-date")
	}
}

func TestProgramAssetDate(t *testing.T) {
	s := marketotest.New(t)
	s.Handle("/rest/asset/v1/programs.json", marketotest.Response{Body: "programs_browse_page.json"}, marketotest.Response{Body: "programs_browse_empty.json"})
	all, err := newHarness(s, Options{}).c.Programs(context.Background())
	if err != nil || len(all) != 2 || !all[0].CreatedAt.Equal(time.Date(2026, 1, 5, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("Programs = %+v, %v", all, err)
	}
}

func TestProgramBadDateIsDecodeError(t *testing.T) {
	s := marketotest.New(t)
	s.Handle("/rest/asset/v1/programs.json", marketotest.Response{Body: `{"success":true,"result":[{"id":2001,"createdAt":"yesterday"}]}`})
	if _, err := newHarness(s, Options{}).c.Programs(context.Background()); !errors.Is(err, ErrDecode) {
		t.Fatalf("err = %v, want ErrDecode", err)
	}
}

// Program by id is only used for names, so a date it can't parse must not break it.
func TestProgramByIDIgnoresDate(t *testing.T) {
	s := marketotest.New(t)
	s.Handle("/rest/asset/v1/program/2001.json", marketotest.Response{Body: `{"success":true,"result":[{"id":2001,"name":"P","createdAt":"some new shape"}]}`})
	if p, err := newHarness(s, Options{}).c.Program(context.Background(), 2001); err != nil || p.Name != "P" {
		t.Fatalf("Program = %+v, %v", p, err)
	}
}

func TestHTTPErrorStatus(t *testing.T) {
	s := marketotest.New(t)
	s.Handle("/rest/asset/v1/program/2001.json", marketotest.Response{Status: http.StatusForbidden, Body: `{"success":false}`})
	_, err := newHarness(s, Options{}).c.Program(context.Background(), 2001)
	var he *HTTPError
	if !errors.As(err, &he) || he.Status != http.StatusForbidden || err.Error() != "Marketo returned HTTP 403" {
		t.Fatalf("err = %v, want HTTPError 403", err)
	}
}

func TestMembershipDates(t *testing.T) {
	s := marketotest.New(t)
	s.Handle("/rest/v1/leads/1/programMembership.json", marketotest.Response{Body: `{"success":true,"result":[
{"id":1,"progressionStatus":"Member","membershipDate":"2026-11-01T01:08:44Z+0000","updatedAt":"2026-11-05T00:00:00Z+0000"},
{"id":2,"progressionStatus":"Member","updatedAt":"2026-10-02T00:00:00Z"},
{"id":3,"progressionStatus":"Member","membershipDate":"junk"},
{"id":4,"progressionStatus":"Member"}]}`})
	ms, err := newHarness(s, Options{}).c.Memberships(context.Background(), 1)
	if err != nil || len(ms) != 4 {
		t.Fatalf("Memberships = %+v, %v", ms, err)
	}
	want := []time.Time{time.Date(2026, 11, 1, 1, 8, 44, 0, time.UTC), time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC), {}, {}}
	for i, m := range ms {
		if !m.Date.Equal(want[i]) {
			t.Errorf("membership %d date = %v, want %v", m.ProgramID, m.Date, want[i])
		}
	}
}

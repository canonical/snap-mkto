// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package marketo

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/canonical/snap-mkto/internal/marketotest"
)

func TestLeadsPaging(t *testing.T) {
	s := marketotest.New(t)
	s.Handle(leadsPath, marketotest.Response{Body: "leads_account_page1.json"}, marketotest.Response{Body: "leads_account_page2.json"})
	leads, err := newHarness(s, Options{}).c.Leads(context.Background(), "sfdcAccountId", []string{"001EXAMPLE000001AAA"}, []string{"id", "email"})
	if err != nil || len(leads) != 3 || leads[2].ID() != 1003 {
		t.Fatalf("Leads = %v, %v", leads, err)
	}
	q := s.Query(leadsPath)
	if s.Hits(leadsPath) != 2 || q.Get("nextPageToken") != "EXAMPLELEADPAGE2====" || q.Get("filterType") != "sfdcAccountId" ||
		q.Get("filterValues") != "001EXAMPLE000001AAA" || q.Get("fields") != "id,email" {
		t.Errorf("hits %d, last query %v", s.Hits(leadsPath), q)
	}

	// A token that comes back again is an error, not a silently short list (account rosters).
	s = marketotest.New(t)
	s.Handle(leadsPath, marketotest.Response{Body: "leads_account_page1.json"})
	_, err = newHarness(s, Options{}).c.Leads(context.Background(), "sfdcAccountId", []string{"x"}, []string{"id"})
	if err == nil || !strings.Contains(err.Error(), "paging did not advance for GET /rest/v1/leads.json") || s.Hits(leadsPath) != 2 {
		t.Errorf("repeated token: hits %d, err %v", s.Hits(leadsPath), err)
	}
}

func TestListMembershipPaging(t *testing.T) {
	const path = "/rest/v1/leads/1001/listMembership.json"
	s := marketotest.New(t)
	s.Handle(path, marketotest.Response{Body: `{"success":true,"moreResult":true,"nextPageToken":"EXAMPLELISTPAGE2====","result":[
{"listId":5002,"createdAt":"2026-10-01T09:00:00Z","updatedAt":"2026-10-01T09:00:00Z"},
{"listId":5003,"createdAt":"junk"}]}`}, marketotest.Response{Body: "list_membership_one.json"})
	lists, err := newHarness(s, Options{}).c.ListMemberships(context.Background(), 1001)
	want := []ListMembership{{5002, time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)}, {5003, time.Time{}}, {5001, time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)}}
	if err != nil || len(lists) != len(want) {
		t.Fatalf("ListMemberships = %+v, %v", lists, err)
	}
	for i := range want {
		if lists[i].ListID != want[i].ListID || !lists[i].Added.Equal(want[i].Added) {
			t.Errorf("list %d = %+v, want %+v", i, lists[i], want[i])
		}
	}
	if s.Hits(path) != 2 || s.Query(path).Get("nextPageToken") != "EXAMPLELISTPAGE2====" {
		t.Errorf("hits %d, last query %v", s.Hits(path), s.Query(path))
	}

	// A token that comes back again is an error, not a silently short list.
	s = marketotest.New(t)
	s.Handle(path, marketotest.Response{Body: `{"success":true,"moreResult":true,"nextPageToken":"SAME","result":[{"listId":1}]}`})
	_, err = newHarness(s, Options{}).c.ListMemberships(context.Background(), 1001)
	if err == nil || !strings.Contains(err.Error(), "paging did not advance for GET /rest/v1/leads/{id}/listMembership.json") || s.Hits(path) != 2 {
		t.Errorf("repeated token: hits %d, err %v", s.Hits(path), err)
	}
}

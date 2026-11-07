// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package marketo

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/canonical/snap-mkto/internal/marketotest"
)

const (
	typesPath  = "/rest/v1/activities/types.json"
	tokenPath  = "/rest/v1/activities/pagingtoken.json"
	actsPath   = "/rest/v1/activities.json"
	guardError = "Marketo paging did not advance for GET /rest/v1/activities.json; stopping to protect your API quota"
)

var since = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

func page(rows string, more bool, next string) marketotest.Response {
	return marketotest.Response{Body: fmt.Sprintf(`{"success":true,"result":[%s],"moreResult":%t,"nextPageToken":%q}`, rows, more, next)}
}

func row(id int, date string) string {
	return fmt.Sprintf(`{"id":%d,"leadId":1,"activityDate":%q,"activityTypeId":1,"primaryAttributeValue":"p","attributes":[{"name":"a","value":"v"}]}`, id, date)
}

func TestResolveTypesUnknown(t *testing.T) {
	all := []ActivityType{{1, "Visit Webpage"}, {2, "Fill Out Form"}, {6, "Send Email"}}
	ids, unknown := ResolveTypes(all, []string{" fill out form ", "Nope", "Visit Webpage", "FILL OUT FORM", "Zzz"})
	if !reflect.DeepEqual(ids, []int{2, 1}) {
		t.Errorf("ids = %v, want [2 1]", ids)
	}
	if !reflect.DeepEqual(unknown, []string{"Nope", "Zzz"}) {
		t.Errorf("unknown = %v", unknown)
	}
}

func TestActivityTypes(t *testing.T) {
	s := marketotest.New(t)
	s.Handle(typesPath, marketotest.Response{Body: "activity_types.json"})
	types, err := newHarness(s, Options{}).c.ActivityTypes(context.Background())
	if err != nil || len(types) < 23 {
		t.Fatalf("got %d types, err %v", len(types), err)
	}
	ids, unknown := ResolveTypes(types, []string{"visit webpage"})
	if len(ids) != 1 || len(unknown) != 0 {
		t.Errorf("ids %v unknown %v", ids, unknown)
	}
}

// recorder captures the activityTypeIds sent on each activities.json call.
type recorder struct{ batches []string }

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Path == actsPath {
		r.batches = append(r.batches, req.URL.Query().Get("activityTypeIds"))
	}
	return http.DefaultTransport.RoundTrip(req)
}

func TestActivitiesBatching(t *testing.T) {
	s := marketotest.New(t)
	s.Handle(tokenPath, marketotest.Response{Body: "pagingtoken_ok.json"})
	s.Handle(actsPath, page("", false, "x"))
	rec := &recorder{}
	ids := make([]int, 23)
	for i := range ids {
		ids[i] = 100 + i
	}
	h := newHarness(s, Options{HTTP: &http.Client{Transport: rec}})
	if _, err := h.c.Activities(context.Background(), 42, ids, since); err != nil {
		t.Fatal(err)
	}
	if got := s.Hits(tokenPath); got != 3 {
		t.Errorf("paging token hits = %d, want 3", got)
	}
	var sizes []int
	for _, b := range rec.batches {
		sizes = append(sizes, len(strings.Split(b, ",")))
	}
	if !reflect.DeepEqual(sizes, []int{10, 10, 3}) {
		t.Errorf("batch sizes = %v, want [10 10 3]", sizes)
	}
	if q := s.Query(actsPath); q.Get("leadIds") != "42" || q.Get("nextPageToken") == "" {
		t.Errorf("query = %v", q)
	}
	if q := s.Query(tokenPath); q.Get("sinceDatetime") != "2026-10-01T00:00:00Z" {
		t.Errorf("sinceDatetime = %q", q.Get("sinceDatetime"))
	}
}

func TestActivitiesMoreResult(t *testing.T) {
	s := marketotest.New(t)
	s.Handle(tokenPath, marketotest.Response{Body: "pagingtoken_ok.json"})
	s.Handle(actsPath, marketotest.Response{Body: "activities_page_empty_more.json"}, marketotest.Response{Body: "activities_final_page.json"})
	acts, err := newHarness(s, Options{}).c.Activities(context.Background(), 1001, []int{1, 2}, since)
	if err != nil || len(acts) != 2 {
		t.Fatalf("got %d, err %v", len(acts), err)
	}
	a := acts[0]
	if a.ID != 5001 || a.TypeID != 1 || a.Primary != "www.example.com/pricing" || a.Date.IsZero() ||
		a.Attributes["Referrer URL"] != "https://www.example.com/" {
		t.Errorf("first activity = %+v", a)
	}
	if got := s.Query(actsPath).Get("nextPageToken"); got != "EXAMPLETOKENBBBB====" {
		t.Errorf("second page called with token %q", got)
	}
}

func TestActivitiesMergedSorted(t *testing.T) {
	s := marketotest.New(t)
	s.Handle(tokenPath, marketotest.Response{Body: "pagingtoken_ok.json"})
	s.Handle(actsPath,
		page(row(9, "2026-11-03T00:00:00Z")+","+row(5, "2026-11-02T00:00:00Z"), false, "a"),
		page(row(7, "2026-11-02T00:00:00Z")+","+row(1, "2026-11-01T00:00:00Z"), false, "b"))
	ids := make([]int, 12) // two batches
	acts, err := newHarness(s, Options{}).c.Activities(context.Background(), 1, ids, since)
	if err != nil {
		t.Fatal(err)
	}
	var got []int
	for _, a := range acts {
		got = append(got, a.ID)
	}
	if !reflect.DeepEqual(got, []int{1, 5, 7, 9}) {
		t.Errorf("order = %v, want [1 5 7 9]", got)
	}
}

func TestActivitiesPagingGuard(t *testing.T) {
	s := marketotest.New(t)
	s.Handle(tokenPath, marketotest.Response{Body: "pagingtoken_ok.json"})
	s.Handle(actsPath, marketotest.Response{Body: "activities_page_empty_more.json"}) // repeats forever
	_, err := newHarness(s, Options{}).c.Activities(context.Background(), 1, []int{1}, since)
	if err == nil || err.Error() != guardError {
		t.Fatalf("err = %v", err)
	}
	if got := s.Hits(actsPath); got != 2 {
		t.Errorf("activities hits = %d, want 2", got)
	}
}

func TestActivitiesPageCap(t *testing.T) {
	var n atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case marketotest.IdentityPath:
			fmt.Fprint(w, `{"access_token":"t","expires_in":3600}`)
		case tokenPath:
			fmt.Fprint(w, `{"success":true,"nextPageToken":"t0"}`)
		default: // always more, with a fresh token each time
			fmt.Fprintf(w, `{"success":true,"result":[],"moreResult":true,"nextPageToken":"t%d"}`, n.Add(1))
		}
	}))
	defer ts.Close()
	_, err := newHarnessURL(ts.URL, Options{}).c.Activities(context.Background(), 1, []int{1}, since)
	if err == nil || err.Error() != guardError {
		t.Fatalf("err = %v", err)
	}
	if got := n.Load(); got != 1000 {
		t.Errorf("pages = %d, want 1000", got)
	}
}

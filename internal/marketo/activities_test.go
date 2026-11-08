// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package marketo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
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
	all := []ActivityType{{ID: 1, Name: "Visit Webpage"}, {ID: 2, Name: "Fill Out Form"}, {ID: 6, Name: "Send Email"}}
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

func TestActivitiesBatching(t *testing.T) {
	s := marketotest.New(t)
	s.Handle(tokenPath, marketotest.Response{Body: "pagingtoken_ok.json"})
	s.Handle(actsPath, page("", false, "x"))
	ids := make([]int, 23)
	for i := range ids {
		ids[i] = 100 + i
	}
	if _, err := newHarness(s, Options{}).c.Activities(context.Background(), 42, ids, since); err != nil {
		t.Fatal(err)
	}
	if got := s.Hits(tokenPath); got != 6 {
		t.Errorf("paging token hits = %d, want 6", got)
	}
	var sizes []int
	seen := map[string]bool{}
	for _, q := range s.Queries(actsPath) {
		b := strings.Split(q.Get("activityTypeIds"), ",")
		sizes = append(sizes, len(b))
		for _, id := range b {
			seen[id] = true
		}
		if q.Get("leadIds") != "42" || q.Get("nextPageToken") == "" {
			t.Errorf("query = %v", q)
		}
	}
	slices.Sort(sizes) // batches run concurrently, so arrival order varies
	if !reflect.DeepEqual(sizes, []int{3, 4, 4, 4, 4, 4}) || len(seen) != 23 {
		t.Errorf("batch sizes = %v over %d distinct ids, want [3 4 4 4 4 4] over 23", sizes, len(seen))
	}
	if q := s.Query(tokenPath); q.Get("sinceDatetime") != "2026-10-01T00:00:00Z" {
		t.Errorf("sinceDatetime = %q", q.Get("sinceDatetime"))
	}
}

// TestActivitiesParallelRefresh runs 3 batches that all hit 601 with the first token at
// once: one new token serves them all.
func TestActivitiesParallelRefresh(t *testing.T) {
	var identity, arrived atomic.Int32
	all := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case marketotest.IdentityPath:
			fmt.Fprintf(w, `{"access_token":"t%d","expires_in":3600}`, identity.Add(1))
		case tokenPath:
			fmt.Fprint(w, `{"success":true,"nextPageToken":"p0"}`)
		default:
			if r.Header.Get("Authorization") == "Bearer t1" {
				if arrived.Add(1) == 3 {
					close(all)
				}
				select { // all three walks hold t1 before any learns it expired
				case <-all:
				case <-time.After(2 * time.Second):
					t.Error("the 3 batches did not run concurrently")
				}
				fmt.Fprint(w, `{"success":false,"errors":[{"code":"601","message":"Access token invalid"}]}`)
				return
			}
			fmt.Fprint(w, `{"success":true,"result":[],"moreResult":false}`)
		}
	}))
	defer ts.Close()
	_, err := newHarnessURL(ts.URL, Options{}).c.Activities(context.Background(), 1, []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}, since)
	if err != nil {
		t.Fatal(err)
	}
	if got := identity.Load(); got != 2 {
		t.Errorf("identity calls = %d, want 2 (one extra after 601)", got)
	}
}

func TestActivitiesAtMostThreeWalks(t *testing.T) {
	var now, peak atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case marketotest.IdentityPath:
			fmt.Fprint(w, `{"access_token":"t","expires_in":3600}`)
		case tokenPath:
			fmt.Fprint(w, `{"success":true,"nextPageToken":"p0"}`)
		default:
			n := now.Add(1)
			for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
			}
			time.Sleep(20 * time.Millisecond)
			now.Add(-1)
			fmt.Fprint(w, `{"success":true,"result":[],"moreResult":false}`)
		}
	}))
	defer ts.Close()
	ids := make([]int, 24) // 6 batches
	if _, err := newHarnessURL(ts.URL, Options{}).c.Activities(context.Background(), 1, ids, since); err != nil {
		t.Fatal(err)
	}
	if got := peak.Load(); got > 3 || got < 2 {
		t.Errorf("peak concurrent activities calls = %d, want at most 3 (and some overlap)", got)
	}
}

// TestActivitiesQuotaWins: one batch hits 607 while the others are mid-page; the quota
// error is returned, not the cancellations it caused, and the others stop.
func TestActivitiesQuotaWins(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == marketotest.IdentityPath:
			fmt.Fprint(w, `{"access_token":"t","expires_in":3600}`)
		case r.URL.Path == tokenPath:
			fmt.Fprint(w, `{"success":true,"nextPageToken":"p0"}`)
		case r.URL.Query().Get("activityTypeIds") == "9":
			time.Sleep(20 * time.Millisecond)
			fmt.Fprint(w, `{"success":false,"errors":[{"code":"607","message":"Daily quota reached"}]}`)
		default:
			select { // hang until the client gives up
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
		}
	}))
	defer ts.Close()
	start := time.Now()
	_, err := newHarnessURL(ts.URL, Options{}).c.Activities(context.Background(), 1, []int{1, 2, 3, 4, 5, 6, 7, 8, 9}, since)
	if !errors.Is(err, ErrQuotaExhausted) {
		t.Fatalf("err = %v, want ErrQuotaExhausted", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Error("other batches were not cancelled")
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
	ids := make([]int, 12) // three batches: two get the second page, whose rows must show once
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

// deadlines records how far off each request's context deadline is, by path.
type deadlines struct {
	mu sync.Mutex
	by map[string]time.Duration
}

func (d *deadlines) RoundTrip(req *http.Request) (*http.Response, error) {
	if dl, ok := req.Context().Deadline(); ok {
		d.mu.Lock()
		d.by[req.URL.Path] = time.Until(dl)
		d.mu.Unlock()
	}
	return http.DefaultTransport.RoundTrip(req)
}

func TestRequestDeadlines(t *testing.T) {
	s := marketotest.New(t)
	s.Handle(tokenPath, marketotest.Response{Body: "pagingtoken_ok.json"})
	s.Handle(actsPath, page("", false, "x"))
	d := &deadlines{by: map[string]time.Duration{}}
	if _, err := newHarness(s, Options{HTTP: &http.Client{Transport: d}}).c.Activities(context.Background(), 1, []int{1}, since); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]time.Duration{actsPath: 120 * time.Second, tokenPath: 30 * time.Second, marketotest.IdentityPath: 30 * time.Second} {
		if got := d.by[path]; got > want || got < want-5*time.Second {
			t.Errorf("%s deadline in %v, want about %v", path, got, want)
		}
	}
}

func TestActivitiesTimeoutScaled(t *testing.T) {
	old := activitiesTimeout
	activitiesTimeout = 50 * time.Millisecond
	defer func() { activitiesTimeout = old }()
	s := marketotest.New(t)
	s.Handle(tokenPath, marketotest.Response{Body: "pagingtoken_ok.json", Delay: 100 * time.Millisecond}) // under 30 s: fine
	s.Handle(actsPath, marketotest.Response{Body: "activities_final_page.json", Delay: 5 * time.Second})
	start := time.Now()
	_, err := newHarness(s, Options{}).c.Activities(context.Background(), 1, []int{1}, since)
	if err == nil || err.Error() != "GET /rest/v1/activities.json timed out: Marketo did not answer in time" {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("timeout not honoured")
	}
}

func TestActivitiesSlowPageProgress(t *testing.T) {
	old := slowPage
	slowPage = 20 * time.Millisecond
	defer func() { slowPage = old }()
	s := marketotest.New(t)
	s.Handle(tokenPath, marketotest.Response{Body: "pagingtoken_ok.json"})
	s.Handle(actsPath, marketotest.Response{Body: "activities_page_empty_more.json"},
		marketotest.Response{Body: "activities_final_page.json", Delay: 60 * time.Millisecond})
	var prog bytes.Buffer
	if _, err := newHarness(s, Options{Progress: &prog}).c.Activities(context.Background(), 1, []int{1}, since); err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`^scanning activity: page 2 took [0-9]+\.[0-9]s \(Marketo scans every activity of these types instance-wide; web visits and field changes are the slow ones\)\n$`)
	if !re.MatchString(prog.String()) {
		t.Errorf("progress = %q, want one line for page 2", prog.String())
	}
}

func TestActivitiesFastPageNoProgress(t *testing.T) {
	s := marketotest.New(t)
	s.Handle(tokenPath, marketotest.Response{Body: "pagingtoken_ok.json"})
	s.Handle(actsPath, marketotest.Response{Body: "activities_final_page.json"})
	var prog bytes.Buffer
	if _, err := newHarness(s, Options{Progress: &prog}).c.Activities(context.Background(), 1, []int{1}, since); err != nil {
		t.Fatal(err)
	}
	if prog.Len() != 0 {
		t.Errorf("progress = %q, want none", prog.String())
	}
}

func TestActivitiesDecodeCampaignID(t *testing.T) {
	s := marketotest.New(t)
	s.Handle(tokenPath, marketotest.Response{Body: "pagingtoken_ok.json"})
	s.Handle(actsPath, page(`{"id":1,"activityDate":"2026-10-01T00:00:00Z","activityTypeId":13,"campaignId":3001,"primaryAttributeValueId":2,"primaryAttributeValue":"Lead Status"},
{"id":2,"activityDate":"2026-10-02T00:00:00Z","activityTypeId":2,"primaryAttributeValue":"Form"}`, false, ""))
	acts, err := newHarness(s, Options{}).c.Activities(context.Background(), 1001, []int{2, 13}, since)
	if err != nil {
		t.Fatal(err)
	}
	if acts[0].CampaignID != 3001 || acts[0].PrimaryID != 2 {
		t.Errorf("acts[0] = %+v, want CampaignID 3001, PrimaryID 2", acts[0])
	}
	if acts[1].CampaignID != 0 || acts[1].PrimaryID != 0 {
		t.Errorf("acts[1] = %+v, want zero ids when absent", acts[1])
	}
}

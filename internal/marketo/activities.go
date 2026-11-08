// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package marketo

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// Marketo accepts up to 10 activity type ids per call, but mkto sends 4: on a real
	// instance a page of 10 types took about 29 s, a page of 4 or fewer about 2.8 s.
	typeBatchSize  = 4
	leadBatchSize  = 30   // Marketo accepts at most 30 lead ids per call
	parallelWalks  = 3    // batches paged at the same time
	maxPages       = 1000 // per batch; guards against paging that never ends
	activitiesPath = "/rest/v1/activities.json"
)

// slowPage is when a page of activities earns a progress line on Options.Progress.
var slowPage = 5 * time.Second

// ActivityType is one Marketo activity type. Primary is the primary attribute's
// name; Attributes are the other attribute names, in API order.
type ActivityType struct {
	ID          int
	Name        string
	Description string
	Primary     string
	Attributes  []string
}

// ActivityTypes fetches every activity type. Ids differ per instance, so
// callers resolve names to ids from this list and never hardcode them.
func (c *Client) ActivityTypes(ctx context.Context) ([]ActivityType, error) {
	var resp struct {
		Result []struct {
			ID               int    `json:"id"`
			Name             string `json:"name"`
			Description      string `json:"description"`
			PrimaryAttribute struct {
				Name string `json:"name"`
			} `json:"primaryAttribute"`
			Attributes []struct {
				Name string `json:"name"`
			} `json:"attributes"`
		} `json:"result"`
	}
	if err := c.get(ctx, "/rest/v1/activities/types.json", nil, &resp); err != nil {
		return nil, err
	}
	types := make([]ActivityType, len(resp.Result))
	for i, r := range resp.Result {
		attrs := make([]string, len(r.Attributes))
		for j, a := range r.Attributes {
			attrs[j] = a.Name
		}
		types[i] = ActivityType{r.ID, r.Name, r.Description, r.PrimaryAttribute.Name, attrs}
	}
	return types, nil
}

// ResolveTypes maps names to ids, ignoring case and surrounding space. Ids keep
// the order of names and are de-duplicated; unknown names keep input order.
func ResolveTypes(all []ActivityType, names []string) (ids []int, unknown []string) {
	byName := map[string]int{}
	for _, t := range all {
		byName[strings.ToLower(strings.TrimSpace(t.Name))] = t.ID
	}
	seen := map[int]bool{}
	for _, n := range names {
		id, ok := byName[strings.ToLower(strings.TrimSpace(n))]
		switch {
		case !ok:
			unknown = append(unknown, n)
		case !seen[id]:
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids, unknown
}

// Activity is one lead activity row.
type Activity struct {
	ID         int
	LeadID     int
	Date       time.Time
	TypeID     int
	Primary    string
	PrimaryID  int // primaryAttributeValueId: the asset or field behind Primary
	CampaignID int // campaignId (UNVERIFIED on a real instance); 0 when absent
	Attributes map[string]any
}

type activityPage struct {
	Result []struct {
		ID         int       `json:"id"`
		LeadID     int       `json:"leadId"`
		Date       time.Time `json:"activityDate"`
		TypeID     int       `json:"activityTypeId"`
		Primary    string    `json:"primaryAttributeValue"`
		PrimaryID  int       `json:"primaryAttributeValueId"`
		CampaignID int       `json:"campaignId"`
		Attributes []struct {
			Name  string `json:"name"`
			Value any    `json:"value"`
		} `json:"attributes"`
	} `json:"result"`
	MoreResult    bool   `json:"moreResult"`
	NextPageToken string `json:"nextPageToken"`
}

// Activities returns the lead's activities of the given types since a time, oldest first.
func (c *Client) Activities(ctx context.Context, leadID int, typeIDs []int, since time.Time) ([]Activity, error) {
	return c.ActivitiesForLeads(ctx, []int{leadID}, typeIDs, since)
}

// ActivitiesForLeads returns the leads' activities of the given types since a time,
// oldest first. Each call asks for at most 30 leads and 4 types. Batches run at most 3 at
// a time, each with its own paging token, and follow moreResult (pages can be empty).
// The first error stops the other batches and is returned.
func (c *Client) ActivitiesForLeads(ctx context.Context, leadIDs, typeIDs []int, since time.Time) ([]Activity, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex // guards acts and first
		acts  []Activity
		first error
		pages atomic.Int64 // across batches, for progress lines
	)
	slots := make(chan struct{}, parallelWalks)
	for leads := range slices.Chunk(leadIDs, leadBatchSize) {
		for types := range slices.Chunk(typeIDs, typeBatchSize) {
			slots <- struct{}{}
			wg.Go(func() {
				defer func() { <-slots }()
				var a []Activity
				err := ctx.Err() // a batch started after an error does nothing
				if err == nil {
					a, err = c.walk(ctx, leads, types, since, &pages)
				}
				mu.Lock()
				defer mu.Unlock()
				if err == nil {
					acts = append(acts, a...)
					return
				}
				// The first error wins, but never lose a real one to the cancellations it caused.
				if first == nil || errors.Is(first, context.Canceled) && !errors.Is(err, context.Canceled) {
					first = err
				}
				cancel()
			})
		}
	}
	wg.Wait()
	if first != nil {
		return nil, first
	}
	sort.SliceStable(acts, func(i, j int) bool {
		if !acts[i].Date.Equal(acts[j].Date) {
			return acts[i].Date.Before(acts[j].Date)
		}
		return acts[i].ID < acts[j].ID
	})
	// An activity has one type and one lead, so batches never share a row; drop repeats
	// anyway so a row can never show twice, whichever batch finished first.
	return slices.CompactFunc(acts, func(a, b Activity) bool { return a.ID == b.ID && a.Date.Equal(b.Date) }), nil
}

// walk pages through one batch of leads and types with a fresh paging token.
func (c *Client) walk(ctx context.Context, leads, types []int, since time.Time, total *atomic.Int64) ([]Activity, error) {
	var tok struct {
		NextPageToken string `json:"nextPageToken"`
	}
	q := url.Values{"sinceDatetime": {since.UTC().Format(time.RFC3339)}}
	if err := c.get(ctx, "/rest/v1/activities/pagingtoken.json", q, &tok); err != nil {
		return nil, err
	}
	var acts []Activity
	token := tok.NextPageToken
	for pages := 1; ; pages++ {
		q := url.Values{
			"nextPageToken":   {token},
			"activityTypeIds": {joinInts(types)},
			"leadIds":         {joinInts(leads)},
		}
		var p activityPage
		n := total.Add(1)
		began := time.Now()
		if err := c.get(ctx, activitiesPath, q, &p); err != nil {
			return nil, err
		}
		if d := time.Since(began); d > slowPage && c.o.Progress != nil {
			c.outMu.Lock()
			fmt.Fprintf(c.o.Progress, "scanning activity: page %d took %.1fs (Marketo scans every activity of these types instance-wide; web visits and field changes are the slow ones)\n", n, d.Seconds())
			c.outMu.Unlock()
		}
		for _, r := range p.Result {
			a := Activity{ID: r.ID, LeadID: r.LeadID, Date: r.Date, TypeID: r.TypeID, Primary: r.Primary, PrimaryID: r.PrimaryID, CampaignID: r.CampaignID, Attributes: map[string]any{}}
			for _, at := range r.Attributes {
				a.Attributes[at.Name] = at.Value
			}
			acts = append(acts, a)
		}
		if !p.MoreResult {
			return acts, nil
		}
		if p.NextPageToken == token || pages == maxPages {
			return nil, errors.New(pagingStuck)
		}
		token = p.NextPageToken
	}
}

func joinInts(ids []int) string {
	s := make([]string, len(ids))
	for i, id := range ids {
		s[i] = strconv.Itoa(id)
	}
	return strings.Join(s, ",")
}

const pagingStuck = "Marketo paging did not advance for GET /rest/v1/activities.json; stopping to protect your API quota"

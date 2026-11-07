// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package marketo

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	batchSize      = 10   // Marketo accepts at most 10 activity type ids per call
	maxPages       = 1000 // per batch; guards against paging that never ends
	activitiesPath = "/rest/v1/activities.json"
)

// slowPage is when a page of activities earns a progress line on Options.Progress.
var slowPage = 5 * time.Second

// ActivityType is one Marketo activity type.
type ActivityType struct {
	ID   int
	Name string
}

// ActivityTypes fetches every activity type. Ids differ per instance, so
// callers resolve names to ids from this list and never hardcode them.
func (c *Client) ActivityTypes(ctx context.Context) ([]ActivityType, error) {
	var resp struct {
		Result []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"result"`
	}
	if err := c.get(ctx, "/rest/v1/activities/types.json", nil, &resp); err != nil {
		return nil, err
	}
	types := make([]ActivityType, len(resp.Result))
	for i, r := range resp.Result {
		types[i] = ActivityType{r.ID, r.Name}
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
	Date       time.Time
	TypeID     int
	Primary    string
	Attributes map[string]any
}

type activityPage struct {
	Result []struct {
		ID         int       `json:"id"`
		Date       time.Time `json:"activityDate"`
		TypeID     int       `json:"activityTypeId"`
		Primary    string    `json:"primaryAttributeValue"`
		Attributes []struct {
			Name  string `json:"name"`
			Value any    `json:"value"`
		} `json:"attributes"`
	} `json:"result"`
	MoreResult    bool   `json:"moreResult"`
	NextPageToken string `json:"nextPageToken"`
}

// Activities returns the lead's activities of the given types since a time,
// oldest first. It asks for at most 10 types per batch, with a fresh paging
// token for each batch, and follows moreResult (pages can be empty).
func (c *Client) Activities(ctx context.Context, leadID int, typeIDs []int, since time.Time) ([]Activity, error) {
	var acts []Activity
	total := 0 // pages across batches, for progress lines
	for start := 0; start < len(typeIDs); start += batchSize {
		batch := typeIDs[start:min(start+batchSize, len(typeIDs))]
		ids := make([]string, len(batch))
		for i, id := range batch {
			ids[i] = strconv.Itoa(id)
		}
		var tok struct {
			NextPageToken string `json:"nextPageToken"`
		}
		q := url.Values{"sinceDatetime": {since.UTC().Format(time.RFC3339)}}
		if err := c.get(ctx, "/rest/v1/activities/pagingtoken.json", q, &tok); err != nil {
			return nil, err
		}
		token := tok.NextPageToken
		for pages := 1; ; pages++ {
			q := url.Values{
				"nextPageToken":   {token},
				"activityTypeIds": {strings.Join(ids, ",")},
				"leadIds":         {strconv.Itoa(leadID)},
			}
			var p activityPage
			total++
			began := time.Now()
			if err := c.get(ctx, activitiesPath, q, &p); err != nil {
				return nil, err
			}
			if d := time.Since(began); d > slowPage && c.o.Progress != nil {
				fmt.Fprintf(c.o.Progress, "scanning activity: page %d took %.1fs (web visits and link clicks are slow on large Marketo instances)\n", total, d.Seconds())
			}
			for _, r := range p.Result {
				a := Activity{ID: r.ID, Date: r.Date, TypeID: r.TypeID, Primary: r.Primary, Attributes: map[string]any{}}
				for _, at := range r.Attributes {
					a.Attributes[at.Name] = at.Value
				}
				acts = append(acts, a)
			}
			if !p.MoreResult {
				break
			}
			if p.NextPageToken == token || pages == maxPages {
				return nil, errors.New(pagingStuck)
			}
			token = p.NextPageToken
		}
	}
	sort.SliceStable(acts, func(i, j int) bool {
		if !acts[i].Date.Equal(acts[j].Date) {
			return acts[i].Date.Before(acts[j].Date)
		}
		return acts[i].ID < acts[j].ID
	})
	return acts, nil
}

const pagingStuck = "Marketo paging did not advance for GET /rest/v1/activities.json; stopping to protect your API quota"

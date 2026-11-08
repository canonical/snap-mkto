// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package marketo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Lead is one lead record as Marketo sends it. Numbers are json.Number.
type Lead map[string]any

// ID returns the lead id, or 0 if the record has none.
func (l Lead) ID() int {
	n, _ := strconv.Atoi(fmt.Sprint(l["id"]))
	return n
}

// ErrTooManyLeads means LeadsUpTo reached its page cap while Marketo still had more leads.
var ErrTooManyLeads = errors.New("more leads match than the page cap allows")

// Leads returns every lead whose filterType field matches one of values, following
// nextPageToken (paging for filter queries is unverified). Paging that doesn't advance is an error.
func (c *Client) Leads(ctx context.Context, filterType string, values []string, fields []string) ([]Lead, error) {
	return c.LeadsUpTo(ctx, filterType, values, fields, 0)
}

// LeadsUpTo is Leads that fetches at most pageCap pages (0: no cap) and returns
// ErrTooManyLeads if Marketo has more after them.
func (c *Client) LeadsUpTo(ctx context.Context, filterType string, values []string, fields []string, pageCap int) ([]Lead, error) {
	var leads []Lead
	token := ""
	for pages := 1; ; pages++ {
		q := url.Values{"filterType": {filterType}, "filterValues": {strings.Join(values, ",")}, "fields": {strings.Join(fields, ",")}}
		if token != "" {
			q.Set("nextPageToken", token)
		}
		var resp struct {
			Result        []json.RawMessage `json:"result"`
			MoreResult    bool              `json:"moreResult"`
			NextPageToken string            `json:"nextPageToken"`
		}
		if err := c.get(ctx, "/rest/v1/leads.json", q, &resp); err != nil {
			return nil, err
		}
		for _, raw := range resp.Result {
			var rec struct {
				Status  string `json:"status"`
				Reasons []struct{ Code, Message string }
			}
			if err := json.Unmarshal(raw, &rec); err != nil {
				return nil, fmt.Errorf("decoding Marketo lead: %v", err)
			}
			if rec.Status == "skipped" {
				for _, r := range rec.Reasons {
					if r.Code == "1006" {
						return nil, &FieldError{Field: fieldName(r.Message)}
					}
				}
				return nil, fmt.Errorf("Marketo skipped a lead record")
			}
			var l Lead
			d := json.NewDecoder(bytes.NewReader(raw))
			d.UseNumber()
			if err := d.Decode(&l); err != nil {
				return nil, fmt.Errorf("decoding Marketo lead: %v", err)
			}
			leads = append(leads, l)
		}
		if !resp.MoreResult || resp.NextPageToken == "" || len(resp.Result) == 0 {
			return leads, nil
		}
		if resp.NextPageToken == token || pages == maxPages { // a short list would pass for a complete one
			return nil, errors.New("Marketo paging did not advance for GET /rest/v1/leads.json; stopping to protect your API quota")
		}
		if pages == pageCap {
			return nil, ErrTooManyLeads
		}
		token = resp.NextPageToken
	}
}

// ListMembership is one static list the lead is on. Added is createdAt, zero if it
// doesn't parse (the date format is UNVERIFIED).
type ListMembership struct {
	ListID int
	Added  time.Time
}

// ListMemberships returns the static lists the lead is on, following nextPageToken.
// Paging that doesn't advance is an error.
func (c *Client) ListMemberships(ctx context.Context, leadID int) ([]ListMembership, error) {
	var out []ListMembership
	token := ""
	for pages := 1; ; pages++ {
		var q url.Values
		if token != "" {
			q = url.Values{"nextPageToken": {token}}
		}
		var resp struct {
			Result []struct {
				ListID    int    `json:"listId"`
				CreatedAt string `json:"createdAt"`
			} `json:"result"`
			MoreResult    bool   `json:"moreResult"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := c.get(ctx, fmt.Sprintf("/rest/v1/leads/%d/listMembership.json", leadID), q, &resp); err != nil {
			return nil, err
		}
		for _, r := range resp.Result {
			added, _ := parseTime(r.CreatedAt)
			out = append(out, ListMembership{r.ListID, added})
		}
		if !resp.MoreResult || resp.NextPageToken == "" || len(resp.Result) == 0 {
			return out, nil
		}
		if resp.NextPageToken == token || pages == maxPages {
			return nil, errors.New("Marketo paging did not advance for GET /rest/v1/leads/{id}/listMembership.json; stopping to protect your API quota")
		}
		token = resp.NextPageToken
	}
}

// Field is one lead field from describe. Name is the REST name, "" when the
// field can't be used over REST (describe sent no rest child).
type Field struct {
	Name, Label, Type string
	Length            int
	ReadOnly          bool
}

// DescribeLeads lists every lead field on the instance (describe is not paged).
func (c *Client) DescribeLeads(ctx context.Context) ([]Field, error) {
	var resp struct {
		Result []struct {
			DisplayName string `json:"displayName"`
			DataType    string `json:"dataType"`
			Length      int    `json:"length"`
			Rest        struct {
				Name     string `json:"name"`
				ReadOnly bool   `json:"readOnly"`
			} `json:"rest"`
		} `json:"result"`
	}
	if err := c.get(ctx, "/rest/v1/leads/describe.json", nil, &resp); err != nil {
		return nil, err
	}
	fields := make([]Field, len(resp.Result))
	for i, r := range resp.Result {
		fields[i] = Field{r.Rest.Name, r.DisplayName, r.DataType, r.Length, r.Rest.ReadOnly}
	}
	return fields, nil
}

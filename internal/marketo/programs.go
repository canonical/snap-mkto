// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package marketo

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"
)

// Membership is one program membership of a lead. Date is membershipDate, else
// updatedAt, else zero. The response shape is UNVERIFIED against a real instance
// (see testdata/marketo/README.md).
type Membership struct {
	ProgramID int
	Status    string
	Date      time.Time
}

// Memberships returns the lead's program memberships. Paging is unverified,
// so nextPageToken is followed only if Marketo sends one with moreResult (and never repeats).
func (c *Client) Memberships(ctx context.Context, leadID int) ([]Membership, error) {
	var out []Membership
	token := ""
	for pages := 1; ; pages++ {
		var q url.Values
		if token != "" {
			q = url.Values{"nextPageToken": {token}}
		}
		var resp struct {
			Result []struct {
				ID                int    `json:"id"`
				ProgressionStatus string `json:"progressionStatus"`
				MembershipDate    string `json:"membershipDate"`
				UpdatedAt         string `json:"updatedAt"`
			} `json:"result"`
			MoreResult    bool   `json:"moreResult"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := c.get(ctx, fmt.Sprintf("/rest/v1/leads/%d/programMembership.json", leadID), q, &resp); err != nil {
			return nil, err
		}
		for _, r := range resp.Result {
			d, err := parseTime(r.MembershipDate)
			if err != nil {
				d, _ = parseTime(r.UpdatedAt) // zero if neither parses: dates only order and window the list
			}
			out = append(out, Membership{r.ID, r.ProgressionStatus, d})
		}
		if !resp.MoreResult || resp.NextPageToken == "" || resp.NextPageToken == token || len(resp.Result) == 0 || pages == maxPages {
			return out, nil
		}
		token = resp.NextPageToken
	}
}

// Program is one Marketo program.
type Program struct {
	ID                             int
	Name, Type, Channel, Workspace string
	CreatedAt                      time.Time
}

// Program fetches one program by id. createdAt is not decoded (CreatedAt stays zero):
// callers only need the name, and a date-shape change once broke every lookup.
func (c *Client) Program(ctx context.Context, id int) (Program, error) {
	var resp struct {
		Result []struct {
			ID        int    `json:"id"`
			Name      string `json:"name"`
			Type      string `json:"type"`
			Channel   string `json:"channel"`
			Workspace string `json:"workspace"`
		} `json:"result"`
	}
	if err := c.get(ctx, fmt.Sprintf("/rest/asset/v1/program/%d.json", id), nil, &resp); err != nil {
		return Program{}, err
	}
	if len(resp.Result) == 0 {
		return Program{}, errors.New("Marketo returned no program " + fmt.Sprint(id))
	}
	r := resp.Result[0]
	return Program{ID: r.ID, Name: r.Name, Type: r.Type, Channel: r.Channel, Workspace: r.Workspace}, nil
}

// Programs lists every program, 200 per page by offset, until Marketo returns an empty page
// (code 702 is not relied on).
func (c *Client) Programs(ctx context.Context) ([]Program, error) {
	var out []Program
	for pages := 0; pages < maxPages; pages++ {
		var resp struct {
			Result []struct {
				ID        int     `json:"id"`
				Name      string  `json:"name"`
				Type      string  `json:"type"`
				Channel   string  `json:"channel"`
				Workspace string  `json:"workspace"`
				CreatedAt apiTime `json:"createdAt"`
			} `json:"result"`
		}
		q := url.Values{"maxReturn": {"200"}, "offset": {fmt.Sprint(len(out))}}
		if err := c.get(ctx, "/rest/asset/v1/programs.json", q, &resp); err != nil {
			return nil, err
		}
		if len(resp.Result) == 0 {
			return out, nil
		}
		for _, r := range resp.Result {
			out = append(out, Program{r.ID, r.Name, r.Type, r.Channel, r.Workspace, time.Time(r.CreatedAt)})
		}
	}
	return nil, fmt.Errorf("stopped after %d pages of programs; Marketo keeps returning rows", maxPages)
}

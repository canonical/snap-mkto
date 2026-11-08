// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package marketo

import (
	"context"
	"time"
)

// UsageDay is the API call count for one calendar day (Marketo's date, US Central).
// Users is the per-API-user split; usage.json reports it, last7days.json doesn't.
type UsageDay struct {
	Date  string
	Total int
	Users []UserUsage
}

// UserUsage is one API user's calls; UserID is the API user's email (a service account).
type UserUsage struct {
	UserID string `json:"userId"`
	Count  int    `json:"count"`
}

// ErrorCount is how often Marketo returned one error code on a day.
type ErrorCount struct {
	Code  string `json:"errorCode"`
	Count int    `json:"count"`
}

func (c *Client) usage(ctx context.Context, path string) ([]UsageDay, error) {
	var resp struct {
		Result []struct {
			Date  string      `json:"date"`
			Total int         `json:"total"`
			Users []UserUsage `json:"users"`
		} `json:"result"`
	}
	if err := c.get(ctx, path, nil, &resp); err != nil {
		return nil, err
	}
	out := make([]UsageDay, len(resp.Result))
	for i, r := range resp.Result {
		out[i] = UsageDay{r.Date, r.Total, r.Users}
	}
	return out, nil
}

// today is the America/Chicago date of the client's clock (the quota resets at midnight Central).
func (c *Client) today() (string, error) {
	chicago, err := time.LoadLocation("America/Chicago")
	if err != nil {
		return "", err
	}
	return c.o.Now().In(chicago).Format("2006-01-02"), nil
}

// UsageToday returns today's calls and their per-user split (see today). No row for that
// day means zero calls.
func (c *Client) UsageToday(ctx context.Context) (UsageDay, error) {
	date, err := c.today()
	if err != nil {
		return UsageDay{}, err
	}
	day := UsageDay{Date: date}
	rows, err := c.usage(ctx, "/rest/v1/stats/usage.json")
	if err != nil {
		return UsageDay{}, err
	}
	for _, r := range rows {
		if r.Date == day.Date {
			day.Total += r.Total
			day.Users = append(day.Users, r.Users...)
		}
	}
	return day, nil
}

// ErrorsToday returns today's error counts by code (see today), in Marketo's order.
func (c *Client) ErrorsToday(ctx context.Context) ([]ErrorCount, error) {
	date, err := c.today()
	if err != nil {
		return nil, err
	}
	var resp struct {
		Result []struct {
			Date   string       `json:"date"`
			Errors []ErrorCount `json:"errors"`
		} `json:"result"`
	}
	if err := c.get(ctx, "/rest/v1/stats/errors.json", nil, &resp); err != nil {
		return nil, err
	}
	var out []ErrorCount
	for _, r := range resp.Result {
		if r.Date == date {
			out = append(out, r.Errors...)
		}
	}
	return out, nil
}

// UsageLast7Days returns the daily totals Marketo reports for the last seven days.
func (c *Client) UsageLast7Days(ctx context.Context) ([]UsageDay, error) {
	return c.usage(ctx, "/rest/v1/stats/usage/last7days.json")
}

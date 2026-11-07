// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package marketo

import (
	"context"
	"time"
)

// UsageDay is the API call count for one calendar day (Marketo's date, US Central).
type UsageDay struct {
	Date  string
	Total int
}

func (c *Client) usage(ctx context.Context, path string) ([]UsageDay, error) {
	var resp struct {
		Result []struct {
			Date  string `json:"date"`
			Total int    `json:"total"`
		} `json:"result"`
	}
	if err := c.get(ctx, path, nil, &resp); err != nil {
		return nil, err
	}
	out := make([]UsageDay, len(resp.Result))
	for i, r := range resp.Result {
		out[i] = UsageDay{r.Date, r.Total}
	}
	return out, nil
}

// UsageToday returns today's calls, where today is the America/Chicago date of the client's
// clock (the quota resets at midnight Central). No row for that day means zero calls.
func (c *Client) UsageToday(ctx context.Context) (UsageDay, error) {
	chicago, err := time.LoadLocation("America/Chicago")
	if err != nil {
		return UsageDay{}, err
	}
	day := UsageDay{Date: c.o.Now().In(chicago).Format("2006-01-02")}
	rows, err := c.usage(ctx, "/rest/v1/stats/usage.json")
	if err != nil {
		return UsageDay{}, err
	}
	for _, r := range rows {
		if r.Date == day.Date {
			day.Total += r.Total
		}
	}
	return day, nil
}

// UsageLast7Days returns the daily totals Marketo reports for the last seven days.
func (c *Client) UsageLast7Days(ctx context.Context) ([]UsageDay, error) {
	return c.usage(ctx, "/rest/v1/stats/usage/last7days.json")
}

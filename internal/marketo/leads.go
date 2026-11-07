// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package marketo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Lead is one lead record as Marketo sends it. Numbers are json.Number.
type Lead map[string]any

// ID returns the lead id, or 0 if the record has none.
func (l Lead) ID() int {
	n, _ := strconv.Atoi(fmt.Sprint(l["id"]))
	return n
}

// LeadsByEmail returns every lead with this email (0, 1 or several duplicates).
func (c *Client) LeadsByEmail(ctx context.Context, email string, fields []string) ([]Lead, error) {
	q := url.Values{"filterType": {"email"}, "filterValues": {email}, "fields": {strings.Join(fields, ",")}}
	var resp struct {
		Result []json.RawMessage `json:"result"`
	}
	if err := c.get(ctx, "/rest/v1/leads.json", q, &resp); err != nil {
		return nil, err
	}
	leads := make([]Lead, 0, len(resp.Result))
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
	return leads, nil
}

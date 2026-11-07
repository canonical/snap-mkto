// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package marketo

import (
	"encoding/json"
	"time"
)

// timeLayouts are the date forms Marketo sends. The asset API writes a literal Z
// followed by an offset ("2026-11-01T01:08:44Z+0000"), seen on a real instance 2026-11-07.
var timeLayouts = []string{time.RFC3339, "2006-01-02T15:04:05Z-0700", "2006-01-02T15:04:05-0700"}

// parseTime parses a Marketo date in any of timeLayouts and returns it in UTC.
func parseTime(s string) (time.Time, error) {
	var err error
	for _, l := range timeLayouts {
		var t time.Time
		if t, err = time.Parse(l, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, err
}

// apiTime decodes a Marketo date with parseTime; null or "" stay zero.
type apiTime time.Time

func (a *apiTime) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil || s == "" {
		return err
	}
	t, err := parseTime(s)
	*a = apiTime(t)
	return err
}

// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/canonical/snap-mkto/internal/marketotest"
)

const errorsPath = "/rest/v1/stats/errors.json"

// nov8 is a US Central 2026-11-08, the date in usage_today_users.json and stats_errors.json.
var nov8 = func() time.Time { return time.Date(2026, 11, 8, 18, 0, 0, 0, time.UTC) }

var configPathRE = regexp.MustCompile(`in \S+config\.toml;`)

func TestQuotaByUserGolden(t *testing.T) {
	s := quotaServer(t, "usage_today_users.json")
	out, _, code := fakeCLINow(t, s, nov8, "quota", "--by-user")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	golden(t, "quota_by_user.txt", configPathRE.ReplaceAllString(out, "in <config>;"))
	out, _, _ = fakeCLINow(t, s, nov8, "quota", "--by-user", "--json")
	if !strings.Contains(out, `"byUser": [`) || !strings.Contains(out, `"userId": "api-user@example.com",`) || strings.Contains(out, `"errors"`) {
		t.Errorf("JSON:\n%s", out)
	}
	out, _, _ = fakeCLINow(t, s, nov8, "quota", "--json")
	if strings.Contains(out, `"byUser"`) {
		t.Errorf("byUser without --by-user:\n%s", out)
	}

	// Sorted by calls desc, then id.
	s = quotaServer(t, `{"success":true,"result":[{"date":"2026-11-08","total":9,"users":[`+
		`{"userId":"c@example.com","count":2},{"userId":"b@example.com","count":5},{"userId":"a@example.com","count":2}]}]}`)
	out, _, _ = fakeCLINow(t, s, nov8, "quota", "--by-user")
	if !regexp.MustCompile(`(?s)b@example\.com +5\na@example\.com +2\nc@example\.com +2\n`).MatchString(out) {
		t.Errorf("order:\n%s", out)
	}
}

func TestQuotaByUserNoExtraCall(t *testing.T) {
	hits := func(args ...string) [2]int {
		s := quotaServer(t, "usage_today_users.json")
		if _, _, code := fakeCLINow(t, s, nov8, args...); code != 0 {
			t.Fatalf("%v: exit %d", args, code)
		}
		return [2]int{s.Hits(usagePath), s.Hits(usage7Path)}
	}
	if plain, byUser := hits("quota"), hits("quota", "--by-user"); plain != byUser {
		t.Errorf("hits plain %v, --by-user %v", plain, byUser)
	}
}

func TestQuotaErrorsGolden(t *testing.T) {
	s := quotaServer(t, "usage_today_users.json")
	s.Handle(errorsPath, marketotest.Response{Body: "stats_errors.json"})
	out, _, code := fakeCLINow(t, s, nov8, "quota", "--errors")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	golden(t, "quota_errors.txt", configPathRE.ReplaceAllString(out, "in <config>;"))
	if s.Hits(errorsPath) != 1 {
		t.Errorf("errors.json hits = %d", s.Hits(errorsPath))
	}
	out, _, _ = fakeCLINow(t, s, nov8, "quota", "--errors", "--json")
	if !strings.Contains(out, `"code": "1006",`) || strings.Contains(out, `"byUser"`) {
		t.Errorf("JSON:\n%s", out)
	}

	// Yesterday's errors don't count today.
	s = quotaServer(t, "usage_today_users.json")
	s.Handle(errorsPath, marketotest.Response{Body: `{"success":true,"result":[{"date":"2026-11-07","total":3,"errors":[{"errorCode":"606","count":3}]}]}`})
	out, _, _ = fakeCLINow(t, s, nov8, "quota", "--errors")
	if !strings.HasSuffix(out, "\nNo API errors today.\n") {
		t.Errorf("got:\n%s", out)
	}
	out, _, _ = fakeCLINow(t, s, nov8, "quota", "--errors", "--json")
	if !strings.Contains(out, `"errors": []`) {
		t.Errorf("JSON:\n%s", out)
	}
}

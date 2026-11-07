// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package redact

import "testing"

func TestTitleBucket(t *testing.T) {
	for _, tc := range []struct{ title, want string }{
		{"VP of Platform Engineering", "senior engineering leader"},
		{"Data Scientist", "individual contributor, data"},
		{"Chief Information Security Officer", "senior security leader"},
		{"CTO, Platform", "senior engineering leader"},
		{"Vice President, Sales", "senior sales leader"},
		{"Head of IT", "senior IT leader"},
		{"Director of Infrastructure", "senior IT leader"},
		{"Engineering Manager", "mid-level engineering manager"},
		{"Team Lead, DevOps", "mid-level engineering manager"},
		{"Analytics Manager", "mid-level data manager"},
		{"Senior Software Engineer", "individual contributor, engineering"},
		{"SRE", "individual contributor, engineering"},
		{"Sysadmin", "individual contributor, IT"},
		{"Marketing Specialist", "individual contributor, marketing"},
		{"Account Executive", "individual contributor, sales"},
		{"Product Owner", "individual contributor, product"},
		{"SVP Engineering", "senior engineering leader"},
		{"EVP, Sales", "senior sales leader"},
		{"President", "unknown role"},
		{"Chief Technology Officer", "senior IT leader"},
		{"Platform Marketing Manager", "mid-level marketing manager"},
		{"Chief Happiness Officer", "unknown role"},
		{"Office Manager", "unknown role"},
		{"Owner", "unknown role"},
		{"", "unknown role"},
		{"   ", "unknown role"},
	} {
		if got := TitleBucket(tc.title); got != tc.want {
			t.Errorf("TitleBucket(%q) = %q, want %q", tc.title, got, tc.want)
		}
	}
}

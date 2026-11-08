// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

const valid = `[marketo]
base_url = "https://x.mktorest.com"
client_id = "id"
client_secret = "s3cret"
`

func write(t *testing.T, body string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestPathOrder(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	cases := []struct {
		name, flag string
		env        map[string]string
		want       string
	}{
		{"flag wins", "/f.toml", map[string]string{"MKTO_CONFIG": "/e.toml", "SNAP_USER_COMMON": "/snap"}, "/f.toml"},
		{"env", "", map[string]string{"MKTO_CONFIG": "/e.toml", "SNAP_USER_COMMON": "/snap"}, "/e.toml"},
		{"snap", "", map[string]string{"SNAP_USER_COMMON": "/snap"}, "/snap/config.toml"},
		{"user config dir", "", nil, "/xdg/mkto/config.toml"},
	}
	for _, c := range cases {
		got, err := Path(c.flag, env(c.env))
		if err != nil || got != c.want {
			t.Errorf("%s: got %q, %v; want %q", c.name, got, err, c.want)
		}
	}
}

func TestEnvOverrides(t *testing.T) {
	p := write(t, valid, 0o600)
	c, err := Load(p, env(map[string]string{
		"MKTO_BASE_URL": "https://y.mktorest.com", "MKTO_CLIENT_ID": "id2", "MKTO_CLIENT_SECRET": "sec2",
	}), &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	m := c.Marketo
	if m.BaseURL != "https://y.mktorest.com" || m.ClientID != "id2" || m.ClientSecret != "sec2" {
		t.Errorf("overrides not applied: %+v", m.BaseURL)
	}
}

func TestEnvFillsMissingKeys(t *testing.T) {
	p := write(t, "[marketo]\n", 0o600)
	_, err := Load(p, env(map[string]string{
		"MKTO_BASE_URL": "https://y.mktorest.com", "MKTO_CLIENT_ID": "a", "MKTO_CLIENT_SECRET": "b",
	}), &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDefaults(t *testing.T) {
	c, err := Load(write(t, valid, 0o600), env(nil), &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if c.Marketo.DailyQuota != 50000 || len(c.Marketo.LeadFields) == 0 || len(c.Marketo.ActivityTypes) == 0 || c.Output.Timezone != "Local" {
		t.Errorf("defaults missing: %+v", c)
	}
}

func TestNormaliseBaseURL(t *testing.T) {
	ok := []string{
		"https://x.mktorest.com", "https://x.mktorest.com/", "https://x.mktorest.com/rest",
		"https://x.mktorest.com/rest/", "  https://x.mktorest.com/rest/  ",
	}
	for _, in := range ok {
		c, err := Load(write(t, strings.Replace(valid, "https://x.mktorest.com", in, 1), 0o600), env(nil), &bytes.Buffer{})
		if err != nil || c.Marketo.BaseURL != "https://x.mktorest.com" {
			t.Errorf("%q: got %q, %v", in, c.Marketo.BaseURL, err)
		}
	}
	for _, in := range []string{"x.mktorest.com", "http://x.mktorest.com", "https://"} {
		_, err := Load(write(t, strings.Replace(valid, "https://x.mktorest.com", in, 1), 0o600), env(nil), &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "base_url") {
			t.Errorf("%q: want base_url error, got %v", in, err)
		}
	}
}

func TestMissingKey(t *testing.T) {
	p := write(t, strings.Replace(valid, "s3cret", "", 1), 0o600)
	_, err := Load(p, env(nil), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "client_secret") || !strings.Contains(err.Error(), p) {
		t.Errorf("err = %v", err)
	}
}

func TestMissingFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nope.toml")
	_, err := Load(p, env(nil), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "mkto init") || !strings.Contains(err.Error(), p) {
		t.Errorf("err = %v", err)
	}
}

func TestPermissionWarning(t *testing.T) {
	var w bytes.Buffer
	p := write(t, valid, 0o644)
	if _, err := Load(p, env(nil), &w); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.String(), "chmod 600 "+p) {
		t.Errorf("warning = %q", w.String())
	}
	w.Reset()
	p = write(t, valid, 0o600)
	if _, err := Load(p, env(nil), &w); err != nil || w.Len() != 0 {
		t.Errorf("err %v, warning %q", err, w.String())
	}
}

func TestBadTimezone(t *testing.T) {
	body := valid + "[output]\ntimezone = \"Mars/Olympus\"\n"
	p := write(t, body, 0o600)
	_, err := Load(p, env(nil), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "timezone") {
		t.Errorf("err = %v", err)
	}
}

func TestLocation(t *testing.T) {
	c := &Config{Output: Output{Timezone: "Asia/Tbilisi"}}
	loc, err := c.Location()
	if err != nil || loc.String() != "Asia/Tbilisi" {
		t.Errorf("got %v, %v", loc, err)
	}
}

func TestTemplateParses(t *testing.T) {
	body := strings.Replace(string(template), "123-ABC-456", "x", 1)
	body = strings.Replace(body, `client_id = ""`, `client_id = "a"`, 1)
	body = strings.Replace(body, `client_secret = ""`, `client_secret = "b"`, 1)
	if _, err := Load(write(t, body, 0o600), env(nil), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
}

func TestWriteTemplate(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "config.toml")
	if err := WriteTemplate(p, false); err != nil {
		t.Fatal(err)
	}
	if err := WriteTemplate(p, false); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Errorf("want --force error, got %v", err)
	}
	if err := WriteTemplate(p, true); err != nil {
		t.Fatal(err)
	}
}

func TestLLMAPIKeyEnvOverride(t *testing.T) {
	p := write(t, valid+"\n[llm]\napi_key = \"from-file\"\n", 0o600)
	c, err := Load(p, env(map[string]string{"MKTO_LLM_API_KEY": "from-env"}), &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if c.LLM.APIKey != "from-env" {
		t.Error("MKTO_LLM_API_KEY did not override llm.api_key")
	}
}

func TestLLMModelDefault(t *testing.T) {
	p := write(t, valid+"\n[llm]\nenabled = true\n", 0o600)
	c, err := Load(p, env(nil), &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if c.LLM.Model != "claude-sonnet-5-5" {
		t.Errorf("llm.model = %q, want the template default claude-sonnet-5-5", c.LLM.Model)
	}
}

func loadTypes(t *testing.T, line string) Marketo {
	t.Helper()
	c, err := Load(write(t, valid+line, 0o600), env(nil), &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	return c.Marketo
}

func TestLegacyActivityTypesMigrated(t *testing.T) {
	for _, line := range []string{
		"", // key absent: defaulted, not migrated
		`activity_types = ["Visit Webpage", "Fill Out Form", "Click Link", "Email Delivered", "Open Email", "Click Email", "Unsubscribe Email", "Change Status in Progression", "New Lead"]`,
		`activity_types = ["Visit Webpage", "Fill Out Form", "Click Link", "Email Delivered", "Open Email", "Click Email", "Unsubscribe Email", "Change Status in Progression", "Interesting Moment", "New Lead"]`,
		`activity_types = [" visit webpage", "FILL OUT FORM ", "Click Link", "Email Delivered", "Open Email", "Click Email", "Unsubscribe Email", "Change Status in Progression", "New Lead"]`,
	} {
		m := loadTypes(t, line+"\n")
		if !slices.Equal(m.ActivityTypes, DefaultActivityTypes) || !m.ActivityTypesDefaulted || m.ActivityTypesMigrated != (line != "") {
			t.Errorf("%q: types %v, defaulted %t, migrated %t", line, m.ActivityTypes, m.ActivityTypesDefaulted, m.ActivityTypesMigrated)
		}
	}
	if m := loadTypes(t, ""); !slices.Equal(m.LeadFields, DefaultLeadFields) {
		t.Errorf("lead_fields = %v", m.LeadFields)
	}
}

func TestCustomActivityTypesKept(t *testing.T) {
	for _, line := range []string{
		// legacy list reordered, one short, one extra: all hand-edited
		`activity_types = ["Fill Out Form", "Visit Webpage", "Click Link", "Email Delivered", "Open Email", "Click Email", "Unsubscribe Email", "Change Status in Progression", "New Lead"]`,
		`activity_types = ["Visit Webpage", "Fill Out Form", "Click Link", "Email Delivered", "Open Email", "Click Email", "Unsubscribe Email", "Change Status in Progression"]`,
		`activity_types = ["Visit Webpage", "Fill Out Form", "Click Link", "Email Delivered", "Open Email", "Click Email", "Unsubscribe Email", "Change Status in Progression", "New Lead", "Change Score"]`,
		`activity_types = ["Open Email"]`,
	} {
		var want struct{ Marketo Marketo }
		if err := toml.Unmarshal([]byte("[marketo]\n"+line), &want); err != nil {
			t.Fatal(err)
		}
		m := loadTypes(t, line+"\n")
		if !slices.Equal(m.ActivityTypes, want.Marketo.ActivityTypes) || m.ActivityTypesDefaulted || m.ActivityTypesMigrated {
			t.Errorf("%q: types %v, defaulted %t, migrated %t", line, m.ActivityTypes, m.ActivityTypesDefaulted, m.ActivityTypesMigrated)
		}
	}
}

// TestTemplateDefaultsCommented: the template leaves the lists commented out, and the
// commented lists are the Go defaults, so `mkto init` shows what the user gets.
func TestTemplateDefaultsCommented(t *testing.T) {
	var parsed Config
	if err := toml.Unmarshal(template, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Marketo.LeadFields != nil || parsed.Marketo.ActivityTypes != nil || parsed.Marketo.ProfileFields != nil || parsed.Marketo.ChangeFields != nil {
		t.Error("template sets lead_fields, activity_types, profile_fields or change_fields; they must stay commented out")
	}
	var block []string
	in := false
	for _, l := range strings.Split(string(template), "\n") {
		s, ok := strings.CutPrefix(l, "# ")
		if ok && (strings.HasPrefix(s, "lead_fields =") || strings.HasPrefix(s, "activity_types =") || strings.HasPrefix(s, "profile_fields =") || strings.HasPrefix(s, "change_fields =")) {
			in = true
		}
		if in {
			block = append(block, s)
			in = !strings.Contains(s, "]")
		}
	}
	var got Marketo
	if err := toml.Unmarshal([]byte(strings.Join(block, "\n")), &got); err != nil {
		t.Fatalf("commented lists don't parse: %v\n%s", err, strings.Join(block, "\n"))
	}
	if !slices.Equal(got.LeadFields, DefaultLeadFields) || !slices.Equal(got.ActivityTypes, DefaultActivityTypes) ||
		!slices.Equal(got.ProfileFields, DefaultProfileFields) || !slices.Equal(got.ChangeFields, DefaultChangeFields) {
		t.Errorf("commented lists %v / %v / %v / %v differ from the Go defaults", got.LeadFields, got.ActivityTypes, got.ProfileFields, got.ChangeFields)
	}
}

// TestChangeFieldsAbsentVsEmpty: absent means the defaults; an explicit [] shows every field.
func TestChangeFieldsAbsentVsEmpty(t *testing.T) {
	if got := loadTypes(t, "").ChangeFields; !slices.Equal(got, []string{"Lead Status", "Lead Score", "Company Name"}) {
		t.Errorf("absent: %v", got)
	}
	if got := loadTypes(t, "change_fields = []\n").ChangeFields; got == nil || len(got) != 0 {
		t.Errorf("empty: %v (nil=%t)", got, got == nil)
	}
	if got := loadTypes(t, "change_fields = [\"Job Title\"]\n").ChangeFields; !slices.Equal(got, []string{"Job Title"}) {
		t.Errorf("custom: %v", got)
	}
}

// TestDefaultProfileFieldsOwner: the owner default is the Salesforce owner id (T42).
func TestDefaultProfileFieldsOwner(t *testing.T) {
	if !slices.Contains(DefaultProfileFields, "sfdcLeadOwnerId") || slices.Contains(DefaultProfileFields, "leadOwnerEmailAddress") {
		t.Errorf("DefaultProfileFields = %v", DefaultProfileFields)
	}
}

// TestProfileFieldsAbsentVsEmpty: absent means the defaults; an explicit [] turns them off.
func TestProfileFieldsAbsentVsEmpty(t *testing.T) {
	if got := loadTypes(t, "").ProfileFields; !slices.Equal(got, DefaultProfileFields) {
		t.Errorf("absent: %v", got)
	}
	if got := loadTypes(t, "profile_fields = []\n").ProfileFields; got == nil || len(got) != 0 {
		t.Errorf("empty: %v (nil=%t)", got, got == nil)
	}
	if got := loadTypes(t, "profile_fields = [\"leadScore\"]\n").ProfileFields; !slices.Equal(got, []string{"leadScore"}) {
		t.Errorf("custom: %v", got)
	}
}

func TestAllMissingKeysListed(t *testing.T) {
	p := write(t, "[marketo]\n", 0o600)
	_, err := Load(p, env(nil), &bytes.Buffer{})
	want := strings.Join([]string{
		"config key marketo.base_url is not set: edit " + p + " (or set the MKTO_BASE_URL environment variable)",
		"config key marketo.client_id is not set: edit " + p + " (or set the MKTO_CLIENT_ID environment variable)",
		"config key marketo.client_secret is not set: edit " + p + " (or set the MKTO_CLIENT_SECRET environment variable)",
	}, "\n")
	if err == nil || err.Error() != want {
		t.Errorf("err = %v\nwant:\n%s", err, want)
	}
}

func TestPlaceholderBaseURL(t *testing.T) {
	for _, base := range []string{"https://123-ABC-456.mktorest.com", "https://123-abc-456.mktorest.com/rest"} {
		p := write(t, strings.Replace(valid, "https://x.mktorest.com", base, 1), 0o600)
		_, err := Load(p, env(nil), &bytes.Buffer{})
		want := "config key marketo.base_url in " + p + " is still the template placeholder: set your REST endpoint from Admin > Web Services"
		if err == nil || err.Error() != want {
			t.Errorf("%s: err = %v", base, err)
		}
	}
	// the untouched template reports the placeholder and both empty credentials at once
	_, err := Load(write(t, string(template), 0o600), env(nil), &bytes.Buffer{})
	if err == nil || strings.Count(err.Error(), "\n") != 2 || !strings.Contains(err.Error(), "placeholder") {
		t.Errorf("template: err = %v", err)
	}
}

func TestDailyQuotaTypeError(t *testing.T) {
	for _, v := range []string{`"50k"`, "1.5"} {
		p := write(t, valid+"daily_quota = "+v+"\n", 0o600)
		_, err := Load(p, env(nil), &bytes.Buffer{})
		if want := "config key marketo.daily_quota in " + p + " must be a whole number, e.g. 50000"; err == nil || err.Error() != want {
			t.Errorf("%s: err = %v", v, err)
		}
	}
	for _, body := range []string{valid + "daily_quota = 50k\n", valid + "client_id = 5\n"} {
		_, err := Load(write(t, body, 0o600), env(nil), &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "is not valid TOML") {
			t.Errorf("%q: err = %v", body, err)
		}
	}
}

func TestNormaliseRestV1(t *testing.T) {
	for _, in := range []string{"https://x.mktorest.com/rest/v1", "https://x.mktorest.com/rest/v1/"} {
		c, err := Load(write(t, strings.Replace(valid, "https://x.mktorest.com", in, 1), 0o600), env(nil), &bytes.Buffer{})
		if err != nil || c.Marketo.BaseURL != "https://x.mktorest.com" {
			t.Errorf("%q: got %v", in, err)
		}
	}
	_, err := Load(write(t, strings.Replace(valid, "https://x.mktorest.com", "https://x.mktorest.com/rest/v2", 1), 0o600), env(nil), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "base_url") {
		t.Errorf("/rest/v2: err = %v", err)
	}
}

func TestFromEnvRecorded(t *testing.T) {
	c, err := Load(write(t, valid, 0o600), env(map[string]string{"MKTO_CLIENT_SECRET": "sec2"}), &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if !c.FromEnv["MKTO_CLIENT_SECRET"] || c.FromEnv["MKTO_CLIENT_ID"] || c.FromEnv["MKTO_BASE_URL"] {
		t.Errorf("FromEnv = %v", c.FromEnv)
	}
}

func TestSecretParseErrorQuotesNothing(t *testing.T) {
	for _, body := range []string{
		"[marketo]\nbase_url = \"https://x.mktorest.com\"\nclient_id = \"id\"\nclient_secret = Zq9secret\n",
		"[marketo]\nbase_url = \"https://x.mktorest.com\"\nclient_id = \"id\"\nclient_secret = \"Zq9\\qsecret\"\n",
		valid + "[llm]\napi_key = Zq9secret\n",
	} {
		p := write(t, body, 0o600)
		_, err := Load(p, env(nil), &bytes.Buffer{})
		if err == nil || strings.Contains(err.Error(), "Zq9") || strings.Contains(err.Error(), "'Z'") || strings.Contains(err.Error(), "'q'") || strings.Contains(err.Error(), "U+") ||
			!strings.Contains(err.Error(), "is not a valid TOML string (put the value in double quotes)") {
			t.Errorf("%q: err = %v", body, err)
		}
	}
}

// TestWhySection: [why] loads, near_hours defaults to 2, and the template's commented
// example parses.
func TestWhySection(t *testing.T) {
	c, err := Load(write(t, valid+"[why]\nscore_field = \"Behavior Score\"\nmql_threshold = 100\nmql_values = [\"MQL\"]\n", 0o600), env(nil), &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if w := c.Why; w.ScoreField != "Behavior Score" || w.MQLThreshold != 100 || !slices.Equal(w.MQLValues, []string{"MQL"}) || w.NearHours != 2 {
		t.Errorf("why = %+v", w)
	}
	var example []string
	in := false
	for _, l := range strings.Split(string(template), "\n") {
		s, ok := strings.CutPrefix(l, "# ")
		in = in || ok && s == "[why]"
		if in && ok {
			example = append(example, s)
		}
	}
	var got Config
	if err := toml.Unmarshal([]byte(strings.Join(example, "\n")), &got); err != nil || got.Why.ScoreField == "" || got.Why.MQLThreshold <= 0 || got.Why.StatusField == "" {
		t.Errorf("commented [why] example: %v, %+v\n%s", err, got.Why, strings.Join(example, "\n"))
	}
}

// TestTokenNamesAbsentVsEmpty: absent means the my.Name / my.WPlink defaults; [] turns them off.
func TestTokenNamesAbsentVsEmpty(t *testing.T) {
	if m := loadTypes(t, ""); !slices.Equal(m.NameTokens, []string{"my.Name"}) || !slices.Equal(m.LinkTokens, []string{"my.WPlink"}) {
		t.Errorf("absent: %v %v", m.NameTokens, m.LinkTokens)
	}
	if m := loadTypes(t, "name_tokens = []\nlink_tokens = [\"my.Link\"]\n"); m.NameTokens == nil || len(m.NameTokens) != 0 || !slices.Equal(m.LinkTokens, []string{"my.Link"}) {
		t.Errorf("custom: %v %v", m.NameTokens, m.LinkTokens)
	}
}

func TestQuotedSecretKeyParseErrorQuotesNothing(t *testing.T) {
	body := "[marketo]\nbase_url = \"https://x.mktorest.com\"\nclient_id = \"id\"\n\"client_secret\" = Zq9secret\n"
	_, err := Load(write(t, body, 0o600), env(nil), &bytes.Buffer{})
	if err == nil || strings.Contains(err.Error(), "'Z'") || strings.Contains(err.Error(), "U+") {
		t.Errorf("err = %v, want no quoted character", err)
	}
}

func TestWriteTemplateForceRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "elsewhere.txt")
	if err := os.WriteFile(target, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "config.toml")
	if err := os.Symlink(target, p); err != nil {
		t.Fatal(err)
	}
	if err := WriteTemplate(p, true); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("err = %v, want a symlink refusal", err)
	}
	if b, _ := os.ReadFile(target); string(b) != "keep" {
		t.Errorf("symlink target overwritten: %q", b)
	}
	if fi, _ := os.Stat(target); fi.Mode().Perm() != 0o644 {
		t.Errorf("symlink target mode changed to %v", fi.Mode().Perm())
	}
}

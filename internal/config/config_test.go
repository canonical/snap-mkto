// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	body := strings.Replace(string(template), `client_id = ""`, `client_id = "a"`, 1)
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

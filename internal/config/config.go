// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

// Package config resolves, loads and validates mkto's TOML configuration.
package config

import (
	_ "embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

//go:embed template.toml
var template []byte

type Config struct {
	Marketo Marketo
	Output  Output
	LLM     LLM
	Why     Why
	// FromEnv: the environment variables that overrode a config key, e.g. "MKTO_CLIENT_SECRET".
	FromEnv map[string]bool `toml:"-"`
}

type Marketo struct {
	BaseURL       string   `toml:"base_url"`
	ClientID      string   `toml:"client_id"`
	ClientSecret  string   `toml:"client_secret"`
	DailyQuota    int      `toml:"daily_quota"`
	LeadFields    []string `toml:"lead_fields"`
	ActivityTypes []string `toml:"activity_types"`
	// ProfileFields: extra lead fields `lead` (and the activity card) show; absent means
	// DefaultProfileFields, an explicit [] means none. Missing ones are skipped, not fatal.
	ProfileFields []string `toml:"profile_fields"`
	// ChangeFields: the Change Data Value fields the text timeline shows; absent means
	// DefaultChangeFields, an explicit [] shows every field.
	ChangeFields []string `toml:"change_fields"`
	// NameTokens and LinkTokens: program tokens `activity` shows as the program's title and
	// link, first match wins; absent means my.Name / my.WPlink, an explicit [] means none.
	NameTokens []string `toml:"name_tokens"`
	LinkTokens []string `toml:"link_tokens"`
	// ActivityTypesDefaulted: activity_types is absent or an unedited v0.1.0 list, so
	// DefaultActivityTypes is in use and its names (some unverified) may be missing.
	ActivityTypesDefaulted bool `toml:"-"`
	// ActivityTypesMigrated: activity_types was an unedited v0.1.0 list.
	ActivityTypesMigrated bool `toml:"-"`
}

// DefaultActivityTypes tell a lead's story: exactly 10, one activities batch.
var DefaultActivityTypes = []string{"Fill Out Form", "Open Email", "Click Email", "Email Bounced", "Email Bounced Soft",
	"Unsubscribe Email", "Change Score", "Change Data Value", "Change Status in Progression", "New Lead"}

var DefaultLeadFields = []string{"id", "email", "firstName", "lastName", "title", "company",
	"industry", "country", "leadSource", "createdAt", "updatedAt"}

// DefaultProfileFields: score, status, owner and CRM sync. Names other than unsubscribed
// are unverified (testdata/marketo/README.md); each one an instance lacks costs a retry.
var DefaultProfileFields = []string{"leadScore", "leadStatus", "sfdcLeadOwnerId", "unsubscribed", "emailInvalid",
	"sfdcLeadId", "sfdcContactId", "sfdcAccountId"}

// DefaultChangeFields are the field labels whose Change Data Value rows the text timeline shows.
var DefaultChangeFields = []string{"Lead Status", "Lead Score", "Company Name"}

// legacyActivityTypes are the v0.1.0 template lists; a config still holding one is migrated.
var legacyActivityTypes = [][]string{
	{"Visit Webpage", "Fill Out Form", "Click Link", "Email Delivered", "Open Email", "Click Email",
		"Unsubscribe Email", "Change Status in Progression", "New Lead"},
	{"Visit Webpage", "Fill Out Form", "Click Link", "Email Delivered", "Open Email", "Click Email",
		"Unsubscribe Email", "Change Status in Progression", "Interesting Moment", "New Lead"},
}

type Output struct {
	Timezone string `toml:"timezone"`
	LeadURL  string `toml:"lead_url"` // a lead's Marketo page, with {id} where the lead id goes
}

// Why is the scoring rule `mkto why` explains, owned by Marketing. ScoreField and
// StatusField are field labels (as `mkto fields` shows them), since Change Score and
// Change Data Value rows name the field by label. The why command validates it.
type Why struct {
	ScoreField   string   `toml:"score_field"`
	MQLThreshold int      `toml:"mql_threshold"`
	StatusField  string   `toml:"status_field"`
	MQLValues    []string `toml:"mql_values"`
	DQValues     []string `toml:"dq_values"`
	NearHours    int      `toml:"near_hours"`
}

type LLM struct {
	Enabled  bool   `toml:"enabled"`
	Provider string `toml:"provider"`
	APIKey   string `toml:"api_key"`
	Model    string `toml:"model"`
}

// Path resolves the config file: flag, MKTO_CONFIG, snap common dir, user config dir.
func Path(flag string, getenv func(string) string) (string, error) {
	switch {
	case flag != "":
		return flag, nil
	case getenv("MKTO_CONFIG") != "":
		return getenv("MKTO_CONFIG"), nil
	case getenv("SNAP_USER_COMMON") != "":
		return filepath.Join(getenv("SNAP_USER_COMMON"), "config.toml"), nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("cannot find a config directory (%w); pass --config <path> or set MKTO_CONFIG", err)
	}
	return filepath.Join(dir, "mkto", "config.toml"), nil
}

// Load reads path, applies env overrides and defaults, and validates.
// A group- or world-readable file only produces a warning on warn.
func Load(path string, getenv func(string) string, warn io.Writer) (*Config, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("no config file at %s: run `mkto init` to create one", path)
	} else if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}
	if fi, err := os.Stat(path); err == nil && fi.Mode().Perm()&0o077 != 0 {
		fmt.Fprintf(warn, "warning: %s is readable by other users and holds credentials; run: chmod 600 %s\n", path, path)
	}
	var c Config
	if err := toml.Unmarshal(b, &c); err != nil {
		if de, ok := errors.AsType[*toml.DecodeError](err); ok && slices.Equal(de.Key(), toml.Key{"marketo", "daily_quota"}) {
			return nil, fmt.Errorf("config key marketo.daily_quota in %s must be a whole number, e.g. 50000", path)
		}
		if key := secretLine(b, err); key != "" { // go-toml quotes the offending character: keep secrets out
			return nil, fmt.Errorf("config key %s in %s is not a valid TOML string (put the value in double quotes)", key, path)
		}
		// go-toml quotes the offending character (U+005A 'Z'): drop it, it may belong to a secret.
		return nil, fmt.Errorf("config %s is not valid TOML: %s", path, quotedChar.ReplaceAllString(err.Error(), "a character"))
	}
	c.FromEnv = map[string]bool{}
	for k, dst := range map[string]*string{
		"MKTO_BASE_URL": &c.Marketo.BaseURL, "MKTO_CLIENT_ID": &c.Marketo.ClientID, "MKTO_CLIENT_SECRET": &c.Marketo.ClientSecret,
		"MKTO_LLM_API_KEY": &c.LLM.APIKey,
	} {
		if v := getenv(k); v != "" {
			*dst, c.FromEnv[k] = v, true
		}
	}
	if err := c.defaultsAndValidate(path); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) defaultsAndValidate(path string) error {
	m := &c.Marketo
	var errs []error
	if strings.TrimSpace(m.BaseURL) != "" {
		base, err := normaliseBaseURL(m.BaseURL)
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("config key marketo.base_url in %s: %w", path, err))
		case strings.EqualFold(base, "https://123-ABC-456.mktorest.com"): // the template's value
			errs = append(errs, fmt.Errorf("config key marketo.base_url in %s is still the template placeholder: set your REST endpoint from Admin > Web Services", path))
		default:
			m.BaseURL = base
		}
	}
	for _, k := range []struct {
		key string
		val *string
	}{
		{"base_url", &m.BaseURL}, {"client_id", &m.ClientID}, {"client_secret", &m.ClientSecret},
	} {
		if strings.TrimSpace(*k.val) == "" {
			errs = append(errs, fmt.Errorf("config key marketo.%s is not set: edit %s (or set the MKTO_%s environment variable)",
				k.key, path, strings.ToUpper(k.key)))
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	var d Config
	if err := toml.Unmarshal(template, &d); err != nil {
		return err // the embedded template is covered by TestTemplateParses
	}
	if m.DailyQuota == 0 {
		m.DailyQuota = d.Marketo.DailyQuota
	}
	if len(m.LeadFields) == 0 {
		m.LeadFields = DefaultLeadFields
	}
	if m.ProfileFields == nil { // go-toml leaves an absent key nil and makes [] empty
		m.ProfileFields = DefaultProfileFields
	}
	if m.ChangeFields == nil {
		m.ChangeFields = DefaultChangeFields
	}
	if m.NameTokens == nil {
		m.NameTokens = []string{"my.Name"}
	}
	if m.LinkTokens == nil {
		m.LinkTokens = []string{"my.WPlink"}
	}
	m.ActivityTypesMigrated = slices.ContainsFunc(legacyActivityTypes, func(l []string) bool {
		return slices.EqualFunc(m.ActivityTypes, l, func(a, b string) bool { return strings.EqualFold(strings.TrimSpace(a), b) })
	})
	if len(m.ActivityTypes) == 0 || m.ActivityTypesMigrated {
		m.ActivityTypes, m.ActivityTypesDefaulted = DefaultActivityTypes, true
	}
	if c.LLM.Model == "" {
		c.LLM.Model = d.LLM.Model
	}
	if c.Why.NearHours == 0 {
		c.Why.NearHours = 2
	}
	if c.Output.Timezone == "" {
		c.Output.Timezone = "Local"
	}
	if _, err := c.Location(); err != nil {
		return fmt.Errorf("config key output.timezone in %s: %w", path, err)
	}
	return nil
}

// normaliseBaseURL returns scheme://host: no trailing slash and no /rest or /rest/v1 suffix.
func normaliseBaseURL(s string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil || u.Host == "" {
		return "", errors.New("must look like https://<munchkin-id>.mktorest.com")
	}
	if u.Scheme != "https" {
		return "", errors.New("must start with https:// (Marketo does not accept plain http)")
	}
	if p := strings.Trim(u.Path, "/"); p != "" && p != "rest" && p != "rest/v1" {
		return "", errors.New("must be the bare host, optionally followed by /rest or /rest/v1")
	}
	return "https://" + u.Host, nil
}

// Location returns the display timezone; "Local" or empty means the system zone.
func (c *Config) Location() (*time.Location, error) {
	if c.Output.Timezone == "" || c.Output.Timezone == "Local" {
		return time.Local, nil
	}
	loc, err := time.LoadLocation(c.Output.Timezone)
	if err != nil {
		return nil, fmt.Errorf("unknown timezone %q: use \"Local\" or an IANA name such as \"Asia/Tbilisi\"", c.Output.Timezone)
	}
	return loc, nil
}

// WriteTemplate writes the commented template (dir 0700 if created, file 0600).
func WriteTemplate(path string, force bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&fs.ModeSymlink != 0 { // --force must not write (and chmod) through it
		return fmt.Errorf("%s is a symlink; mkto won't write through it: remove it or pass --config with another path", path)
	}
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if force {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	f, err := os.OpenFile(path, flags, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%s already exists: use --force to overwrite it", path)
	} else if err != nil {
		return err
	}
	if _, err := f.Write(template); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(0o600); err != nil { // umask and pre-existing files must not loosen it
		f.Close()
		return err
	}
	return f.Close()
}

// secretLine names the secret key on the line a TOML error points at, or "" when the
// error is elsewhere.
func secretLine(b []byte, err error) string {
	de, ok := errors.AsType[*toml.DecodeError](err)
	if !ok {
		return ""
	}
	row, _ := de.Position()
	lines := strings.Split(string(b), "\n")
	if row < 1 || row > len(lines) {
		return ""
	}
	key, _, _ := strings.Cut(lines[row-1], "=")
	switch strings.Trim(strings.TrimSpace(key), `"'`) { // a quoted key is the same key
	case "client_secret":
		return "marketo.client_secret"
	case "api_key":
		return "llm.api_key"
	}
	return ""
}

var quotedChar = regexp.MustCompile(`U\+[0-9A-Fa-f]{4,6} '[^']*'`)

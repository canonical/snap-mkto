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
}

type Marketo struct {
	BaseURL       string   `toml:"base_url"`
	ClientID      string   `toml:"client_id"`
	ClientSecret  string   `toml:"client_secret"`
	DailyQuota    int      `toml:"daily_quota"`
	LeadFields    []string `toml:"lead_fields"`
	ActivityTypes []string `toml:"activity_types"`
}

type Output struct {
	Timezone string `toml:"timezone"`
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
		return nil, fmt.Errorf("config %s is not valid TOML: %w", path, err)
	}
	for k, dst := range map[string]*string{
		"MKTO_BASE_URL": &c.Marketo.BaseURL, "MKTO_CLIENT_ID": &c.Marketo.ClientID, "MKTO_CLIENT_SECRET": &c.Marketo.ClientSecret,
		"MKTO_LLM_API_KEY": &c.LLM.APIKey,
	} {
		if v := getenv(k); v != "" {
			*dst = v
		}
	}
	if err := c.defaultsAndValidate(path); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) defaultsAndValidate(path string) error {
	m := &c.Marketo
	for _, k := range []struct {
		key string
		val *string
	}{
		{"base_url", &m.BaseURL}, {"client_id", &m.ClientID}, {"client_secret", &m.ClientSecret},
	} {
		if strings.TrimSpace(*k.val) == "" {
			return fmt.Errorf("config key marketo.%s is not set: edit %s (or set the MKTO_%s environment variable)",
				k.key, path, strings.ToUpper(k.key))
		}
	}
	base, err := normaliseBaseURL(m.BaseURL)
	if err != nil {
		return fmt.Errorf("config key marketo.base_url in %s: %w", path, err)
	}
	m.BaseURL = base

	var d Config
	if err := toml.Unmarshal(template, &d); err != nil {
		return err // the embedded template is covered by TestTemplateParses
	}
	if m.DailyQuota == 0 {
		m.DailyQuota = d.Marketo.DailyQuota
	}
	if len(m.LeadFields) == 0 {
		m.LeadFields = d.Marketo.LeadFields
	}
	if len(m.ActivityTypes) == 0 {
		m.ActivityTypes = d.Marketo.ActivityTypes
	}
	if c.LLM.Model == "" {
		c.LLM.Model = d.LLM.Model
	}
	if c.Output.Timezone == "" {
		c.Output.Timezone = "Local"
	}
	if _, err := c.Location(); err != nil {
		return fmt.Errorf("config key output.timezone in %s: %w", path, err)
	}
	return nil
}

// normaliseBaseURL returns scheme://host: no trailing slash and no /rest suffix.
func normaliseBaseURL(s string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil || u.Host == "" {
		return "", errors.New("must look like https://<munchkin-id>.mktorest.com")
	}
	if u.Scheme != "https" {
		return "", errors.New("must start with https:// (Marketo does not accept plain http)")
	}
	if p := strings.Trim(u.Path, "/"); p != "" && p != "rest" {
		return "", errors.New("must be the bare host, optionally followed by /rest")
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

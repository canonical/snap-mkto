// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/canonical/snap-mkto/internal/marketotest"
)

func TestEnvSecretNamedOn401(t *testing.T) {
	const envSecret = "env-s3cret-value"
	for _, c := range []struct {
		name string
		env  map[string]string
		hint string
	}{
		{"file", nil, "check client_id and client_secret in "},
		{"secret from env", map[string]string{"MKTO_CLIENT_SECRET": envSecret}, "check MKTO_CLIENT_SECRET (set in the environment) and client_id in "},
		{"both from env", map[string]string{"MKTO_CLIENT_ID": "env-id", "MKTO_CLIENT_SECRET": envSecret},
			"check MKTO_CLIENT_ID / MKTO_CLIENT_SECRET (set in the environment)"},
	} {
		srv := marketotest.New(t)
		srv.Handle(marketotest.IdentityPath, marketotest.Response{Status: 401, Body: `{"error":"unauthorized","error_description":"Bad client credentials"}`})
		_, errOut, code := fakeCLIFull(t, srv, time.Now, "", "", c.env, "lead", "jane@example.com")
		want := "Marketo authentication failed (HTTP 401: Bad client credentials): " + c.hint
		if code != 1 || !strings.HasPrefix(errOut, want) {
			t.Errorf("%s: exit %d, stderr %q; want prefix %q", c.name, code, errOut, want)
		}
		if strings.Contains(errOut, envSecret) || strings.Contains(errOut, "s3cret") {
			t.Errorf("%s: stderr contains a secret", c.name)
		}
	}
}

func TestConfigHelpShowsPath(t *testing.T) {
	out, _, code := exec(t, map[string]string{"SNAP_USER_COMMON": "/home/u/snap/mkto/common"}, "--help")
	if want := "config file path (default: /home/u/snap/mkto/common/config.toml)"; code != 0 || !strings.Contains(out, want) {
		t.Errorf("exit %d, help missing %q:\n%s", code, want, out)
	}
}

func TestJoinedConfigErrorOneLinePerProblem(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	if _, _, code := exec(t, map[string]string{"MKTO_CONFIG": cfg}, "init"); code != 0 {
		t.Fatalf("init exit %d", code)
	}
	_, errOut, code := exec(t, map[string]string{"MKTO_CONFIG": cfg}, "quota")
	lines := strings.Split(strings.TrimSuffix(errOut, "\n"), "\n")
	if code != 1 || len(lines) != 3 || strings.Contains(errOut, "�") {
		t.Errorf("exit %d, want 3 clean lines (placeholder, client_id, client_secret):\n%s", code, errOut)
	}
}

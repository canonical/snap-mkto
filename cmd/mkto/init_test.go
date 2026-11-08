// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitModes(t *testing.T) {
	p := filepath.Join(t.TempDir(), "mkto", "config.toml")
	out, _, code := exec(t, nil, "--config", p, "init")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	di, _ := os.Stat(filepath.Dir(p))
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if di.Mode().Perm() != 0o700 || fi.Mode().Perm() != 0o600 {
		t.Errorf("modes dir %v file %v", di.Mode().Perm(), fi.Mode().Perm())
	}
	for _, want := range []string{p, "client_secret", "daily_quota", "mkto doctor"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestInitRefusesOverwrite(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, errOut, code := exec(t, nil, "--config", p, "init")
	if code != 1 || !strings.Contains(errOut, "--force") {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
	if b, _ := os.ReadFile(p); string(b) != "keep" {
		t.Error("file was overwritten")
	}
}

func TestInitForce(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, code := exec(t, nil, "--config", p, "init", "--force"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	b, _ := os.ReadFile(p)
	fi, _ := os.Stat(p)
	if !strings.Contains(string(b), "[marketo]") || fi.Mode().Perm() != 0o600 {
		t.Errorf("not rewritten: mode %v", fi.Mode().Perm())
	}
}

// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

func golden(t *testing.T, name, got string) {
	t.Helper()
	p := filepath.Join("..", "..", "testdata", "golden", name)
	if *update {
		if err := os.WriteFile(p, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != got {
		t.Errorf("golden %s mismatch\nwant:\n%s\ngot:\n%s", name, want, got)
	}
}

// exec runs the CLI in-process and returns stdout, stderr and the exit code.
func exec(t *testing.T, env map[string]string, args ...string) (string, string, int) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, Env{
		Stdout: &out,
		Stderr: &errb,
		Getenv: func(k string) string { return env[k] },
	})
	return out.String(), errb.String(), code
}

func TestVersionHuman(t *testing.T) {
	out, _, code := exec(t, nil, "version")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{version, commit, "go1.", "snap: no"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestVersionSnap(t *testing.T) {
	out, _, _ := exec(t, map[string]string{"SNAP_NAME": "mkto", "SNAP_REVISION": "x1"}, "version")
	if !strings.Contains(out, "snap: mkto (x1)") {
		t.Errorf("output:\n%s", out)
	}
}

func TestVersionJSON(t *testing.T) {
	old := goVersion
	goVersion = "go1.test"
	defer func() { goVersion = old }()
	out, _, code := exec(t, map[string]string{"SNAP_NAME": "mkto", "SNAP_REVISION": "x1"}, "version", "--json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	golden(t, "version.json", out)
}

func TestUnknownFlagExit2(t *testing.T) {
	_, errOut, code := exec(t, nil, "version", "--nope")
	if code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if !strings.Contains(errOut, "--nope") {
		t.Errorf("stderr = %q", errOut)
	}
}

func TestUnknownCommandExit2(t *testing.T) {
	if _, _, code := exec(t, nil, "bogus"); code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
}

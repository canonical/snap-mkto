// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/canonical/snap-mkto/internal/llmtest"
	"github.com/canonical/snap-mkto/internal/marketotest"
)

// pitchOK cites E1 and source 1 (the Kubernetes news after research renumbering).
const pitchOK = `{"offerings":[` +
	`{"name":"ubuntu pro","reason":"The pricing visit may indicate interest in support for what they run.","cites":["E1"]},` +
	`{"name":"Kubernetes","reason":"They announced a Kubernetes migration.","cites":["E2","[1]"]}],` +
	`"opener":"I saw Example Corp is moving to Kubernetes; happy to share what other platform teams learned.",` +
	`"unknowns":["Whether the lead runs Ubuntu today."],"no_fit":false}`

// playbookDir returns a config dir holding playbook.md with this text.
func playbookDir(t *testing.T, playbook string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "playbook.md"), []byte(playbook), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// briefPitch runs a brief with the playbook in its config dir: narrate, verify, research, then reply as the pitch.
func briefPitch(t *testing.T, dir string, reply llmtest.Response, args ...string) (string, string, int, *llmtest.Server) {
	t.Helper()
	ai := llmtest.New(t, narration(sPricing, sForm), llmtest.Message(`{"unsupported":[]}`, 1, 1), research(researchOK), reply)
	out, errOut, code := briefLLMIn(t, dir, briefServer(t, "Contact Us Form"), ai, true, "model = \"test-model\"\n", args...)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	return out, errOut, code, ai
}

func section(out, name string) string {
	_, s, _ := strings.Cut(out, "## "+name+"\n")
	s, _, _ = strings.Cut(s, "\n## ")
	return s
}

func TestBriefPitchDropsOfferings(t *testing.T) {
	reply := llmtest.Pitch(`{"offerings":[` +
		`{"name":"Juju","reason":"Not in the playbook.","cites":["E1"]},` +
		`{"name":"Kubernetes","reason":"Bad cite.","cites":["E9"]},` +
		`{"name":"Kubernetes","reason":"Bad source.","cites":["7"]},` +
		`{"name":"Kubernetes","reason":"They announced a Kubernetes migration.","cites":["1"]}],` +
		`"opener":"Hello.","unknowns":[],"no_fit":false}`)
	out, _, _, _ := briefPitch(t, playbookDir(t, llmtest.Playbook), reply)
	want := "1. **Kubernetes** — They announced a Kubernetes migration [1].\n\nSuggested opener: \"Hello.\"\n"
	if got := section(out, "How Canonical could help"); got != want {
		t.Errorf("pitch section = %q, want %q", got, want)
	}
	if strings.Contains(out, "What we don't know") {
		t.Error("an empty unknowns list must not render a section")
	}
}

func TestBriefPitchNoFit(t *testing.T) {
	for _, reply := range []string{
		`{"offerings":[],"opener":"x","unknowns":["Budget."],"no_fit":true}`,
		`{"offerings":[{"name":"Juju","reason":"r","cites":["E1"]}],"opener":"x","unknowns":["Budget."],"no_fit":false}`,
	} {
		out, _, _, _ := briefPitch(t, playbookDir(t, llmtest.Playbook), llmtest.Pitch(reply))
		if got := section(out, "How Canonical could help"); got != "_Nothing in the playbook fits these signals._\n" {
			t.Errorf("%s: pitch section = %q", reply, got)
		}
		if got := section(out, "What we don't know"); got != "- Budget.\n" {
			t.Errorf("%s: unknowns = %q", reply, got)
		}
	}
}

func TestBriefPlaybookSkipped(t *testing.T) {
	big := "### Kubernetes\n" + strings.Repeat("x", 64<<10)
	for _, c := range []struct {
		name, playbook, note string
	}{
		{"missing", "", "_No playbook.md in DIR; skipped._\n"},
		{"oversize", big, "_playbook.md is larger than 64 KiB; skipped._\n"},
	} {
		for _, flag := range []string{"", "--json"} {
			dir := t.TempDir()
			if c.playbook != "" {
				dir = playbookDir(t, c.playbook)
			}
			var args []string
			if flag != "" {
				args = append(args, flag)
			}
			out, errOut, _, ai := briefPitch(t, dir, llmtest.Pitch(pitchOK), args...)
			if n := len(ai.Requests()); n != 3 {
				t.Errorf("%s %q: %d LLM requests, want no pitch call", c.name, flag, n)
			}
			note := strings.ReplaceAll(c.note, "DIR", dir)
			if flag == "" {
				if got := section(out, "How Canonical could help"); got != note {
					t.Errorf("%s: pitch section = %q, want %q", c.name, got, note)
				}
				continue
			}
			if errOut != strings.Trim(note, "_\n")+"\n" {
				t.Errorf("%s --json: stderr = %q", c.name, errOut)
			}
			var got struct{ Pitch struct{ Unavailable string } }
			if err := json.Unmarshal([]byte(out), &got); err != nil || got.Pitch.Unavailable == "" {
				t.Errorf("%s --json: pitch = %+v (%v)", c.name, got.Pitch, err)
			}
		}
	}
}

func TestBriefPitchRefused(t *testing.T) {
	out, _, code, _ := briefPitch(t, playbookDir(t, llmtest.Playbook), llmtest.Refusal("cyber"))
	if code != 0 || section(out, "How Canonical could help") != "_Pitch unavailable: the model declined._\n" {
		t.Errorf("exit %d, stdout:\n%s", code, out)
	}
	if !strings.Contains(out, "## Events\n") {
		t.Error("a pitch refusal must still print the brief")
	}
}

func TestBriefPitchSendsPlaybookNoPII(t *testing.T) {
	_, _, _, ai := briefPitch(t, playbookDir(t, llmtest.Playbook), llmtest.Pitch(pitchOK))
	reqs := ai.Requests()
	if len(reqs) != 4 {
		t.Fatalf("%d LLM requests, want 4", len(reqs))
	}
	var m struct {
		Messages []struct {
			Content []struct{ Text string }
		}
	}
	if err := json.Unmarshal(reqs[3].Body, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Messages) != 1 || len(m.Messages[0].Content) != 1 {
		t.Fatalf("pitch request has %d messages, want one with one text block", len(m.Messages))
	}
	want := "Inputs:\n```json\n" +
		`{"sentences":[{"text":"The lead visited the pricing page.","cites":["E1"]},` +
		`{"text":"A day later the lead filled out the contact form, which may indicate buying interest.","cites":["E2"]}],` +
		`"research":[{"text":"Example Corp is a German software company.","sources":[2]},` +
		`{"text":"It has about 250 employees.","sources":[2]},` +
		`{"text":"It runs workloads on Kubernetes.","sources":[1]},` +
		`{"text":"It announced a Kubernetes migration in September 2026.","sources":[1,2]}],` +
		`"sources":[{"url":"` + hitK8s.URL + `","title":"` + hitK8s.Title + `"},{"url":"` + hitAbout.URL + `","title":"` + hitAbout.Title + `"}],` +
		`"event_ids":["E1","E2"]}` + "\n```\n\nPlaybook:\n```markdown\n" + llmtest.Playbook + "\n```"
	if got := m.Messages[0].Content[0].Text; got != want {
		t.Errorf("pitch user message:\n%s\nwant:\n%s", got, want)
	}
	for _, bad := range []string{"jane", "Jane", "Doe", "901820", "+49", "192.0.2.10", "mkt_tok", "abc123", "555", "1001", briefKey} {
		if bytes.Contains(reqs[3].Body, []byte(bad)) {
			t.Errorf("pitch request holds %q", bad)
		}
	}
}

func TestBriefPitchWithoutResearch(t *testing.T) {
	ai := llmtest.New(t, narration(sPricing), llmtest.Message(`{"unsupported":[]}`, 1, 1), llmtest.Pitch(pitchOK))
	out, errOut, code := briefLLMIn(t, playbookDir(t, llmtest.Playbook), briefServer(t, "Contact Us Form"), ai, true, "model = \"test-model\"\n", "--no-research", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	reqs := ai.Requests()
	if len(reqs) != 3 || !bytes.Contains(reqs[2].Body, []byte(`\"research\":[],\"sources\":[]`)) {
		t.Fatalf("want narrate, verify, pitch with no research; got %d requests", len(reqs))
	}
	var got struct {
		Pitch struct {
			Offerings []struct{ Name string }
			NoFit     bool `json:"no_fit"`
		}
		Usage map[string]struct{ In, Out int }
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	// Source [1] no longer exists, so Kubernetes is dropped and only Ubuntu Pro is left.
	if len(got.Pitch.Offerings) != 1 || got.Pitch.Offerings[0].Name != "Ubuntu Pro" || got.Usage["pitch"].In != 150 {
		t.Errorf("--json = %s", out)
	}
}

func TestBriefMarkdownEscaped(t *testing.T) {
	js := `{"summary":[{"text":"Example Corp [Verify](https://phish.example) ships ![](https://t.example/p.png) <b>*_#_*</b> \u0060code\u0060.","sources":[1]}],` +
		`"size":null,"industry":null,"signals":[],"news":[],"sources":[{"url":"` + hitAbout.URL + `","title":"t"}]}`
	evil := llmtest.Result{URL: hitAbout.URL, Title: "About [x](https://phish.example)"}
	for _, flag := range []string{"", "--json"} {
		ai := llmtest.New(t, narration(sPricing), llmtest.Message(`{"unsupported":[]}`, 1, 1), research(js, evil))
		var args []string
		if flag != "" {
			args = append(args, flag)
		}
		out, errOut, code := briefLLM(t, briefServer(t, "Contact Us Form"), ai, true, args...)
		if code != 0 {
			t.Fatalf("exit %d: %s", code, errOut)
		}
		if flag == "--json" {
			if !strings.Contains(out, `"text": "Example Corp [Verify](https://phish.example) ships`) {
				t.Errorf("--json must not be escaped:\n%s", out)
			}
			continue
		}
		for _, raw := range []string{"[Verify](", "![](", "<b>", "[x]("} {
			if strings.Contains(out, raw) {
				t.Errorf("stdout holds live markdown %q:\n%s", raw, out)
			}
		}
		for _, want := range []string{
			`Example Corp \[Verify\]\(https://phish.example\) ships \!\[\]\(https://t.example/p.png\) \<b\>\*\_\#\_\*\</b\> \` + "`" + `code\` + "`" + ` [1].`,
			"1. <" + hitAbout.URL + `> — About \[x\]\(https://phish.example\)` + "\n",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("stdout lacks %q:\n%s", want, out)
			}
		}
	}
}

func TestBriefSave(t *testing.T) {
	dir := playbookDir(t, llmtest.Playbook)
	out, errOut, _, _ := briefPitch(t, dir, llmtest.Pitch(pitchOK), "--save")
	want := filepath.Join(dir, "briefs", "20261107T120000Z-1001.md")
	if errOut != "Saved "+want+"\n" {
		t.Errorf("stderr = %q", errOut)
	}
	b, err := os.ReadFile(want)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != out || !strings.Contains(out, "## How Canonical could help\n1. **Ubuntu Pro**") {
		t.Errorf("saved brief differs from stdout:\n%s", b)
	}
	for _, bad := range []string{"Jane", "jane", "Doe", "jane.doe@example.com", "+49 30 901820", "901820", briefKey} {
		if bytes.Contains(b, []byte(bad)) {
			t.Errorf("saved brief holds lead data or a secret (%d chars)", len(bad))
		}
	}
	for p, mode := range map[string]fs.FileMode{want: 0o600, filepath.Dir(want): 0o700 | fs.ModeDir} {
		if fi, err := os.Stat(p); err != nil || fi.Mode() != mode {
			t.Errorf("%s: mode %v (%v), want %v", filepath.Base(p), fi.Mode(), err, mode)
		}
	}

	// Same second again: a new file, the first one untouched.
	_, errOut, _, _ = briefPitch(t, dir, llmtest.Pitch(pitchOK), "--save")
	second := filepath.Join(dir, "briefs", "20261107T120000Z-1001-2.md")
	if errOut != "Saved "+second+"\n" {
		t.Errorf("second save: stderr = %q", errOut)
	}
	if fi, err := os.Stat(second); err != nil || fi.Mode() != 0o600 {
		t.Errorf("second save: %v", err)
	}
	if again, _ := os.ReadFile(want); !bytes.Equal(again, b) {
		t.Error("second save clobbered the first")
	}
}

func TestBriefCompanyCleanedAndEscaped(t *testing.T) {
	s := briefServer(t, "Contact Us Form")
	s.Handle(leadsPath, marketotest.Response{Body: strings.Replace(briefLead, `"Example Corp"`, `"[Evil](https://phish.example) \u001b[2J Corp"`, 1)})
	ai := llmtest.New(t, narration(sPricing), llmtest.Message(`{"unsupported":[]}`, 1, 1))
	out, errOut, code := briefLLM(t, s, ai, true, "--no-research")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.HasPrefix(out, "# Lead brief · \\[Evil\\]\\(https://phish.example\\) \uFFFD\\[2J Corp · last 90 days\n") {
		t.Errorf("stdout:\n%s", out)
	}
}

func TestBriefPitchAPIError(t *testing.T) {
	reply := llmtest.Response{Status: 400, Body: `{"type":"error","error":{"type":"invalid_request_error","message":"no"}}`}
	out, _, code, _ := briefPitch(t, playbookDir(t, llmtest.Playbook), reply)
	if want := "_Pitch unavailable: Anthropic API error 400: invalid_request_error._\n"; code != 0 || section(out, "How Canonical could help") != want {
		t.Errorf("exit %d, stdout:\n%s", code, out)
	}
}

func TestBriefMarkdownEscapesModelText(t *testing.T) {
	ai := llmtest.New(t, narration(`{"text":"The lead saw [x](https://phish.example) ~~here~~ [E1].","cites":["E1"]}`),
		llmtest.Message(`{"unsupported":[]}`, 1, 1), llmtest.Pitch(`{"offerings":[`+
			`{"name":"Kubernetes","reason":"Fits ![](https://t.example/p.png) *now*.","cites":["E1"]}],`+
			`"opener":"Hi <b>there</b> _x_","unknowns":["Budget [y](https://phish.example)#"],"no_fit":false}`))
	out, errOut, code := briefLLMIn(t, playbookDir(t, llmtest.Playbook), briefServer(t, "Contact Us Form"), ai, true, "model = \"test-model\"\n", "--no-research")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	for _, want := range []string{
		`The lead saw \[x\]\(https://phish.example\) \~\~here\~\~ [E1].`,
		`1. **Kubernetes** — Fits \!\[\]\(https://t.example/p.png\) \*now\* [E1].`,
		`Suggested opener: "Hi \<b\>there\</b\> \_x\_"`,
		`- Budget \[y\]\(https://phish.example\)\#`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
}

func TestBriefEventsFenced(t *testing.T) {
	for _, c := range []struct{ asset, fence string }{
		{"[Verify](https://phish.example)", "```"},
		{"Form ```x```", "````"},
	} {
		s := briefServer(t, c.asset)
		s.Handle(actsPath, marketotest.Response{Body: strings.Replace(briefActs(c.asset),
			"www.example.com/pricing?mkt_tok=abc123SECRET&utm_source=x", "www.example.com/%3Cimg%20src=x%3E", 1)})
		ai := llmtest.New(t, narration(sPricing), llmtest.Message(`{"unsupported":[]}`, 1, 1))
		out, errOut, code := briefLLM(t, s, ai, true, "--no-research")
		if code != 0 {
			t.Fatalf("%s: exit %d: %s", c.asset, code, errOut)
		}
		want := "## Events\n" + c.fence + "\nE1  2026-11-01 10:00 UTC  Visit Webpage       https://www.example.com/<img src=x>\n" +
			"E2  2026-11-02 11:00 UTC  Fill Out Form       " + c.asset + "\nE3  2026-11-03 08:00 UTC  Interesting Moment\n" + c.fence + "\n"
		if !strings.HasSuffix(out, want) {
			t.Errorf("%s: stdout does not end with %q:\n%s", c.asset, want, out)
		}
	}
}

func TestBriefSaveFails(t *testing.T) {
	dir := playbookDir(t, llmtest.Playbook)
	if err := os.WriteFile(filepath.Join(dir, "briefs"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ai := llmtest.New(t, narration(sPricing), llmtest.Message(`{"unsupported":[]}`, 1, 1), llmtest.Pitch(pitchOK))
	out, errOut, code := briefLLMIn(t, dir, briefServer(t, "Contact Us Form"), ai, true, "model = \"test-model\"\n", "--no-research", "--save")
	if code != 1 || !strings.Contains(out, "## Events\n") || !strings.HasPrefix(errOut, "cannot save the brief: ") {
		t.Errorf("exit %d, stderr %q, stdout:\n%s", code, errOut, out)
	}
}

func TestBriefSaveRefusesSymlink(t *testing.T) {
	dir := playbookDir(t, llmtest.Playbook)
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(dir, "briefs")); err != nil {
		t.Fatal(err)
	}
	ai := llmtest.New(t, narration(sPricing), llmtest.Message(`{"unsupported":[]}`, 1, 1), llmtest.Pitch(pitchOK))
	_, errOut, code := briefLLMIn(t, dir, briefServer(t, "Contact Us Form"), ai, true, "model = \"test-model\"\n", "--no-research", "--save")
	if want := "briefs/ in " + dir + " is a symlink; refusing to save\n"; code != 1 || errOut != want {
		t.Errorf("exit %d, stderr %q, want %q", code, errOut, want)
	}
	if ents, _ := os.ReadDir(target); len(ents) != 0 {
		t.Error("saved through the symlink")
	}
}

func TestBriefSaveWarnsOpenDir(t *testing.T) {
	dir := playbookDir(t, llmtest.Playbook)
	briefs := filepath.Join(dir, "briefs")
	if err := os.Mkdir(briefs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(briefs, 0o755); err != nil {
		t.Fatal(err)
	}
	_, errOut, _, _ := briefPitch(t, dir, llmtest.Pitch(pitchOK), "--save")
	want := "warning: " + briefs + " is accessible by other users; run: chmod 700 " + briefs + "\nSaved " +
		filepath.Join(briefs, "20261107T120000Z-1001.md") + "\n"
	if errOut != want {
		t.Errorf("stderr = %q, want %q", errOut, want)
	}
	if fi, err := os.Stat(briefs); err != nil || fi.Mode().Perm() != 0o755 {
		t.Error("an existing briefs/ must keep its mode")
	}
}

func TestBriefSaveWithShowPayload(t *testing.T) {
	s := briefServer(t, "Contact Us Form")
	out, errOut, code := brief(t, s, "", "--show-payload", "--save")
	if code != 2 || out != "" || !strings.Contains(errOut, "--save can't be used with --show-payload") || len(s.Methods()) != 0 {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
}

func TestBriefSaveWithJSON(t *testing.T) {
	dir := playbookDir(t, llmtest.Playbook)
	out, _, _, _ := briefPitch(t, dir, llmtest.Pitch(pitchOK), "--save", "--json")
	b, err := os.ReadFile(filepath.Join(dir, "briefs", "20261107T120000Z-1001.md"))
	if err != nil || !json.Valid([]byte(out)) || !bytes.HasPrefix(b, []byte("# Lead brief · Example Corp")) {
		t.Errorf("--save --json must print JSON and save the markdown (%v)", err)
	}
}

// TestNoEmbeddedPlaybook fails if any go:embed directive names or matches a playbook file.
func TestNoEmbeddedPlaybook(t *testing.T) {
	root := filepath.Join("..", "..")
	n := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		for sc := bufio.NewScanner(f); sc.Scan(); {
			line := strings.TrimSpace(sc.Text())
			if !strings.HasPrefix(line, "//go:embed ") {
				continue
			}
			n++
			for _, pat := range strings.Fields(strings.TrimPrefix(line, "//go:embed ")) {
				matches, _ := filepath.Glob(filepath.Join(filepath.Dir(p), strings.TrimPrefix(pat, "all:")))
				for _, m := range append(matches, pat) {
					_ = filepath.WalkDir(m, func(q string, _ fs.DirEntry, _ error) error {
						if strings.Contains(strings.ToLower(q), "playbook") {
							t.Errorf("%s embeds %s", p, q)
						}
						return nil
					})
					if strings.Contains(strings.ToLower(m), "playbook") {
						t.Errorf("%s: go:embed %s names a playbook", p, pat)
					}
				}
			}
		}
		return nil
	})
	if err != nil || n == 0 {
		t.Fatalf("walked the repo: %d go:embed directives (%v)", n, err)
	}
}

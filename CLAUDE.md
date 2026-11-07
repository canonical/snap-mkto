# mkto — project guide for Claude Code

## What this is

`mkto` is a small, **read-only** command-line client for the Marketo Engage REST API, written in Go and packaged as a **strictly confined Ubuntu snap**.

Given a lead's email address it shows the lead's profile, recent activity and program memberships. It can also list programs and show how much of the daily API quota is left. A later phase adds an optional LLM layer (`mkto brief`) that turns a *redacted* activity timeline into a short narrative, researches the lead's company, and suggests how Canonical could help.

The project started as a snap-packaging assignment. The snap and its documentation (`SNAP_PACKAGING.md`) are first-class deliverables, not an afterthought.

## Read these before working

| File | What it is |
|---|---|
| `docs/SPEC.md` | v1 commands, config, Marketo API usage, error handling, output. Source of truth for v1. |
| `docs/LLM_LAYER.md` | Phase 2–3 design: redaction, narrative, verification, company research, pitch. **Do not start this until Phase 1.5 is done.** |
| `docs/TASKS.md` | Phased checklist. Work top to bottom, one task at a time, and tick items off as you finish them. |
| `docs/REPO.md` | GitHub repo settings, commit conventions, CI, Dependabot, releases. |
| `SNAP_PACKAGING.md` | Snap build/install/config guide. Keep it accurate whenever `snap/snapcraft.yaml` changes. |
| `PLAYBOOK.example.md` | Shape of the user's private `playbook.md` (Phase 3). The real file is never committed. |

## Stack

- Go (current stable release), Go modules, one binary: `mkto`.
- CLI: `github.com/spf13/cobra`. Config: TOML via `github.com/pelletier/go-toml/v2`. Tables: `text/tabwriter`.
- Keep dependencies minimal. Ask before adding any dependency not listed here.
- Tests: standard `testing` plus a `net/http/httptest` fake Marketo server. **No real Marketo calls in tests, ever.**
- Snap: snapcraft, `base: core26`, `confinement: strict`, `go` plugin.

## Layout

```
cmd/mkto/main.go          entrypoint
internal/config/          path resolution, load, env overrides, validation, permission check
internal/marketo/         HTTP client, token cache, endpoints, error mapping, retries
internal/output/          table and JSON rendering
internal/redact/          (Phase 2) the ONLY path from Marketo data to an LLM
internal/llm/             (Phase 2+) provider client + prompts; accepts only redact types
snap/snapcraft.yaml
testdata/                 invented fixtures only (example.com addresses)
```

## Commands

```bash
go build ./...
go test ./...
go vet ./...
gofmt -l .                      # must print nothing
go run ./cmd/mkto <command>     # run from source

snapcraft pack                  # builds in LXD
sudo snap install --dangerous ./mkto_*.snap
```

## Non-negotiable rules

1. **Read-only.** mkto calls only GET endpoints on Marketo, plus the identity token endpoint. Never add create, update or delete calls.
2. **Secrets stay in the config file.** Never log `client_secret`, access tokens or LLM API keys. Mask them in `--verbose` output and error messages, and never print a request URL that contains a secret. Never put credentials in `snapcraft.yaml`, code, tests or git history.
3. **No real lead data in the repo.** Fixtures in `testdata/` are invented. Use `example.com` / `example.org` addresses.
4. **`.gitignore`** must cover `config.toml`, `playbook.md`, `briefs/`, `*.snap`, `.env`, `dist/`.
5. **Phase 2+:** nothing reaches an LLM except through `internal/redact`. The LLM package's public functions accept only redact types, so unredacted data can't be passed by accident. LLM features are off by default (`[llm] enabled = false`).
6. **Quota-aware.** Cache the access token, page efficiently, batch activity types, and back off on rate-limit errors (see `docs/SPEC.md`).
7. **Don't guess APIs.** If unsure about a Marketo endpoint, a snapcraft key, or a Claude API detail, check the official docs:
   - Marketo: https://developer.adobe.com/marketo-apis/
   - Snapcraft: https://documentation.ubuntu.com/snapcraft/stable/
   - Claude API: https://docs.claude.com/en/api/overview

## Working style

- Small, focused commits. One task from `docs/TASKS.md` at a time.
- After any change to `snap/snapcraft.yaml`: rebuild the snap, re-test the commands under confinement, then update `SNAP_PACKAGING.md`.
- If something in these docs looks wrong or outdated, say so and propose a fix instead of silently deviating.
- Prefer clear error messages that tell the user what to do next (e.g. which config key to set, which `chmod` to run).

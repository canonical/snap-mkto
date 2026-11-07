# mkto

Marketers who need one lead's story usually click through Marketo screen by screen: the profile, the activity log, each program the lead joined. `mkto` puts that on one terminal screen. Give it an email address and it shows the lead's profile, a timeline of recent activity and their program memberships. It also lists programs and reports today's API usage. It is a small, read-only command-line client for the Marketo Engage REST API, packaged as a strictly confined Ubuntu snap.

It enables marketing ops and growth teams to:

- Look up: see a lead's profile fields by email, with duplicates flagged instead of hidden.
- Trace: read a lead's activity as one chronological timeline, plus the programs they joined in the same window.
- Budget: check how much of the daily API quota is left before a big job.
- Brief: optionally have Claude write a short lead brief from redacted data, with web research on the company and next steps from your own playbook.
- Export: write any result as JSON to a file only you can read.

> **Read-only by design.** mkto only sends GET requests to Marketo, plus the identity call for a token. It never creates, updates or deletes anything. `mkto brief` is **off by default** and needs data-protection sign-off before use with real leads. Every example on this page and in the tests uses invented data.

<p>
<img src="./docs/assets/mkto_demo.gif" alt="Terminal demo: mkto quota, lead, activity and brief for an invented example.com lead" width="800">
</p>

## Key Features

- **Strictly confined snap**: only the `network` and `home` plugs. The config lives in `~/snap/mkto/common/` and survives refreshes. Verified under confinement on a real instance.
- **Quota-aware**: the token is cached, activity types are batched 10 per call, paging stops if Marketo repeats itself, and rate-limit errors back off at 5, 10 and 20 s. When the daily quota runs out, mkto stops with exit code 4.
- **Built for large instances**: web-activity scans can take about 30 s per page, so activity calls allow 120 s and print a progress line. `brief` skips web visits unless you add `--web`.
- **Secrets stay secret**: the client secret, tokens and API keys never reach logs, `--verbose` output or errors. Redirects are never followed, and Marketo text is cleaned of terminal escape codes before it is printed.
- **Redaction before any LLM call**: only an allowlist of fields reaches the model. A guard replaces emails, phone numbers, IPs and the lead's name with `{redacted}`, and `--show-payload` prints exactly what would be sent.
- **Every claim is checked**: each sentence in a brief must cite an event, a second model call removes unsupported ones, research keeps only sources that came back from a real web search, and offerings must come from your playbook.

## Software Components

| Component    | Path                   | Purpose                                                                                     |
| ------------ | ---------------------- | ------------------------------------------------------------------------------------------- |
| CLI          | `cmd/mkto/`            | Cobra commands: `init`, `lead`, `activity`, `programs`, `quota`, `brief`, `version`         |
| Config       | `internal/config/`     | Path resolution, TOML load, env overrides, validation, the `chmod 600` warning              |
| Marketo      | `internal/marketo/`    | HTTP client: token cache, response envelope, retries and backoff, per-request deadlines     |
| Output       | `internal/output/`     | Tables, stable JSON structs, terminal-safe text                                             |
| Redaction    | `internal/redact/`     | The only path from Marketo data to an LLM: allowlist, URL scrubbing, in-place guard         |
| LLM          | `internal/llm/`        | Claude via the official Go SDK: narrate, verify, research with web search, pitch            |
| Snap         | `snap/snapcraft.yaml`  | core26, strict confinement, one static binary                                               |
| Test data    | `testdata/`            | Invented Marketo responses and golden outputs (example.com only)                            |

## Quickstart

1. Build and install the snap. It needs `snapcraft` and LXD; see [SNAP_PACKAGING.md](./SNAP_PACKAGING.md). mkto isn't in the Snap Store yet, so the local build is installed with `--dangerous` (it isn't store-signed).

   ```bash
   snapcraft pack
   sudo snap install --dangerous ./mkto_0.1.0_amd64.snap
   mkto version
   ```

   From source instead (Go 1.27.3 or later): `make build` builds `./mkto` with the version and commit stamped in.

2. Create the config file. It is written with mode 600, readable only by you.

   ```bash
   mkto init
   ```

   Fill in `base_url` (`https://<munchkin-id>.mktorest.com`), `client_id` and `client_secret` from **Admin → Web Services**, and set `daily_quota` to your subscription's limit. Use an API-only user whose role has read permissions only.

3. Check the connection and your quota.

   ```bash
   mkto quota
   ```

   ```
   Today (2026-11-07, US Central): 1120 of 50000 calls used (2.2%), 48880 remaining
   The daily limit comes from daily_quota in ~/snap/mkto/common/config.toml; Marketo doesn't report it.
   ```

4. Look up a lead and their recent activity.

   ```bash
   mkto lead jane@example.com
   mkto activity jane@example.com --days 30
   ```

5. Export to a file, for a spreadsheet or another tool.

   ```bash
   mkto activity jane@example.com --types "Open Email,Click Email" --json --out ~/jane.json
   ```

Inside the snap, `--out` can write anywhere in your home folder except hidden paths, and `/tmp` is private to the snap. mkto says so when either applies.

## How it works

`mkto activity`, the busiest command, runs these steps:

1. **Resolve the lead** by email. With duplicates it picks the most recently updated lead and lists the others, so you can choose one with `--lead-id`.
2. **Resolve activity types** by name from the config, and warn about names your instance doesn't have.
3. **Scan activities** from a paging token at the start of the window, at most 10 types per call, following `nextPageToken` until Marketo says there is no more.
4. **Load program memberships**, keep the ones dated within the window, newest first, and look up their names (capped at 25 lookups).
5. **Render** a table in your timezone, or stable JSON with UTC timestamps.

Exit codes are the same for every command:

| Code | Meaning                        |
| ---- | ------------------------------ |
| 0    | Success                        |
| 1    | Error                          |
| 2    | Usage error                    |
| 3    | Lead not found                 |
| 4    | Daily API quota exhausted      |

## Use it

| Command                        | What it does                                                                 |
| ------------------------------ | ---------------------------------------------------------------------------- |
| `mkto init [--force]`          | Write the config template                                                    |
| `mkto version`                 | Version, commit, Go version and snap revision                                |
| `mkto lead <email>`            | The configured `lead_fields` for every lead with that email                  |
| `mkto activity <email>`        | Timeline and programs. `--days`, `--types`, `--all-types`, `--lead-id`, `--all-programs`, `--out` |
| `mkto programs [--name <s>]`   | All programs, filtered case-insensitively                                    |
| `mkto quota`                   | Today's usage (US Central day, when the quota resets) and the last 7 days    |
| `mkto brief <email>`           | The LLM lead brief (see below)                                               |

Every data command takes `--json`, and `--verbose` shows request paths, timings and retries, never secrets or emails.

### mkto brief

`mkto brief <email>` writes a short brief: a cited narrative of the lead's recent activity, web research on the company, and "How Canonical could help" from your own `playbook.md`.

```bash
mkto brief jane@example.com --show-payload     # what would be sent; sends nothing
mkto brief jane@example.com --days 30 --save
```

- **Off by default.** Get data-protection sign-off first (see [docs/LLM_LAYER.md](./docs/LLM_LAYER.md)), then set `[llm] enabled = true` and `api_key`.
- **Review first.** `--show-payload` works while `enabled = false` and starts with a list of what was redacted.
- **Playbook.** Copy [PLAYBOOK.example.md](./PLAYBOOK.example.md) to `playbook.md` in the config directory. Without it, the pitch step is skipped.
- `--web` adds web visits and link clicks, `--no-research` skips the company web search, and `--save` writes the brief to `briefs/` in the config directory (mode 600).

## Development

### Tests and lint

```bash
make test      # go test -race ./..., offline: fake Marketo and Claude servers
make lint      # go vet + gofmt (same as CI)
make build     # ./mkto with version and commit stamped in
make snap      # snapcraft pack, in LXD
make gif       # re-render docs/assets/mkto_demo.gif
```

Go dependencies are in `go.mod` and `go.sum`. The only Python is `tools/demo_gif.py`, which renders the README GIF from the golden test outputs. It runs with [uv](https://docs.astral.sh/uv/): its one dependency, Pillow, is declared inside the script and pinned in `tools/demo_gif.py.lock`, so `make gif` needs nothing installed beyond uv.

### GitHub Actions

| Workflow   | File                       | Runs                                                                         |
| ---------- | -------------------------- | ---------------------------------------------------------------------------- |
| CI         | `.github/workflows/ci.yml` | Every push and pull request: `test` (tidy, vet, gofmt, race tests), then `snap` (builds the snap and uploads it as the `mkto-snap` artifact) |
| Renovate   | `renovate.json`            | Dependency update PRs for Go modules and GitHub Actions                      |
| Dependabot | `.github/dependabot.yml`   | Weekly Go module and monthly GitHub Actions update PRs                      |

Every action in CI is pinned to a commit SHA.

### Configuration

| Setting                                                   | Default                             | Purpose                                      |
| --------------------------------------------------------- | ----------------------------------- | -------------------------------------------- |
| `--config` / `MKTO_CONFIG`                                | `~/snap/mkto/common/config.toml` (snap), `~/.config/mkto/config.toml` (source) | Which config file to use |
| `MKTO_BASE_URL`, `MKTO_CLIENT_ID`, `MKTO_CLIENT_SECRET`   | from the file                       | Override the file, for CI. Don't type secrets inline in a shell |
| `daily_quota`                                             | `50000`                             | Your subscription's limit; Marketo doesn't report it |
| `lead_fields`, `activity_types`                           | see `mkto init`                     | What `lead` shows and `activity` fetches     |
| `timezone`                                                | `Local`                             | Any IANA name, e.g. `Asia/Tbilisi`           |
| `[llm] enabled`, `api_key` / `MKTO_LLM_API_KEY`, `model`  | `false`, none, `claude-sonnet-5-5`  | `mkto brief`                                 |

## Reference

| Document                                                             | What's in it                                                       |
| -------------------------------------------------------------------- | ------------------------------------------------------------------ |
| [SNAP_PACKAGING.md](./SNAP_PACKAGING.md)                             | Building, installing and debugging the snap; the confinement run   |
| [docs/SPEC.md](./docs/SPEC.md)                                       | Commands, config, Marketo API usage, errors and output             |
| [docs/LLM_LAYER.md](./docs/LLM_LAYER.md)                             | `mkto brief`: redaction rules, pipeline, known limits              |
| [docs/REAL_INSTANCE_CHECKLIST.md](./docs/REAL_INSTANCE_CHECKLIST.md) | How to verify a build against a real instance, and past results    |
| [docs/PLAN.md](./docs/PLAN.md), [docs/TASKS.md](./docs/TASKS.md)     | The build plan and its checklist                                   |
| [docs/REPO.md](./docs/REPO.md)                                       | Repository settings, conventions, CI and releases                  |
| [CLAUDE.md](./CLAUDE.md), [.claude/agents/](./.claude/agents/)       | The AI build setup, kept on purpose                                |

## Notice

mkto is an internal tool, not an official Canonical product. It reads from Marketo only, and with `mkto brief` enabled it sends redacted data to the Claude API. All people, companies and figures in the tests, docs and GIF are invented.

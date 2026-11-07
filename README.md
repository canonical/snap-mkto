# mkto

A small, read-only command-line client for the Marketo Engage REST API, packaged as a strictly confined Ubuntu snap.

- `mkto init` — create the config file
- `mkto version` — version and build info
- `mkto lead <email>` — a lead's profile fields
- `mkto activity <email>` — recent activity timeline and program memberships
- `mkto programs` — list programs
- `mkto quota` — today's API usage against your daily limit
- `mkto brief <email>` — optional LLM-written lead brief, off by default (see below)

mkto only reads from Marketo. It never creates, updates or deletes anything.

## Install

mkto isn't in the Snap Store yet. Build the snap locally (needs `snapcraft` and LXD; see `SNAP_PACKAGING.md`):

```bash
snapcraft pack                                    # builds in LXD → mkto_0.1.0_amd64.snap
sudo snap install --dangerous ./mkto_0.1.0_amd64.snap
mkto version
```

`--dangerous` is needed because a local build isn't signed by the store. The CI `snap` job builds the same snap and uploads it as the `mkto-snap` artifact.

From source (Go 1.27.3 or later):

```bash
make build      # ./mkto, with version and commit stamped in
# or: go install ./cmd/mkto
```

## Configure

```bash
mkto init
```

This creates a config file readable only by you:

- snap: `~/snap/mkto/common/config.toml`
- from source: `~/.config/mkto/config.toml`

Fill in your REST base URL, client id and client secret from **Admin → Web Services**, and set `daily_quota` to your subscription's limit. For CI, `MKTO_BASE_URL`, `MKTO_CLIENT_ID` and `MKTO_CLIENT_SECRET` override the file.
In CI, set them as repository secrets. In a shell, don't type secrets inline (`MKTO_CLIENT_SECRET=... mkto quota`): they land in your shell history.

Config path, first match wins: `--config <path>`, `MKTO_CONFIG`, `$SNAP_USER_COMMON/config.toml` (inside the snap), `~/.config/mkto/config.toml`.

Use an API-only user whose role has read permissions only.

## Usage

```bash
mkto quota
mkto lead jane@example.com
mkto activity jane@example.com --days 30
mkto activity jane@example.com --types "Open Email,Click Email" --json --out ~/jane.json
mkto activity jane@example.com --all-programs   # programs outside the window too
mkto programs --name webinar
```

Add `--json` to any data command (lead, activity, programs, quota, version, brief) for machine-readable output, and `--verbose` to see request timings and retries.

## mkto brief (optional, off by default)

`mkto brief <email>` writes a short lead brief: a cited narrative of the lead's recent activity, built from redacted data only, plus web research on the company and "How Canonical could help" from your own playbook.

- **Off by default.** Get data-protection sign-off before using it with real leads (see `docs/LLM_LAYER.md`), then set `[llm] enabled = true` and `api_key` (or `MKTO_LLM_API_KEY`).
- **Review first.** `mkto brief <email> --show-payload` prints exactly what would be sent and sends nothing. It works while `enabled = false`.
- **Playbook.** Offerings come only from `playbook.md` in the config directory (`~/snap/mkto/common/playbook.md` under the snap, `~/.config/mkto/playbook.md` from source). Copy `PLAYBOOK.example.md` to start. Without it, the pitch step is skipped.
- `--web` adds web visits and link clicks, which `brief` skips by default because they are slow on large instances.
- `--no-research` skips the company web search. `--save` also writes the brief to `briefs/<UTC time>-<lead id>.md` in the config directory (mode 0600). `--json` gives the full result as JSON.

```bash
mkto brief jane@example.com --show-payload
mkto brief jane@example.com --days 30 --save
```

## Docs

- `SNAP_PACKAGING.md` — building, installing and debugging the snap
- `docs/SPEC.md` — command and API specification
- `docs/LLM_LAYER.md` — design for `mkto brief`

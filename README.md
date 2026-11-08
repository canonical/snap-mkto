# mkto

Marketers who need one lead's story usually click through Marketo screen by screen: the profile, the activity log, each program the lead joined. `mkto` puts that on one terminal screen. Give it an email address and it shows the lead's profile, a timeline of recent activity and their program memberships, or explains how their score and status got where they are. It also looks up a list of leads or everyone at an account, lists programs, activity types and lead fields, and reports today's API usage. It is a small, read-only command-line client for the Marketo Engage REST API, packaged as a strictly confined Ubuntu snap.

It enables marketing ops and growth teams to:

- Look up: see a lead's profile fields by email, with duplicates flagged instead of hidden.
- Trace: read a lead's activity as one chronological timeline, plus the programs they joined in the same window.
- Explain: see the score and status changes behind an MQL or DQ, judged by your own scoring rule.
- Scale: check a list of emails or a whole account in a few API calls.
- Budget: check how much of the daily API quota is left before a big job.
- Brief: optionally have Claude write a short lead brief from redacted data, with web research on the company and next steps from your own playbook.
- Export: write results as JSON or CSV to a file only you can read.

> **Read-only by design.** mkto only sends GET requests to Marketo, plus the identity call for a token. It never creates, updates or deletes anything. `mkto brief` is **off by default** and needs data-protection sign-off before use with real leads. Every example on this page and in the tests uses invented data.

<p>
<img src="./docs/assets/mkto_demo.gif" alt="Terminal demo: mkto quota, lead, activity and brief for an invented example.com lead" width="800">
</p>

## Key Features

- **Strictly confined snap**: only the `network` and `home` plugs. The config lives in `~/snap/mkto/common/` and survives refreshes. Verified under confinement on a real instance.
- **Quota-aware**: the token is cached, activity types are batched 4 per call (3 batches at a time; a 10-type page is about ten times slower), paging stops if Marketo repeats itself, and rate-limit errors back off at 5, 10 and 20 s. When the daily quota runs out, mkto stops with exit code 4.
- **Built for large instances**: web-activity scans can take about 30 s per page, so activity calls allow 120 s and print a progress line. `activity`, `why`, `brief`, `account` and `leads --activity` skip web visits unless you add `--web`.
- **Secrets stay secret**: the client secret, tokens and API keys never reach logs, `--verbose` output or errors. Redirects are never followed, and Marketo text is cleaned of terminal escape codes before it is printed.
- **Redaction before any LLM call**: only an allowlist of fields reaches the model. A guard replaces emails, phone numbers, IPs and the lead's name with `{redacted}`, and `--show-payload` prints exactly what would be sent.
- **Every claim is checked**: each sentence in a brief must cite an event, a second model call removes unsupported ones, research keeps only sources that came back from a real web search, and offerings must come from your playbook.

## Software Components

| Component    | Path                   | Purpose                                                                                     |
| ------------ | ---------------------- | ------------------------------------------------------------------------------------------- |
| CLI          | `cmd/mkto/`            | Cobra commands: `init`, `doctor`, `version`, `lead`, `activity`, `why`, `brief`, `leads`, `account`, `programs`, `types`, `fields`, `quota` |
| Config       | `internal/config/`     | Path resolution, TOML load, env overrides, validation, the `chmod 600` warning              |
| Marketo      | `internal/marketo/`    | HTTP client: token cache, response envelope, retries and backoff, per-request deadlines     |
| Output       | `internal/output/`     | Tables, stable JSON structs, terminal-safe text                                             |
| Story        | `internal/story/`      | Pure helpers: the timeline detail column, the call card summary, the score/status ledger    |
| Redaction    | `internal/redact/`     | The only path from Marketo data to an LLM: allowlist, URL scrubbing, in-place guard         |
| LLM          | `internal/llm/`        | Claude via the official Go SDK: narrate, verify, research with web search, pitch            |
| Snap         | `snap/snapcraft.yaml`  | core26, strict confinement, one static binary                                               |
| Test data    | `testdata/`            | Invented Marketo responses and golden outputs (example.com only)                            |

## Quickstart

1. Build and install the snap. It needs `snapcraft` and LXD; see [SNAP_PACKAGING.md](./SNAP_PACKAGING.md). mkto isn't in the Snap Store yet, so the local build is installed with `--dangerous` (it isn't store-signed).

   ```bash
   snapcraft pack
   sudo snap install --dangerous ./mkto_*_amd64.snap
   mkto version
   ```

   From source instead (Go 1.27.3 or later): `make build` builds `./mkto` with the version and commit stamped in.

2. Create the config file, then edit it. It is written with mode 600, readable only by you.

   ```bash
   mkto init
   ```

   Fill in `base_url` (`https://<munchkin-id>.mktorest.com`), `client_id` and `client_secret` from **Admin → Web Services**, and set `daily_quota` to your subscription's limit. Use an API-only user whose role has read permissions only. Everything else can stay commented out: the defaults apply.

3. Check the setup.

   ```bash
   mkto doctor
   ```

   `doctor` checks, in about four API calls, that the config loads, the credentials work, the API role can read activity types, lead fields and programs, and that every name in your config exists on your instance. A line starting `✗` says what to fix; `!` is a warning. `mkto quota` then shows how much of today's quota is left:

   ```
   Today (2026-11-07, US Central): 1120 of 50000 calls used (2.2%), 48880 remaining
   The daily limit comes from daily_quota in ~/snap/mkto/common/config.toml; Marketo doesn't report it.
   ```

4. Look up a lead, their recent activity, and why their score or status changed.

   ```bash
   mkto lead jane@example.com
   mkto activity jane@example.com --days 30
   mkto why jane@example.com
   ```

   `why` needs the `[why]` section of the config (your MQL rule, owned by Marketing); `mkto init` writes a commented example, and `mkto fields` gives the field labels it takes.

5. Export to a file, for a spreadsheet or another tool.

   ```bash
   mkto activity jane@example.com --types "Open Email,Click Email" --json --out ~/jane.json
   mkto leads --from ~/emails.csv --csv --out ~/leads.csv
   ```

Inside the snap, `--out` and `--from` work anywhere in your home folder except hidden paths, and `/tmp` is private to the snap. mkto says so when either applies.

## How it works

`mkto activity`, the busiest command, runs these steps:

1. **Resolve the lead** by email. With duplicates it picks the most recently updated lead and lists the others, so you can choose one with `--lead-id`.
2. **Resolve activity types** by name from the config, and warn about names your instance doesn't have.
3. **Scan activities** from a paging token at the start of the window, at most 4 types per call and 3 batches at a time, following `nextPageToken` until Marketo says there is no more.
4. **Load program memberships**, keep the ones dated within the window, newest first, and look up their names and `my.` name/link tokens (capped at 25 programs).
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
| `mkto doctor`                  | Check the config, credentials, API permissions and the names in your config; exit 1 if a check fails |
| `mkto version`                 | Version, commit, Go version and snap revision                                |
| `mkto lead <email>`            | The configured `lead_fields` and `profile_fields` for every lead with that email; several leads are compared side by side. `--fields a,b` picks the fields for one run, `--lead-id` shows one lead, `--by <field>` looks up by another field, `--lists` adds the static lists the lead is on (list id and date added; names aren't in that API, and smart lists can't be read) |
| `mkto leads --from FILE\|-`    | Score, status, owner and last update for a list of emails (one per line, or the first CSV column; `-` reads stdin), one row per email in input order. Up to 300 emails per API call; emails with no lead get a `not found` row, emails shared by several leads one row per lead marked `(dup)`. `--activity` adds LAST TOUCH and 14-day touch counts: per 30 leads, a paging-token call plus one call per page for each batch of 4 activity types (the 10 defaults make 3 batches, about 6 calls plus extra pages; `--web` adds web visits and link clicks). `--csv`, `--json`, `--out`, `--force`. Under the snap the file must be a non-hidden path in your home folder |
| `mkto account <sfdcAccountId>` | Everyone at an account, highest score first: name, title, score, status, owner, last touch and the top touch counts over `--days` (default 14). At most 200 leads (a note says when more were found); more than 3000 is an error asking you to narrow `--by`. Activities cost, per 30 leads, a paging-token call plus one call per page for each batch of 4 activity types (the 10 defaults make 3 batches); `--web` adds 2 types. `--by <field>` finds the leads by another searchable field, `--web`, `--csv`, `--json`, `--out`, `--force`. Lead data stays on your machine |
| `mkto activity <email>`        | Timeline and programs. `--days` (default 14), `--types`, `--all-types`, `--web`, `--all-changes`, `--lead-id`, `--by`, `--all-programs`, `--out`, `--force` |
| `mkto why <email>`             | The score and status changes behind an MQL or DQ, judged by your `[why]` rule: a ledger with reasons and smart campaign names, threshold crossings, and the other activity near each one. `--days` (default 90), `--web`, `--lead-id`, `--by` |
| `mkto programs [--name <s>]`   | All programs, filtered case-insensitively                                    |
| `mkto types [--search <s>]`    | Activity types with their primary and other attribute names; `--search` matches the type name or an attribute name, case-insensitively |
| `mkto fields [--search <s>]`   | Lead fields: REST name (for `lead_fields`, `profile_fields` and `--fields`), label, type, length and read-only; `--search` matches name or label, case-insensitively |
| `mkto quota`                   | Today's usage (US Central day, when the quota resets) and the last 7 days. `--by-user` splits today's calls by API user (no extra call); `--errors` adds today's API error codes (one extra call) |
| `mkto brief <email>`           | The LLM lead brief (see below). Takes `--days`, `--lead-id`, `--by` and `--web` too |

Every data command takes `--json`, and `--verbose` shows request paths, timings and retries, never secrets or emails. `leads` and `account` also take `--csv`; cells that a spreadsheet would run as a formula get a leading `'`. Notes and warnings go to stderr. The full reference is [docs/SPEC.md](./docs/SPEC.md).

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

### Changes in 0.2.0

**New commands**

- **`mkto doctor`** checks your setup in about four API calls: config, credentials, activity types, lead fields and asset (program) access, then the names in `lead_fields`, `profile_fields`, `change_fields`, `activity_types` and `[why]` against your instance. Each problem gets one line with the config key to edit; exit 1 if a check fails.
- **`mkto why <email>`** explains a score or status. It lists the lead's changes to `score_field` and `status_field` (old→new, Δ, reason, smart campaign), marks where the score crossed or dropped below `mql_threshold` and where the status became an MQL or DQ value, and lists other activity within `near_hours` of each mark (with nothing near, the last 3 activities before it, `2d before`). "Near" is timing only, not a cause. It prints the rule it used, so a wrong config shows. Marketing owns the `[why]` section; without it, `why` exits 2 and names the keys to set. Status history comes from Marketo's lead-changes API.
- **`mkto leads --from FILE|-`** looks up a list of emails, up to 300 per API call: score, status, owner and last update, one row per email, `not found` rows kept. `--activity` adds last touch and 14-day counts.
- **`mkto account <sfdcAccountId>`** shows everyone at an account, highest score first, with last touch and touch counts (at most 200 leads).
- **`mkto types`** and **`mkto fields`** list the activity type names and lead fields (REST name and label) your instance has, so you can fill in the config without guessing.

**Activity timeline**

- **Story-first default types:** Fill Out Form, Open Email, Click Email, Email Bounced, Email Bounced Soft, Unsubscribe Email, Change Score, Change Data Value, Change Status in Progression and New Lead (3 API batches of at most 4 types, run at the same time).
- **Web visits are opt-in.** `activity`, `why`, `brief`, `account` and `leads --activity` skip Visit Webpage and Click Link (slow on large instances) unless you pass `--web`, which adds them even when `activity_types` doesn't list them.
- **A call card opens the output:** name, title @ company and lead id; score, status and owner; last touch (`18d ago`), the window's top counts (Email Delivered isn't a touch) and the timezone; and an `open:` link if you set `lead_url`.
- **A detail column says what changed:** score changes (`+25 (60→85) Filled out form`), field changes (`Nurture→MQL`), progression statuses, bounce reasons and clicked links (without query strings). Score, field and progression changes end with the smart campaign that made them, `via "<campaign>"` (one API call per campaign, at most 25 per run; Marketo's docs don't show `campaignId` on activities, so some instances may have nothing to name). `change_fields` limits which field changes the text timeline shows; `--all-changes` and `--json` show them all.
- **Programs are named by their assets and credit:** a program with a `{{my.Name}}` token shows as `<my.Name> (<program name>)` with its `{{my.WPlink}}` link underneath (`name_tokens`, `link_tokens`; one more API call per program, within the cap of 25). `★ acquired` marks the program that created the lead and `✓ success` a program where the lead reached success.

**Lookups**

- **`profile_fields`:** `lead` shows score, status, owner and CRM sync fields after `lead_fields` (empty values skipped). A field your instance lacks is skipped (`--verbose` names it; `doctor` tells you to trim it). `lead --fields a,b` shows just those fields for one run.
- **Look leads up by any searchable field:** `lead`, `activity`, `why` and `brief` take `--by <field>`, e.g. `mkto lead 1001 --by id`. If Marketo refuses the field (error 1003), mkto shows Marketo's message and points to `mkto fields`.
- **Duplicates:** `lead` compares them side by side (only the fields that differ) and takes `--lead-id`; the duplicate note goes to stderr first, so it can't scroll away.
- **`lead --lists`** adds the static lists the lead is on.
- **`quota --by-user`** splits today's calls by API user (no extra call); `--errors` adds today's API error codes.

**Safety and errors**

- **Mistakes cost no quota:** an argument that isn't an email, a bad flag or a taken `--out` file is caught before any API call.
- **`--out` never overwrites silently:** an existing file needs `--force`, and even then it is replaced only when the run succeeds. A failed run leaves no new file, and mkto prints `wrote <path>` when done.
- **Clearer errors:** every missing config key in one go, the template's placeholder host caught, whether a credential or `base_url` came from an environment variable, a network hint for DNS and connection failures, "did you mean" for misspelt commands and activity types, and friendlier `--days` and `--lead-id` messages.
- **`brief`'s payload tells the story:** score changes (numbers only), `status_field` changes and progression statuses; Email Delivered is dropped. See [docs/LLM_LAYER.md](./docs/LLM_LAYER.md).

**Upgrading from 0.1.0**

- `mkto init` now leaves `activity_types`, `lead_fields`, `profile_fields` and the new keys commented out, so you get the current defaults.
- If your `activity_types` is still the unedited v0.1.0 template list, mkto uses the new defaults instead (`--verbose` and `doctor` say so; delete the key to silence them). A list you edited is kept exactly as you wrote it, and unknown types in it still warn.
- `--json` output only gained keys; none were removed.

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
| CI         | `.github/workflows/ci.yml` | Every push and pull request: `test` (tidy, vet, gofmt, race tests), then `snap` (builds amd64 and arm64 snaps, uploaded as the `mkto-snap-amd64` and `mkto-snap-arm64` artifacts) |
| Renovate   | `renovate.json`            | Dependency update PRs for Go modules and GitHub Actions                      |
| Dependabot | `.github/dependabot.yml`   | Weekly Go module and monthly GitHub Actions update PRs                      |

Every action in CI is pinned to a commit SHA.

### Configuration

| Setting                                                   | Default                             | Purpose                                      |
| --------------------------------------------------------- | ----------------------------------- | -------------------------------------------- |
| `--config` / `MKTO_CONFIG`                                | `~/snap/mkto/common/config.toml` (snap), `~/.config/mkto/config.toml` (source) | Which config file to use |
| `MKTO_BASE_URL`, `MKTO_CLIENT_ID`, `MKTO_CLIENT_SECRET`, `MKTO_LLM_API_KEY` | from the file     | Override the file, for CI. Don't type secrets inline in a shell |
| `daily_quota`                                             | `50000`                             | Your subscription's limit; Marketo doesn't report it |
| `lead_fields`, `activity_types`                           | see `mkto init`                     | What `lead` shows and `activity` fetches (REST field names; activity type names from `mkto types`) |
| `profile_fields`                                          | score, status, owner (`sfdcLeadOwnerId`), CRM ids (see `mkto init`); `[]` = none | Extra fields `lead` shows after `lead_fields` (empty values skipped), also used by the `activity`/`why` call card, `leads` and `account`; a name your instance lacks is skipped. Owner is `leadOwnerEmailAddress` when you add it and it has a value, else `sfdcLeadOwnerId` |
| `change_fields`                                           | Lead Status, Lead Score, Company Name; `[]` = all | Which Change Data Value fields the timeline shows, by label (as in `mkto fields`) |
| `name_tokens`, `link_tokens`                              | `["my.Name"]`, `["my.WPlink"]`      | Program tokens shown as a program's title and link; with or without `my.`, any case; `[]` = off |
| `timezone`                                                | `Local`                             | Any IANA name, e.g. `Asia/Tbilisi`           |
| `lead_url`                                                | none                                | `open:` link on the `activity` card. Copy a lead's URL from your browser and put `{id}` where the lead id is; a value without `{id}` is ignored (`--verbose` says so) |
| `[why] score_field`, `mql_threshold`, `status_field`, `mql_values`, `dq_values`, `near_hours` | none (`near_hours` 2) | `mkto why`; set by Marketing. The fields are labels from `mkto fields` |
| `[llm] enabled`, `provider`, `api_key`, `model`           | `false`, `anthropic`, none, `claude-sonnet-5-5` | `mkto brief`                     |

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

# mkto Implementation Plan

> **For agentic workers:** execute with superpowers:subagent-driven-development using the agents in `.claude/agents/` (see "Execution" at the end). Steps use checkbox syntax. Tick `docs/TASKS.md` as tasks land.

**Goal:** Build `mkto` v1 (read-only Marketo CLI) as a strictly confined core26 snap, then the off-by-default `mkto brief` LLM layer (Phases 2–3).

**Architecture:** One Go binary. `cmd/mkto` holds cobra commands and exit-code mapping. `internal/config` resolves, loads and validates the TOML file. `internal/marketo` is the only HTTP client to Marketo (token cache, envelope, retries). `internal/output` owns the stable JSON structs and table rendering. Phase 2 adds `internal/redact` (the only path to an LLM) and `internal/llm` (accepts only redact types).

**Tech stack:** Go (current stable), cobra, go-toml/v2, stdlib `net/http`, `httptest`, `text/tabwriter`, `time/tzdata`. Snapcraft `base: core26`, `confinement: strict`, `go` plugin. Phase 2: Anthropic Messages API via the official Go SDK `github.com/anthropics/anthropic-sdk-go/v2` (approved by Erekle 2026-11-07).

**Spec:** `docs/SPEC.md` (v1), `docs/LLM_LAYER.md` (Phases 2–3), `SNAP_PACKAGING.md`, `docs/REPO.md`. Task 0 applies the corrections below to those files first.

## Decisions (from Erekle, 2026-11-07)

- **Marketo:** production only. Tests use the fake server only. Erekle runs the real-instance checks himself, from a checklist that Claude writes. No raw Marketo responses are ever pasted into the repo.
- **Git:** `snap-mkto` becomes its own repo. Claude commits locally, one task per commit series, using Conventional Commits. **Claude never pushes, tags remotely or opens PRs.**
- **Scope:** Phases 0–3. Phase 2+ runs on invented fixtures until data-protection sign-off.
- **Repo:** https://github.com/canonical/snap-mkto (public, empty on 2026-11-07). Module path: `github.com/canonical/snap-mkto`. The snap and binary are still named `mkto`. LICENSE copyright: Canonical Ltd.
- **Tooling:** the `/snap/bin` launchers are blocked in Claude's shell, so call `/snap/go/current/bin/go` and `/snap/snapcraft/current/bin/snapcraft` directly. If LXD access is blocked too, Erekle runs `snapcraft pack`.

## Global Constraints

- Read-only. Only `GET` to Marketo, plus the identity token call. A test asserts the fake server never sees another method.
- Never log `client_secret`, access tokens, LLM keys or lead emails. `--verbose` prints method, URL path (no query string), status, duration and retry notices.
- Token goes in the `Authorization: Bearer` header. Query-param tokens were removed by Marketo on 2026-08-31.
- Exit codes: `0` ok, `1` error, `2` usage error or not implemented, `3` lead not found, `4` daily quota exhausted.
- Per-request timeout: 30 s. Backoff for 606/615/713/HTTP 502: 5 s, 10 s, 20 s (max 3 retries), then fail. Sleep honours `ctx`.
- Config dir is `0700` and the config file is `0600`. Every file mkto writes (`--out`, `briefs/`) is `0600`.
- Testdata is invented and uses `example.com` / `example.org` only.
- Dependencies: cobra, go-toml/v2, and (Phase 2+) anthropic-sdk-go/v2 only. Ask before adding anything else.
- `gofmt -l .` prints nothing, `go vet ./...` is clean, and `go test -race ./...` passes.
- Golden files live in `testdata/golden/` and are regenerated with `go test ./... -update`.

## Review Focus

1. **`base_url` variants.** `https://x.mktorest.com/rest/`, a trailing `/`, or a bare host all normalise to `https://x.mktorest.com`. A missing scheme or `http://` is an error naming `base_url`. Test: `TestNormaliseBaseURL` (Task 2).
2. **Paging that never ends.** If `moreResult: true` comes back with the same `nextPageToken` twice, or after 1000 pages, stop with an error. Do not loop forever and burn quota. Test: `TestActivitiesPagingGuard` (Task 5).
3. **Non-JSON responses** (an HTML maintenance page, or a 503 with a body). The error reads `unexpected non-JSON response from Marketo (HTTP 503)` and never dumps the body. Test: `TestNonJSONBody` (Task 3).
4. **Ctrl-C during backoff.** A cancelled context returns at once and does not sleep out the 20 s. Test: `TestBackoffHonoursContext` (Task 3).
5. **Messy email input.** Surrounding whitespace is trimmed and `+` is encoded. Mixed case is passed through unchanged. Test: `TestLeadEmailTrimmedAndEncoded` (Task 4).

---

## Phase 0–1: v1 CLI

### Task 0: Apply spec corrections (docs only, no code)

**Files:** Modify `docs/SPEC.md`, `docs/LLM_LAYER.md`, `docs/TASKS.md`, `docs/REPO.md`, `CLAUDE.md`

- [ ] SPEC: quota "today" is the `America/Chicago` calendar date, because the quota resets at 12:00 AM Central (Adobe REST API overview).
- [ ] SPEC: 1006 can be response-level or record-level (`status: "skipped"`, `reasons[]`). Handle both and name the field.
- [ ] SPEC: 713 and HTTP 502 get the same backoff as 606/615. Add a "Non-JSON response" row.
- [ ] SPEC: duplicate leads. `activity` picks the most recent `updatedAt`, lists the other ids, and points to `--lead-id`.
- [ ] SPEC: `--types` and `--all-types` are mutually exclusive (exit 2). `--days` must be ≥ 1.
- [ ] SPEC: `--out` files are `0600`. `--verbose` prints paths without query strings.
- [ ] SPEC: activities get one paging token per batch of ≤ 10 type ids, and batches are merged and sorted by `activityDate`. *(Token reuse across batches is unverified, so one token per batch is the safe choice. With the 10 default types it costs nothing extra.)*
- [ ] SPEC: import `time/tzdata`, so IANA zones work inside the snap whatever the base ships.
- [ ] LLM_LAYER: "Interesting Moment" descriptions are free text. Keep only the type and date, and drop `primaryAttributeValue`. Always fetch `firstName`, `lastName` and `email` for the guard, even when they are not in `lead_fields`. Note that the guard cannot catch *other* people's names in asset names.
- [ ] LLM_LAYER: one verify call returns JSON `{"unsupported":[sentence indexes]}`, not one call per sentence.
- [ ] TASKS: Phase 0 "create `.gitignore`" (it does not exist yet) and "`git init` in snap-mkto".
- [ ] REPO: the owner is a Canonical org, the copyright is Canonical Ltd., and the licence needs confirming with that org.
- [ ] Commit `docs: correct spec against Marketo docs (Nov 2026)`.

### Task 1: Repo bootstrap, skeleton, `version`, `brief` stub

**Files:** Create `.gitignore`, `LICENSE`, `go.mod`, `Makefile`, `.github/workflows/ci.yml`, `.github/dependabot.yml`, `cmd/mkto/main.go`, `cmd/mkto/root.go`, `cmd/mkto/version.go`, `cmd/mkto/brief.go`, `cmd/mkto/exit.go`, `cmd/mkto/main_test.go`

**Interfaces, produces:**
- `type exitError struct{ code int; err error }` and `func exitCode(err error) int` (nil→0, exitError→its code, cobra usage errors→2, anything else→1)
- `func newRootCmd(env Env) *cobra.Command`, where `type Env struct{ Stdout, Stderr io.Writer; Getenv func(string) string; HTTP *http.Client; Now func() time.Time; Sleep func(context.Context, time.Duration) error }`. Every command reads from `Env`, so tests run the whole CLI in-process.
- `func run(args []string, env Env) int` is called from `main()`.
- `var version, commit = "dev", "none"`, set via `-ldflags "-X main.version=… -X main.commit=…"`

Steps:
- [ ] `git init` and `.gitignore` were already done in setup (ledger ruling). LICENSE is the full Apache-2.0 text with copyright `Canonical Ltd.` Module path: `github.com/canonical/snap-mkto`.
- [ ] Tests: `TestVersionHuman` (contains version, commit, `go1.`, `snap: no`), `TestVersionSnap` (Getenv `SNAP_NAME=mkto`, `SNAP_REVISION=x1`, output contains `snap: mkto (x1)`), `TestVersionJSON` (golden), `TestBriefStub` (stderr is exactly `mkto brief is planned for a later release (see docs/LLM_LAYER.md)\n`, exit 2), `TestUnknownFlagExit2`.
- [ ] Implement. Makefile targets are `build`, `test`, `lint` (`go vet` + `test -z "$(gofmt -l .)"`) and `snap`. CI and Dependabot are copied verbatim from REPO.md, test job only.
- [ ] `make lint test` passes. Commit `build: bootstrap module, CI and version command`.

### Task 2: `internal/config` and `mkto init`

**Files:** Create `internal/config/config.go`, `internal/config/template.toml` (embedded), `internal/config/config_test.go`, `cmd/mkto/init.go`, `cmd/mkto/init_test.go`

**Interfaces, produces:**
- `type Config struct{ Marketo Marketo; Output Output; LLM LLM }`, `Marketo{ BaseURL, ClientID, ClientSecret string; DailyQuota int; LeadFields, ActivityTypes []string }`, `Output{ Timezone string }`, `LLM{ Enabled bool; Provider, APIKey, Model string }`
- `func Path(flag string, getenv func(string) string) (string, error)`: flag → `MKTO_CONFIG` → `$SNAP_USER_COMMON/config.toml` → `os.UserConfigDir()/mkto/config.toml`
- `func Load(path string, getenv func(string) string, warn io.Writer) (*Config, error)`: decode, apply env overrides, normalise `base_url`, apply defaults (`daily_quota` 50000 if 0; default fields and types from the template), validate, check permissions
- `func (c *Config) Location() (*time.Location, error)`
- `func WriteTemplate(path string, force bool) error`

Steps:
- [ ] Tests: `TestPathOrder` (four cases), `TestEnvOverrides`, `TestNormaliseBaseURL` (Review Focus 1), `TestMissingKey` (error contains `client_secret` and the path), `TestPermissionWarning` (0644 → stderr contains `chmod 600 <path>`, 0600 → nothing), `TestBadTimezone`, `TestTemplateParses` (the embedded template loads after the secrets are filled in).
- [ ] `cmd/mkto` tests: `TestInitModes` (dir 0700, file 0600; prints the path and next steps), `TestInitRefusesOverwrite` (exit 1, message mentions `--force`), `TestInitForce`.
- [ ] Implement. The template is the TOML from SPEC §Format, with comments.
- [ ] Green, then commit `feat(config): path resolution, load, validation and init`.

### Task 3: `internal/marketo` core client and fake server

**Files:** Create `internal/marketo/client.go`, `internal/marketo/errors.go`, `internal/marketo/client_test.go`, `internal/marketotest/server.go`, `testdata/marketo/*.json`

**Interfaces, produces:**
- `type Options struct{ HTTP *http.Client; Log io.Writer; Now func() time.Time; Sleep func(context.Context, time.Duration) error }` (nil Log means quiet)
- `func New(baseURL, clientID, clientSecret string, o Options) *Client`
- `func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error`: unexported; envelope `{success, errors[], warnings[], result, moreResult, nextPageToken}`; retries per Global Constraints
- `var ErrQuotaExhausted = errors.New(...)`; `type APIError struct{ Code, Message string }`; `type FieldError struct{ Field string }` (message: `field "x" not found: fix lead_fields in <config>`)
- `marketotest.New(t) *Server` with `Handle(path string, responses ...Response)` (served in order, last one repeated), `Hits(path) int`, `Methods() []string` and `URL`. Fixtures are loaded from `testdata/marketo/`.

Steps:
- [ ] Before writing fixtures: the `marketo-api-checker` agent confirms each envelope and error shape against Adobe docs and records the source URL in `testdata/marketo/README.md`.
- [ ] Tests: `TestTokenCached` (3 calls → 1 identity hit), `TestTokenRefreshBeforeExpiry` (`expires_in` 100, Now +41 s → refresh), `TestBearerHeader` (the header is set and no `access_token` query param), `TestRefreshOn601Once`, `TestRefreshOn602ThenFail`, `TestBackoff606` (sleeps 5 s, 10 s, then success), `TestGiveUp615` (3 retries → error mentions rate limit), `TestRetry713`, `TestRetryHTTP502`, `TestQuota607` (`errors.Is(err, ErrQuotaExhausted)`, 0 retries), `TestSuccessFalseIn200`, `TestNon200`, `TestTimeout`, `TestNonJSONBody` (Review Focus 3), `TestBackoffHonoursContext` (Review Focus 4), `TestVerboseNoSecrets` (the log does not contain the secret, the token or `@`), `TestOnlyGET`.
- [ ] Implement. Commit `feat(marketo): client with token cache, envelope and retries`.

### Task 4: `mkto lead`

**Files:** Create `internal/marketo/leads.go`, `internal/output/output.go`, `cmd/mkto/lead.go`, `cmd/mkto/lead_test.go`, `testdata/golden/lead*.{txt,json}`

**Interfaces, produces:**
- `type Lead map[string]any`, `func (l Lead) ID() int`
- `func (c *Client) LeadsByEmail(ctx, email string, fields []string) ([]Lead, error)`
- `cmd/mkto/clients.go`: `func newClient(cmd *cobra.Command, env Env) (*config.Config, *marketo.Client, error)` (shared by all data commands)
- `output.LeadJSON{ ID int; Fields map[string]any }`, `func output.WriteJSON(w io.Writer, v any) error` (indented, stable), `func output.Table(w io.Writer, rows [][]string) error`

Steps:
- [ ] Tests: `TestLeadEmailTrimmedAndEncoded` (`" jane+test@example.com "` → the server sees `filterValues=jane+test@example.com`), `TestLeadOne` (golden txt + json), `TestLeadDuplicates` (two blocks plus a duplicates note, golden), `TestLeadNotFound` (exit 3, stderr names the email), `TestLead1006ResponseLevel`, `TestLead1006RecordLevel`, `TestLeadQuotaExit4`.
- [ ] Implement. Fields print in `lead_fields` order and timestamps are shown in `output.timezone`. Commit `feat(lead): lead profile command`.

### Task 5: activity types and activities

**Files:** Create `internal/marketo/activities.go`, `internal/marketo/activities_test.go`

**Interfaces, produces:**
- `type ActivityType struct{ ID int; Name string }`; `func (c *Client) ActivityTypes(ctx) ([]ActivityType, error)`
- `func ResolveTypes(all []ActivityType, names []string) (ids []int, unknown []string)` (case-insensitive name match)
- `type Activity struct{ ID int; Date time.Time; TypeID int; Primary string; Attributes map[string]any }`
- `func (c *Client) Activities(ctx, leadID int, typeIDs []int, since time.Time) ([]Activity, error)`: batches of ≤10, one `pagingtoken.json` per batch, loop while `moreResult`, merge, sort by `Date` then `ID`

Steps:
- [ ] Tests: `TestResolveTypesUnknown`, `TestActivitiesBatching` (23 ids → 3 batches `10,10,3`, 3 paging-token hits), `TestActivitiesMoreResult` (page 1 is empty with `moreResult:true`, page 2 has 2 rows → 2 results), `TestActivitiesMergedSorted`, `TestActivitiesPagingGuard` (Review Focus 2).
- [ ] Implement. Commit `feat(marketo): activity types and paged activities`.

### Task 6: `mkto activity` with programs section

**Files:** Create `internal/marketo/programs.go` (membership + program by id), `cmd/mkto/activity.go`, `cmd/mkto/activity_test.go`, goldens

**Interfaces, produces:**
- `type Membership struct{ ProgramID int; Status string; ... }`; `func (c *Client) Memberships(ctx, leadID int) ([]Membership, error)` (follows `nextPageToken` if present)
- `type Program struct{ ID int; Name, Type, Channel, Workspace string; CreatedAt time.Time }`; `func (c *Client) Program(ctx, id int) (Program, error)`
- `output.ActivityJSON{ Lead LeadJSON; Since time.Time; Events []EventJSON; Programs []MembershipJSON; Notes []string }`

Steps:
- [ ] `marketo-api-checker` confirms the programMembership response shape (Adobe's reference didn't load on 2026-11-07, so it needs another source). If it can't be confirmed, mark the fixture "unverified" and add it to Erekle's real-instance checklist.
- [ ] Tests: `TestActivityTimelineGolden` (Asia/Tbilisi, txt + json), `TestActivityDuplicatesPicksLatest` (note lists the other ids), `TestActivityLeadIDFlag`, `TestActivityDaysOver90Warns`, `TestActivityDaysZeroExit2`, `TestActivityTypesAndAllTypesExit2`, `TestActivityAllTypesWarns`, `TestActivityUnknownTypeWarns`, `TestMembershipCap25` (30 memberships → 25 program hits, note `showing names for 25 of 30 programs`), `TestProgramLookupCached` (same id twice → 1 hit), `TestActivityOutFile` (file mode 0600, stdout empty), `TestActivityHelpMentionsRetention`.
- [ ] Implement. Commit `feat(activity): timeline and program memberships`.

### Task 7: `mkto programs` and `mkto quota`

**Files:** Create `internal/marketo/usage.go`, add `Programs(ctx)` to `programs.go`, `cmd/mkto/programs.go`, `cmd/mkto/quota.go`, tests, goldens

**Interfaces, produces:**
- `func (c *Client) Programs(ctx) ([]Program, error)`: `maxReturn=200`, offset paging until `result` is empty (don't rely on code 702)
- `type UsageDay struct{ Date string; Total int }`; `func (c *Client) UsageToday(ctx) (UsageDay, error)` (sums `total` across users); `func (c *Client) UsageLast7Days(ctx) ([]UsageDay, error)`

Steps:
- [ ] Tests: `TestProgramsOffsetPaging` (200 + 200 + 13 + empty → 413 rows, 4 hits), `TestProgramsNameFilter` (`WEBinar` matches `2026-Q4 Webinar`), `TestProgramsGolden`, `TestQuotaGolden` (used, limit, remaining and percent; says the limit comes from the config), `TestQuotaChicagoDay` (Now `2026-11-07T05:30Z` → the "today" header shows `2026-11-06`), `TestQuotaOverLimit` (remaining 0, no negative).
- [ ] Implement. Commit `feat: programs and quota commands`.
- [ ] **v1 gate:** the `spec-reviewer` checks every SPEC §Testing bullet against a named test. The `security-auditor` runs a full pass. README matches reality.

## Phase 1.5: Snap

### Task 8: snapcraft.yaml, build, confinement

**Files:** Create `snap/snapcraft.yaml`, `docs/REAL_INSTANCE_CHECKLIST.md`. Modify `SNAP_PACKAGING.md`, `.github/workflows/ci.yml`, `Makefile`, `README.md`

Prerequisites (Erekle runs these, since they need sudo): `sudo snap install go --classic`, `sudo snap install snapcraft --classic`, `lxd init --auto` if it isn't already set up.

- [ ] The `snap-engineer` agent checks every snapcraft key against the current docs: the core26 go plugin, `build-snaps` channel, and ldflags via `override-build` or plugin options. It records the URLs it checked in SNAP_PACKAGING.md.
- [ ] `snapcraft pack` succeeds. `unsquashfs -l` shows `bin/mkto`. `mkto version` inside the snap reports `snap: mkto (xN)`.
- [ ] Erekle runs `sudo snap install --dangerous ./mkto_*.snap`.
- [ ] Confinement checks without Marketo:
  - `mkto init` writes `~/snap/mkto/common/config.toml` with mode 0600.
  - `--out ~/x.json` works.
  - `--out /tmp/x.json` lands in the snap's private /tmp. Document it.
  - Asia/Tbilisi renders correctly.
  - `journalctl` / `snappy-debug` show no denials, or each one is documented.
- [ ] Write `docs/REAL_INSTANCE_CHECKLIST.md`: exact commands for Erekle against production (`quota`, `lead`, `activity`, `programs`, `--json`), what to look for, and "never paste output into the repo or the chat if it contains lead data". Erekle reports pass/fail and error text only.
- [ ] SNAP_PACKAGING.md: remove the draft note and add the verified yaml, commands and sample output (from invented data). Add the CI `snap` job and the `source-code` / `issues` URLs (once the org is confirmed).
- [ ] Commit `build(snap): strict core26 snap`. Erekle tags `v0.1.0` and creates the release. Claude prepares the release notes.

## Phase 2–3: brief (expanded 2026-11-07, ruling R4)

API facts (claude-api skill, cached 2026-10-06; implementers re-check https://docs.claude.com before coding):
- POST https://api.anthropic.com/v1/messages, headers x-api-key, anthropic-version, content-type. Model from llm.model (default claude-sonnet-5-5).
- Sonnet 5.5: thinking adaptive by default; {type:"disabled"} is a 400; temperature/top_p/top_k rejected; forced tool_choice any/tool is a 400; effort via output_config.effort (default high).
- Structured JSON: output_config.format (json schema). Not combinable with citations.
- Web search: {type:"web_search_20260209", name:"web_search", max_uses:5}. Results come back as web_search_tool_result blocks; errors are HTTP 200 with an error object in content (branch: list = ok, object = error). Text blocks carry citations (url, title).
- stop_reason "refusal" (stop_details.category) must be handled: brief prints "the model declined this step" and continues without it. Erekle decided 2026-11-07: NO server-side fallbacks (no beta header).
- Usage: usage.input_tokens / output_tokens (+cache fields) → --verbose per step and total.
- Erekle decided 2026-11-07: use the official Go SDK github.com/anthropics/anthropic-sdk-go/v2 (approved dependency). Implementers read the claude-api skill go/claude-api/*.md before coding; the request shapes above become SDK params.

### Task 9: internal/redact
Files: internal/redact/{redact.go,title.go,url.go,guard.go}_test.go
Interfaces:
- `type Payload struct{ /* unexported */ }`, `type CompanyQuery struct{ /* unexported */ }`
- `func Build(l marketo.Lead, acts []marketo.Activity, types map[int]string) (Payload, error)`, `func (p Payload) Company() CompanyQuery` (the only way to get a CompanyQuery: built from the already-guarded Payload), `func TitleBucket(title string) string`, `func ScrubURL(raw string) string`, `func (p Payload) JSON() []byte`, `func (q CompanyQuery) JSON() []byte`
- plus `type Event struct{ ID string /*E1..*/; Date time.Time /*hour, UTC*/; Type, Asset, URL string }` (unexported fields in Payload; accessor JSON() only).
Rules (LLM_LAYER §Redaction + Task 0 corrections): allowlist profile {company, industry, country, numberOfEmployees, title bucket}; events: type, date truncated to hour UTC, primary for asset types; webpage/link → ScrubURL; Interesting Moment → type+date only; everything else dropped. Guard: emails, phones (≥9 digits w/ separators, leading +), IPv4/IPv6, lead first/last name (len≥3, case-insensitive), email local part → error naming the JSON path, never the value.
Tests: every LLM_LAYER §Tests case + Interesting Moment dropped + error never contains the value + `go vet` check that internal/llm exported funcs take only redact types (a test using go/parser over internal/llm).

### Task 10: brief --show-payload + enabled gate
cmd/mkto/brief.go replaces the stub. Fetch: lead (lead_fields + firstName,lastName,email,title,company,industry,country,numberOfEmployees), activities (--days default 90); no memberships (memberships are not part of the redacted payload). --show-payload prints Payload.JSON() and CompanyQuery.JSON(), exit 0, zero LLM calls, works with enabled=false. Without it and enabled=false: exit 1 `mkto brief is off. Read the sign-off section in docs/LLM_LAYER.md, then set [llm] enabled = true`. MKTO_LLM_API_KEY overrides llm.api_key; missing key with enabled=true → error naming the key.

### Task 11: internal/llm provider (Anthropic)
Interfaces:
- `type Provider interface{ Narrate(ctx, redact.Payload) (Narrative, Usage, error); Verify(ctx, redact.Payload, Narrative) ([]int, Usage, error); Research(ctx, redact.CompanyQuery) (Research, Usage, error); Pitch(ctx, PitchInput) (Pitch, Usage, error) }`
- Implemented with anthropic-sdk-go/v2 (Erekle-approved); model only from `llm.model`.
 Fake Anthropic httptest server in internal/llmtest. Per-step timeouts (narrate/verify 60 s, research 120 s), max_tokens per step, key masking (never in logs/errors; same redirect + transport-error rules as marketo). Prompts go:embed + override from <config dir>/prompts/<name>.md.

### Task 12: narrate, verify, render
Narrate: output_config.format schema {sentences:[{text, cites:[E ids]}]}. Deterministic check (every sentence ≥1 cite, all cites exist) drops failures. Verify: one call → {"unsupported":[indexes]}; drop them; --verbose lists "Removed claims". Render markdown per LLM_LAYER §Output shape; --json struct. Goldens.

### Task 13: research
CompanyQuery only; web_search_20260209 max_uses 5; output JSON {summary,size,industry,signals[],news[],sources[{url,title}]}; claims whose source index isn't in sources are dropped; tool error object → "research unavailable: <code>"; --no-research skips.

### Task 14: playbook, pitch, --save
playbook.md from config dir (never embedded; test greps go:embed targets). ≤3 offerings, each tied to E<n> or [n]; opener; "What we don't know". Missing playbook → one-line skip. --save → briefs/<UTC 20060102T150405Z>-<leadid>.md, dir 0700, file 0600, test asserts no name/email in the file. Update README, SNAP_PACKAGING.md, LLM_LAYER.md.

---

## Execution

The orchestrator (main session) runs each task like this:

1. `go-builder` implements the task with TDD and reports which files changed and the test output.
2. `spec-reviewer` checks it against this plan and the spec, and runs the lint and tests. Anything it rejects goes back to step 1.
3. `security-auditor` runs on Tasks 3, 4, 6, 8, 9, 10, 11, 14 and on every phase gate.
4. `marketo-api-checker` runs before any new endpoint or fixture (Tasks 3, 4, 5, 6, 7). `snap-engineer` owns Task 8.
5. `go-builder` commits locally (ruling: the SDD flow reviews BASE..HEAD). Nothing is ever pushed.

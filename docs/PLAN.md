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

---

## Phase 4: usability (2026-11-08)

### T43: drop the lead-changes walk from why (2026-11-09)

Erekle's real-instance `why` (90 days) took 3 min 22 s: the activity batches took about 100 s, then the `leadchanges.json` walk (T33) took about as long again. T33 existed only because the Change Data Value attribute names were UNVERIFIED. They are now VERIFIED (`mkto types`: `Old Value`, `New Value`, `Reason`), and the ledger already showed every Lead Status change from those rows. So delete `statusFromLeadChanges`, `Client.LeadChanges`, their tests and the fixture. Expected: `why` over 90 days takes about 100 s, at about 37 fewer calls.

### T42: real-instance polish for why, owner and change_fields (2026-11-09)

From Erekle's first `mkto why` run on a real MQL:
- **Reasons:** Marketo reasons run to about 110 characters (`Changed by Smart Campaign Run Action Change Data Value <date> action …`), so the ledger is wider than a terminal. In the text table, cap REASON and CAMPAIGN at 60 characters with `…`; `--json` keeps them in full.
- **Nothing near:** the MQL mark had no `near:` lines (nothing within ±2 h). When a mark has no near events, show up to 3 `before:` lines: the last non-change activities before the mark in the window, each with how long before (`3h before`, `2d before`). This is pure story logic: `Mark.Before []marketo.Activity`.
- **Owner:** the instance has no `leadOwnerEmailAddress`. Erekle chose `sfdcLeadOwnerId` (a Salesforce user id). Make it the default profile field instead of `leadOwnerEmailAddress`. The card, `leads` and `account` show the first non-empty of `leadOwnerEmailAddress` and `sfdcLeadOwnerId` as owner, so instances that have the email still show it.
- **`change_fields`:** Erekle's list is `["Lead Status", "Lead Score", "Company Name"]`. It becomes the default when the key is absent; `change_fields = []` shows every field, and `--all-changes` still does.

### T41: activity batches by speed, 14-day default (real instance, 2026-11-09)

Measured on Erekle's production instance:
- page count depends only on the window, about 0.4 pages a day (12 pages for 30 days, for every type group);
- page time depends on the types per call: ≤4 types about 2.8 s, the 10 default types about 29 s.

Decision (Erekle): activity calls carry at most 4 type ids (not Marketo's limit of 10). Batches run concurrently, at most 3 at a time. `activity`, `account` and `leads --activity` default to 14 days; `why` keeps 90. Expected: a 14-day lookup takes about 17 s and about 21 activity calls; a 30-day one about 35 s and 39 calls.


Source: `docs/USABILITY_REVIEW.md` (the 20 ideas, plus Erekle's decisions D1–D6). Every task follows the Execution steps above: `go-builder` with TDD, then `spec-reviewer`, then `security-auditor` where marked 🔒, then the orchestrator commits. Until the gate task (T40) folds Phase 4 into `docs/SPEC.md`, this section is the spec for Phase 4. *(Folded in 2026-11-08: `docs/SPEC.md` is now the spec, and its §Deviations from the plan lists where the code differs from this section.)*

**Pending from Erekle:**
- ~~The `change_fields` list~~: settled 2026-11-09 (T42): Lead Status, Lead Score, Company Name.
- Results of the real-instance run, `docs/REAL_INSTANCE_CHECKLIST.md` Part C (C1–C17); C10 decides whether web primaries get their query strings stripped.
- Snap Store publishing: `snapcraft register mkto` and the first upload (D1, T39).

**API check results (marketo-api-checker, 2026-11-08; details in `testdata/marketo/README.md`):**
- VERIFIED:
  - `reachedSuccess` and `acquiredBy` on programMembership
  - the smartCampaign by-id shape
  - the tokens-by-folder path and shape
  - describe.json
  - listMembership
  - leadchanges.json (`fields[{id,name,newValue,oldValue}]`, so T33 goes ahead)
  - usage `users[{userId,count}]` and errors `errors[{errorCode,count}]`
  - 603
  - 414 above 8 KB
- UNVERIFIED:
  - `campaignId` on activities (absent from every docs example)
  - attribute names for Change Score, Change Data Value, Click/Open Email, bounces and progression
  - type ids other than 2
  - describe2 (path and response: T25 has no `--searchable`)
  - leads.json paging for filterType queries
  - most standard field names beyond email, names, title, phone and unsubscribed
- Token names: the docs example has no `my.` prefix. Match token names with or without a leading `my.` (case-insensitive).
- Error 1003: the docs disagree ("Too many results match the filter" on the leads page, "Invalid data" in the error table). Never hard-code a meaning. Show Marketo's message plus `check the field is searchable: mkto fields`.
- Profile fields: each one missing on an instance costs one 1006 retry per run. `doctor` (T26) must list them so the user can trim `profile_fields`.

**Phase 4 constraints (in addition to the Global Constraints):**
- GET only. No `_method=GET` POST override; batch sizes keep URLs under 7 KB instead.
- Fixtures come from `marketo-api-checker`. Attribute names and shapes it marks UNVERIFIED go on the real-instance checklist (Part C) and are never treated as facts in user-facing text.
- One view model drives both outputs: text and `--json` are rendered from the same structs. New `--json` fields are `omitempty` (existing goldens may grow but never lose keys).
- No new dependencies. Edit distance, CSV and relative time all come from the stdlib or a few lines of code.
- Lead data stays local (D5). `account` and `leads` never feed `brief`.
- `internal/story` is pure: stdlib plus `marketo` types, no I/O, and `now` is passed in.

### Phase 4A: free wins (no new endpoints)

#### T15: `--web` on activity, web types off by default
**Files:** `cmd/mkto/clients.go`, `cmd/mkto/activity.go`, `cmd/mkto/brief.go`, tests
**Interfaces:**
- `func dropWebTypes(names []string, web bool) (kept []string, note string, err error)`, lifted from `brief.go:112-124` with the same messages; brief calls it.
- `activity --web`. `--types` and `--all-types` ignore the filter: an explicit choice wins.
**Tests:** `TestActivityDefaultSkipsWebTypes` (the `activityTypeIds` query lacks the Visit Webpage/Click Link ids; the note is on stderr), `TestActivityWebFlagIncludes`, `TestActivityExplicitTypesKeepWeb`, plus the existing brief tests still pass.
**Commit:** `feat(activity): web visits and link clicks opt-in via --web`

#### T16: story-first defaults and migration of v0.1.0 lists (D6)
**Files:** `internal/config/config.go`, `internal/config/template.toml`, `internal/config/config_test.go`, `README.md` (a "Changes in 0.2.0" note)
**Interfaces:**
- `var DefaultActivityTypes = []string{"Fill Out Form","Open Email","Click Email","Email Bounced","Email Bounced Soft","Unsubscribe Email","Change Score","Change Data Value","Change Status in Progression","New Lead"}`. That is exactly 10, one batch.
- `var DefaultLeadFields`: today's list.
- `legacyActivityTypes [][]string`: the v0.1.0 template lists, with and without "Interesting Moment".
- `Marketo.ActivityTypesDefaulted bool` with `toml:"-"`. It is true when the key is absent or equal (case-insensitive, in order) to a legacy list.
- The template comments out `activity_types` and `lead_fields`, showing the defaults.
**Behaviour:**
- `activity` and `brief` print `warning: unknown activity type` only when the list was set by the user. A defaulted list gives one `--verbose` note: `note: default activity types not on this instance: <names>`.
- A migrated legacy list gives one `--verbose` note naming the change.
**Tests:** `TestLegacyActivityTypesMigrated`, `TestCustomActivityTypesKept`, `TestTemplateDefaultsCommented` (the template still parses and its commented lists equal the Go vars), `TestDefaultTypesOneBatch` (Hits on pagingtoken == 1), `TestDefaultMissingTypeQuiet`.
**Commit:** `feat(config): story-first default activity types; migrate v0.1.0 lists`

#### T17: detail column (`internal/story`) 🔒
**Files:** create `internal/story/detail.go` and `detail_test.go`; edit `cmd/mkto/activity.go`, `internal/output/output.go`, goldens
**Interfaces:**
- `func Detail(typeName string, a marketo.Activity) string`. It takes the whole activity because Change Data Value's field name is the primary value (`Attribute Name`), not an attribute. Lower-cased type name → detailer:
  - Change Score: `+25 (60→85) Filled out form`
  - Change Data Value: `Nurture→MQL` (the field name is already in the primary column)
  - Change Status in Progression: `→ Attended` (with ` ✓ success` when Success is true)
  - Click Email: the link
  - Email Bounced and Email Bounced Soft: category + details
  - Fill Out Form: none (primary already names the form)
  - Unknown types: `""`.
- Detailers read only named attributes. Client IP Address, User Agent, Form Fields and Query Parameters are never read. Attribute names come from the checker's fixture.
- `func ChangeField(a marketo.Activity) string` returns the Change Data Value field name (for filtering).
- Lines are trimmed of trailing spaces inside `writeActivity`, so an empty detail leaves no padding; `output.Table` is unchanged.
- PII names follow the fixtures: Client IP Address, User Agent, Form Fields, Query Parameters, Referrer URL. Raw `attributes` in `--json` are unchanged (as in 0.1.0).
- `output.EventJSON.Detail string 'json:"detail,omitempty"'`.
- Config `Marketo.ChangeFields []string` (`change_fields`). When it is non-empty, the text timeline hides Change Data Value rows for other fields. `--all-changes` shows them all. `--json` always has every row. The default is empty, which shows all; Erekle supplies the list later.
**Tests:**
- `TestDetailTable`: each type, missing attributes, wrong types (string vs number values).
- `TestDetailNeverReadsPII`: attributes contain 192.0.2.10, a UA string and form fields; none appear in any output.
- `TestActivityStoryGolden` (txt + json) using `activities_story_page.json` and `activity_types_story.json`.
- `TestChangeFieldsFilter`, `TestAllChangesFlag`.
**Commit:** `feat(activity): per-type detail column (score deltas, field changes, statuses)`

#### T18: decode campaignId and primaryAttributeValueId
**Files:** `internal/marketo/activities.go`, `internal/output/output.go`, tests
**Interfaces:** `Activity.CampaignID int`, `Activity.PrimaryID int`. `EventJSON.CampaignID int 'json:"campaignId,omitempty"'` and `EventJSON.PrimaryID int 'json:"primaryId,omitempty"'`.
**Tests:** `TestActivitiesDecodeCampaignID`; the story JSON golden is updated.
**Commit:** `feat(marketo): keep campaignId and primaryAttributeValueId on activities`

#### T19: profile fields and `--fields`
**Files:** `internal/config/config.go`, `template.toml`, `cmd/mkto/clients.go`, `cmd/mkto/lead.go`, `cmd/mkto/activity.go`, tests
**Interfaces:**
- Config `Marketo.ProfileFields []string` (`profile_fields`). The default is `["leadScore","leadStatus","leadOwnerEmailAddress","unsubscribed","emailInvalid","sfdcLeadId","sfdcContactId","sfdcAccountId"]`; the checker confirms the names.
- Profile fields are extras for `leadsByEmail`. A missing one is dropped and retried, with a `--verbose` note; never fatal.
- `lead --fields a,b` replaces lead_fields + profile_fields for one run. Fields named this way are treated as the user's own, so a missing one is an error naming the field.
- `lead` shows profile fields after lead_fields and skips empty values.
- `brief` never adds profile fields to its fetch (the redact allowlist is unchanged).
**Tests:** `TestLeadShowsProfileFields`, `TestProfileFieldMissingSkipped` (one retry, exit 0), `TestLeadFieldsFlag`, `TestLeadFieldsFlagMissingErrors`, `TestBriefIgnoresProfileFields`.
**Commit:** `feat(lead): score, status, owner and sync fields by default; --fields`

#### T20: call card header
**Files:** create `internal/story/summary.go` and its test; edit `cmd/mkto/activity.go`, `cmd/mkto/lead.go`, `internal/config` (`Output.LeadURL`, key `lead_url`), goldens
**Interfaces:**
- `func Summarize(acts []marketo.Activity, typeName map[int]string, now time.Time) Summary` with `Summary{Counts map[string]int; LastTouch time.Time}`. "Touch" means any non-Email-Delivered activity.
- `func Ago(t, now time.Time) string` gives `just now`, `5m ago`, `3h ago`, `2d ago`, or the date when 30 days or more.
- Header line 1: `Jane Doe · Platform Engineer @ Example Corp`.
- Header line 2: profile values that are present, e.g. `score 85 · MQL · owner a.rep@example.com`.
- Header line 3: `last touch 2d ago · 30d: 3 opens, 1 click, 1 form · times in Asia/Tbilisi`.
- Then `lead_url` with `{id}` replaced by the lead id, when set. The UI path isn't verified, so mkto never builds it: the user copies a lead's URL from the browser and puts `{id}` where the id goes.
- The duplicate note moves to stderr before stdout output (for `lead` and `activity`); in `--json` it stays in `notes`.
- `lead`'s duplicate note says `use --lead-id` (T27 adds the flag to lead; until then the text stays as it is).
**Tests:** `TestAgo` (table), `TestSummarizeSkipsDelivered`, `TestActivityCallCardGolden`, `TestLeadDuplicateNoteOnStderr`, `TestCallCardNoProfileFields` (lines with nothing to show are left out).
**Commit:** `feat(activity): call card header with last touch, counts and record link`

#### T21: fail before spending quota, safe `--out` 🔒
**Files:** `cmd/mkto/activity.go`, `cmd/mkto/lead.go`, `cmd/mkto/brief.go`, `cmd/mkto/clients.go`, tests
**Interfaces:**
- `func checkEmail(s string) error`: one `@`, non-empty local part, a domain containing a dot, no spaces (a format check only). Exit 2: `"<x>" doesn't look like an email address`. It runs before `newClient` in lead, activity and brief.
- `--out` is opened before any Marketo call: `O_WRONLY|O_CREATE|O_EXCL`, mode 0600. `--force` allows overwriting: a 0600 temp file (`mkto-out-*.tmp`, not hidden, for the snap home plug) in the same directory is renamed over the target on success, so a failed run never touches the old file.
- If the run fails after mkto created the file, the file is removed.
- On success, stderr gets `wrote <abs path>` (plus the snap /tmp note).
- brief stops re-requesting `numberOfEmployees` inside a run after a 1006 (it is already dropped per run; check the brief → leadsByEmail path for a second lookup and remove the duplicate call).
**Tests:**
- `TestBadEmailNoCalls` (`Hits(IdentityPath)==0`)
- `TestOutUnwritableNoCalls`
- `TestOutExistsRefuses` (exit 2, file unchanged), `TestOutForceOverwrites`
- `TestOutRemovedOnFailure`
- `TestOutPrintsWrote`
- `TestBriefOneLeadLookup` (hits on leads.json == 1 when the fields exist)
**Commit:** `fix: check email and --out before calling Marketo; never overwrite silently`

#### T22: config errors that list everything
**Files:** `internal/config/config.go`, `cmd/mkto/root.go`, tests
**Behaviour:**
- Every missing key is reported in one error (`errors.Join`).
- `123-ABC-456` in base_url is rejected: `marketo.base_url is still the template placeholder`.
- A credential that came from an env var is named in auth errors: `HTTP 401 … check MKTO_CLIENT_SECRET`.
- `--config` help shows the resolved path: `default: ~/snap/mkto/common/config.toml` under the snap.
- `daily_quota = "50k"` gives `marketo.daily_quota must be a whole number` instead of the raw go-toml text.
- A leading `/rest/v1` in base_url is normalised away, not rejected.
**Tests:** `TestAllMissingKeysListed`, `TestPlaceholderBaseURL`, `TestEnvSecretNamedOn401`, `TestConfigHelpShowsPath`, `TestDailyQuotaTypeError`, `TestNormaliseRestV1`.
**Commit:** `fix(config): list every missing key, catch the template placeholder`

#### T23: error polish
**Files:** `cmd/mkto/root.go`, `cmd/mkto/brief.go`, tests
**Behaviour:**
- Unknown commands get cobra's "Did you mean" (the root Args must return cobra's `unknown command` error with suggestions).
- `--days 2w` gives `--days takes a whole number of days, e.g. --days 30`; `--lead-id x` gives `--lead-id takes a lead id number, e.g. --lead-id 1001`.
- brief-off lists the steps inline (set `[llm] enabled = true` and `api_key` in `<config path>`; preview with `--show-payload`) and links https://github.com/canonical/snap-mkto/blob/main/docs/LLM_LAYER.md.
- `connection refused` / DNS errors get `check marketo.base_url and your network`.
**Tests:** `TestUnknownCommandSuggests`, `TestDaysParseFriendly`, `TestBriefOffInlineSteps`, `TestDialErrorHint`.
**Commit:** `fix: suggestions for typos, friendlier flag and network errors`

### Phase 4B: discovery (Erekle runs these on the real instance before Phase 4C)

#### T24: `mkto types [--search]` and did-you-mean
**Files:** `internal/marketo/activities.go` (`ActivityType` gains `Description`, `Primary string`, `Attributes []string`), create `cmd/mkto/types.go` and its test, `cmd/mkto/activity.go`
**Behaviour:**
- Table: id, name, primary attribute, attribute names. `--search` is a case-insensitive substring match on name or attributes. `--json` uses `[]output.TypeJSON`.
- An unknown type in `--types`/activity_types warns `… skipped; did you mean "Change Score"?` when the closest name has Levenshtein ≤ 3 or is a substring match.
**Tests:** `TestTypesGolden`, `TestTypesSearch`, `TestUnknownTypeSuggests`, `TestLevenshtein`.
**Commit:** `feat: mkto types lists activity types; suggest close names`

#### T25: `mkto fields [--search]`
**Files:** `internal/marketo/leads.go` (`DescribeLeads`), create `cmd/mkto/fields.go` and its test, `output.FieldJSON`
**Interfaces:** `type Field struct{ Name, Label, Type string; Length int; ReadOnly bool }`; `func (c *Client) DescribeLeads(ctx) ([]Field, error)`. If the checker verifies describe2, `--searchable` filters to the searchable fields; otherwise there is no such flag.
**Tests:** `TestFieldsGolden`, `TestFieldsSearch` (`--search score` gives the score rows).
**Commit:** `feat: mkto fields lists lead fields from describe`

#### T26: `mkto doctor`
**Files:** create `cmd/mkto/doctor.go` and its test
**Behaviour:**
- Checks, in order: config loads, then token, then `activities/types.json`, then `leads/describe.json`, then `asset/v1/programs.json?maxReturn=1`. About 4 calls.
- Then it cross-checks lead_fields, profile_fields, change_fields and activity_types against describe and types, listing those missing on this instance with the config key to edit.
- One line per check (`✓`/`✗` plus hint). The 603 hint is `ask Marketing Ops to give the API role Read-Only Assets`.
- Exit 1 if any check fails. `--json` uses `output.DoctorJSON`.
- README quickstart: `mkto init`, edit, then `mkto doctor`.
**Tests:** `TestDoctorAllGood`, `TestDoctorAssetDenied` (`error_603.json`), `TestDoctorMissingFields`, `TestDoctorBadCredentials`.
**Commit:** `feat: mkto doctor checks credentials, permissions and configured names`

### Phase 4C: scoring and attribution

#### T27: lookup by other keys, `lead --lead-id`, side-by-side duplicates
**Files:** `internal/marketo/leads.go`, `cmd/mkto/clients.go`, `lead.go`, `activity.go`, `brief.go`, tests
**Interfaces:**
- `func (c *Client) Leads(ctx, filterType string, values []string, fields []string) ([]Lead, error)` follows `nextPageToken` (cap 1000 pages). `LeadsByEmail` is removed (no callers left).
- `leadsByEmail` becomes `leadsBy(ctx, cmd, env, client, cfg, by, value, extras)`.
- `--by <field>` is on lead, activity and brief; the default is `email`. `--lead-id N` is on lead.
- The argument is checked as an email only when `--by email`.
- Error 1003 maps to exit 1 with Marketo's message plus `check the field is searchable: mkto fields` (its meaning is ambiguous in the docs).
- `lead` with 2 or more matches: one column per lead, rows only for fields that differ, plus id/updatedAt.
**Tests:** `TestLeadsPaging`, `TestLeadByID`, `TestLeadBySfdcContactID`, `TestLeadLeadIDFlag`, `TestLeadDuplicatesSideBySideGolden`, `TestBy1003Hint`.
**Commit:** `feat(lead): look up by any searchable field; --lead-id; compare duplicates`

#### T28: score/status ledger (pure)
**Files:** create `internal/story/ledger.go` and `ledger_test.go`
**Interfaces:**
- `type Rules struct{ ScoreField string; Threshold int; StatusField string; MQLValues, DQValues []string; Near time.Duration }`
- `type Step struct{ Date time.Time; Kind string /* score|status */; Field string; Old, New string; Delta int; Reason string; CampaignID int; ActivityID int }`
- `type Mark struct{ Step Step; Label string /* crossed 80 | MQL | DQ */; Near []marketo.Activity }`
- `func Build(acts []marketo.Activity, typeName map[int]string, r Rules) (steps []Step, marks []Mark)`
- A crossing is `old < T <= new`, judged on the step's own Old/New values, never a running sum. Score drops below T are marked too.
- Near = other activities within ±Near of the step, excluding Change Score/Data Value rows.
**Tests:** `TestLedgerCrossUp`, `TestLedgerDropThenRecross`, `TestLedgerMissingOldValue` (no crossing claimed), `TestLedgerStatusMQLAndDQ`, `TestLedgerNearWindow`, `TestLedgerOtherScoreFieldIgnored`.
**Commit:** `feat(story): score and status ledger with threshold crossings`

#### T29: `mkto why` (D4)
**Files:** create `cmd/mkto/why.go` and its test; edit `internal/config` (`[why]` section), `output.WhyJSON`, goldens
**Behaviour:**
- `mkto why <email|--by> [--days 90] [--lead-id N]` fetches Change Score, Change Data Value and Change Status in Progression plus the story types for "near" (Visit Webpage and Click Link only with `--web`).
- `score_field` and `status_field` are display labels (as `mkto fields` shows them, e.g. "Behavior Score", "Lead Status"), because Marketo's Change Score / Change Data Value rows name the field by label. The template says so, and `doctor` cross-checks them against describe labels like `change_fields`. `mql_threshold` must be > 0.
- Without `[why]` set it exits 2 and names the keys. The section is owned by Marketing (D4); the template shows a commented example.
- Output:
  - the call card line
  - `rule: Behavior Score ≥ 100 · MQL when Lead Status in [MQL] · DQ in [Disqualified, Recycled]`
  - the ledger table (date, field, old→new, Δ, reason, campaign)
  - marks with `near:` lines
  - the current values
- Wording is "near", never "caused".
- Above 90 days it adds a note: Marketo may purge score and data-value changes after a retention period (UNVERIFIED exact period).
**Tests:** `TestWhyNoConfigExit2`, `TestWhyGolden` (txt + json, threshold crossing plus MQL→DQ), `TestWhyNoChanges`, `TestWhyRetentionNote`.
**Commit:** `feat: mkto why explains score and status changes`

#### T30: program token names and links (D3)
**Files:** `internal/marketo/programs.go` (`ProgramTokens`), `cmd/mkto/clients.go` (`lookupNames` lifted from `activity.go:130-156`), `activity.go`, `internal/config` (`name_tokens`, `link_tokens`), goldens
**Interfaces:**
- `func (c *Client) ProgramTokens(ctx, programID int) (map[string]string, error)` uses `GET /rest/asset/v1/folder/{id}/tokens.json?folderType=Program`, keyed by token name exactly as returned. Lookups match with or without a leading `my.`, case-insensitive.
- Config defaults: `name_tokens = ["my.Name"]`, `link_tokens = ["my.WPlink"]`.
- `func lookupNames[K comparable, V any](ctx, ids []K, cap int, fetch func(K) (V, error)) (map[K]V, failed int, firstErr error)`: shared by programs, tokens and campaigns. Quota and ctx errors are returned.
- The programs section shows `<my.Name> (program name)` and the link on the next line when present. Token lookups count toward the same 25-lookup cap.
- `--json`: `MembershipJSON.Title` and `MembershipJSON.Link`, both omitempty.
**Tests:** `TestProgramTokensDecoded`, `TestProgramsSectionUsesTokenName`, `TestTokenLookupFailsSoft` (603 → program name, one warning), `TestLookupNamesCap`.
**Commit:** `feat(activity): show program asset names and links from my. tokens`

#### T31: smart campaign names
**Files:** `internal/marketo/programs.go` (`SmartCampaign`), `activity.go`, `why.go`, goldens
**Behaviour:** campaignId → name via `lookupNames` (cap 25, per run). The Change Score/Data Value detail gains `via "<campaign>"`, and why's campaign column too. A failure falls back to `campaign <id>` with one warning.
**Tests:** `TestCampaignNameShown`, `TestCampaignLookupCached`, `TestCampaign603FailsSoft`.
**Commit:** `feat: name the smart campaign behind score and status changes`

#### T32: program credit (acquiredBy, reachedSuccess)
**Files:** `internal/marketo/programs.go`, `activity.go`, `output.MembershipJSON`, goldens
**Behaviour:** `Membership.AcquiredBy`, `Membership.Success`, with field names per the checker. The programs section marks `★ acquired` and `✓ success`; `--json` gets `acquiredBy` and `success`.
**Tests:** `TestMembershipCreditDecoded`, `TestProgramsSectionCreditGolden`.
**Commit:** `feat(activity): mark the acquisition program and program success`

#### T33: status history via leadchanges (shape VERIFIED 2026-11-08)
**Files:** `internal/marketo/activities.go` (`LeadChanges(ctx, leadID, fields, since)`), `why.go`
**Behaviour:** `why` uses leadchanges for status_field history when its shape is VERIFIED; otherwise the task is closed as "won't do: Change Data Value covers it" in DECISIONS (no code).
**Tests:** `TestLeadChangesPaging`, `TestWhyUsesLeadChanges`.
**Commit:** `feat(why): status history from lead changes`

### Phase 4D: reach

#### T34: `mkto leads --from FILE|-` 🔒
**Files:** create `cmd/mkto/leads.go` and its test
**Behaviour:**
- Reads emails (one per line, or the first CSV column; header lines without `@` are skipped), trims them, removes duplicates and validates them with `checkEmail`.
- Chunks so each URL stays under 7 KB, at most 300 per call.
- Table: one row per input email with score, status, owner, last updated; `not found` rows are kept.
- `--csv` uses `encoding/csv`, `--json`, and `--out` (the T21 rules).
- No activity calls (keeps cost at about N/300 calls). `--activity` adds last touch via activities `leadIds` ≤ 30 per call.
**Tests:** `TestLeadsFromFileChunks` (enough emails for 2 chunks → Hits == 2), `TestLeadsNotFoundRow`, `TestLeadsCSVGolden`, `TestLeadsStdin`, `TestLeadsActivityBatches30`.
**Commit:** `feat: mkto leads looks up many emails in few calls`

#### T35: `mkto account <sfdcAccountId>` 🔒
**Files:** create `cmd/mkto/account.go` and its test
**Behaviour:**
- Uses `Leads("sfdcAccountId", …)` with paging. The roster (name, title, score, status, owner, last touch) is sorted by score desc.
- Last touch and 30-day counts come from activities with `leadIds` ≤ 30 per call over the story types.
- `--by <field>` allows another searchable account key (e.g. a custom domain field).
- A cap of 200 leads, with a note when hit.
**Tests:** `TestAccountRosterGolden`, `TestAccountActivitiesBatched`, `TestAccountCapNote`, `TestAccountNotFound` (exit 3).
**Commit:** `feat: mkto account shows everyone at an account`

#### T36: `lead --lists`
**Files:** `internal/marketo/leads.go` (`ListMemberships`), `lead.go`
**Behaviour:** Lists the static lists the lead is on (id, name), with paging. The help says smart lists can't be read through the API.
**Tests:** `TestLeadListsGolden`, `TestListMembershipPaging`.
**Commit:** `feat(lead): --lists shows static list membership`

#### T37: brief payload tells the story (D2) 🔒
**Files:** `internal/redact/redact.go`, `redact_test.go`, `cmd/mkto/brief.go`, `docs/LLM_LAYER.md`, goldens
**Behaviour:**
- `redact.Event` gains `Change string 'json:"change,omitempty"'`:
  - Change Score: `+25 (60→85)`, numbers only.
  - Change Data Value: `<status_field>: old→new`, only when the field is the `[why] status_field` or a score field.
  - Change Status in Progression: `→ <new status>`.
- Reason text and other fields are never included.
- Email Delivered is dropped from the payload. `leaves()` covers `Change`, so the guard scans it.
- brief's default fetch types add Change Score, Change Data Value and Change Status in Progression (already in the T16 defaults).
**Tests:** `TestPayloadScoreChangeNumbersOnly`, `TestPayloadDataValueOnlyStatusField`, `TestPayloadDropsEmailDelivered`, `TestGuardScansChange`, `TestShowPayloadGolden`.
**Commit:** `feat(brief): include score and status changes in the redacted payload`

#### T38: `quota --by-user` and recent errors
**Files:** `internal/marketo/usage.go`, `cmd/mkto/quota.go`, goldens
**Behaviour:** `--by-user` adds a per-API-user table for today (from the same usage.json call; no extra quota). `--errors` adds `stats/errors.json` (code, count) for today.
**Tests:** `TestQuotaByUserGolden`, `TestQuotaErrorsGolden`, `TestQuotaByUserNoExtraCall`.
**Commit:** `feat(quota): per-user usage and today's error codes`

#### T39: Snap Store readiness (D1: Erekle publishes)
**Owner:** `snap-engineer`. **Files:** `snap/snapcraft.yaml` (`version: 0.2.0`, `grade: stable`), `.github/workflows/ci.yml` (arm64 build via a matrix or `remote-build` docs), `SNAP_PACKAGING.md` (store steps for Erekle: `snapcraft register mkto`, `upload --release=edge`).
**Rules:** No login, register, upload or release by Claude. Rebuild, then re-test under confinement: `doctor`, `fields`, `types`, `leads --from ~/file`, and `activity --out ~/x.json --force` over an existing file (the `mkto-out-*.tmp` temp file must be allowed by the home plug).
**Commit:** `build(snap): 0.2.0, arm64 build, store publishing guide`

#### T40: Phase 4 gate
- `spec-reviewer` maps every T15–T38 test name to a passing test. `security-auditor` runs a full pass (GET only, PII in new outputs, the redact boundary for T37, file modes for `--out`/`leads`).
- Docs:
  - Fold Phase 4 into `docs/SPEC.md`: commands, flags, config keys, endpoint rows with UNVERIFIED tags, the defaults migration, and `why` meaning "near".
  - Update the README usage and quickstart.
  - Add Part C (UNVERIFIED shapes to confirm) to `docs/REAL_INSTANCE_CHECKLIST.md`.
  - Add the release notes for 0.2.0.
- Ask Erekle for the `change_fields` list (decision pending) and put it in the template.
- If checklist C10 shows query strings in Visit Webpage / Click Link primaries, strip them (scheme + host + path, as `story.bareURL` does for Click Email) in `activity` and `why` output (security audit of T29, Low).

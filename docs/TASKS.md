# Tasks

Work top to bottom. Tick each box when the task is done and tested. Phases 0–1.5 are the snap assignment; Phases 2–3 are optional extras.

## Phase 0 — Setup

- [x] `git init` in snap-mkto (its own repo; remote `canonical/snap-mkto`)
- [x] Create `.gitignore` covering everything in CLAUDE.md rule 4
- [x] Correct the spec against the Marketo docs (see docs/PLAN.md Task 0)
- [x] `go mod init`, cobra skeleton, `mkto version` (commit + version via `-ldflags`)
- [x] `LICENSE` (see docs/REPO.md)
- [x] `Makefile` with `build`, `test`, `lint` (`go vet` + `gofmt -l`), `snap`
- [x] `.github/workflows/ci.yml` (test job only) and `.github/dependabot.yml` (see docs/REPO.md)

## Phase 1 — v1 CLI (see docs/SPEC.md)

- [x] `internal/config`: path resolution, load, env overrides, validation, permission warning
- [x] `mkto init`
- [x] `internal/marketo`: token fetch + cache + refresh, envelope parsing, error mapping (601/602/606/607/615/1006), backoff, timeouts
- [x] `mkto lead`
- [x] Activity types: fetch once, resolve names ↔ ids, warn on unknown names
- [x] `mkto activity`: paging token, `moreResult` loop, batching by 10 type ids, timezone display, `--out`
- [x] Program membership section (name lookups cached and capped)
- [x] `mkto programs`
- [x] `mkto quota`
- [x] `mkto brief` stub (exit 2)
- [x] `--json` for every command, with golden tests
- [x] All tests listed under "Testing" in docs/SPEC.md

## Phase 1.5 — Snap (see SNAP_PACKAGING.md)

- [x] `snap/snapcraft.yaml` (core26, strict, go plugin, plugs `network` + `home`)
- [x] `snapcraft pack` succeeds in LXD
- [x] `sudo snap install --dangerous ./mkto_*.snap`
- [x] Under confinement: `mkto init` writes to `~/snap/mkto/common/config.toml` with 0600
- [x] Under confinement: every command works against a real instance (read-only)
- [x] `--out ~/some-file.json` works (home plug); confirm `/tmp` output is private to the snap and document it
- [x] Check `journalctl` / `snappy-debug` for AppArmor denials; fix or document each one
- [x] Update SNAP_PACKAGING.md with the verified snapcraft.yaml, commands and sample output
- [x] README.md matches reality
- [x] Add the `snap` job to CI; add `source-code` and `issues` to snapcraft.yaml (see docs/REPO.md)
- [ ] Tag `v0.1.0` and create a GitHub release with the snap attached
- [ ] Optional: register the name, upload to `edge`, `snapcraft remote-build` for arm64

**Demo for the manager:** install from the local `.snap`, run `init`, `lead`, `activity`, `programs`, `quota`, then walk through SNAP_PACKAGING.md.

## Phase 2 — Story (see docs/LLM_LAYER.md)

Real lead data only after data-protection sign-off.

- [x] `internal/redact`: allowlist builder, title buckets, URL scrubbing, final guard, type enforcement
- [x] Exhaustive redaction tests
- [x] `mkto brief --show-payload`
- [x] `internal/llm`: provider interface, Anthropic implementation, embedded + overridable prompts
- [x] Narrate + verify steps
- [x] Render (markdown + `--json`), `enabled = false` gate

## Phase 3 — Research and pitch

- [x] Research step with web search; sources required; claims without sources dropped
- [x] `playbook.md` loading; pitch step; skip cleanly when missing
- [x] `--no-research`, `--save`
- [x] Update README, SNAP_PACKAGING.md and docs/LLM_LAYER.md

## Phase 4 — Usability (see docs/USABILITY_REVIEW.md and docs/PLAN.md §Phase 4)

4A free wins
- [x] T15 `--web` on activity; web types off by default
- [x] T16 Story-first default activity types; migrate v0.1.0 lists; release note
- [x] T17 Detail column (`internal/story`), `change_fields` + `--all-changes`
- [x] T18 Decode `campaignId` / `primaryAttributeValueId`
- [x] T19 `profile_fields` (score, status, owner, sync) + `lead --fields`
- [x] T20 Call card header, relative times, duplicate note on stderr, `lead_url`
- [x] T21 Email and `--out` checks before any call; safe `--out` (`--force`, `wrote <path>`)
- [x] T22 Config errors list every missing key; placeholder host; env-var source named
- [x] T23 Typo suggestions, friendly `--days`, brief-off inline steps, network hint

4B discovery (run on the real instance before 4C)
- [x] T24 `mkto types [--search]` + did-you-mean
- [x] T25 `mkto fields [--search]`
- [x] T26 `mkto doctor`

4C scoring and attribution
- [x] T27 `Leads(filterType)` paging, `--by`, `lead --lead-id`, side-by-side duplicates
- [x] T28 `internal/story` ledger (pure)
- [x] T29 `mkto why` with `[why]` config
- [x] T30 Program `my.` token names and links
- [x] T31 Smart campaign names
- [x] T32 `acquiredBy` / `reachedSuccess` markers
- [x] T33 Status history via `leadchanges.json` (built, then removed in T43: same data, twice the time)

4D reach
- [x] T34 `mkto leads --from FILE|-`
- [x] T35 `mkto account <sfdcAccountId>`
- [x] T36 `lead --lists`
- [x] T37 brief payload includes score/status changes
- [x] T38 `quota --by-user`, `--errors`
- [x] T39 Snap 0.2.0, arm64, store guide (Erekle publishes)
- [x] T41 Activity batches of ≤4 types, 3 at a time; 14-day default (real-instance timing)
- [x] T43 Drop the lead-changes walk from why (real instance: 3m22s → ~100 s expected)
- [x] T42 why: cut long reasons, before: lines; owner = sfdcLeadOwnerId; change_fields default
- [x] T40 Phase 4 gate
  - [x] Docs: Phase 4 folded into SPEC.md, README (commands, quickstart, 0.2.0 notes, config), checklist Part C (C11–C17)
  - [x] Review and security audit (spec-reviewer PASS, security-auditor PASS, 2026-11-09)
  - [x] `change_fields` list from Erekle (in the template): Lead Status, Lead Score, Company Name

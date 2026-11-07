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
- [ ] `sudo snap install --dangerous ./mkto_*.snap`
- [ ] Under confinement: `mkto init` writes to `~/snap/mkto/common/config.toml` with 0600
- [ ] Under confinement: every command works against a real instance (read-only)
- [ ] `--out ~/some-file.json` works (home plug); confirm `/tmp` output is private to the snap and document it
- [ ] Check `journalctl` / `snappy-debug` for AppArmor denials; fix or document each one
- [ ] Update SNAP_PACKAGING.md with the verified snapcraft.yaml, commands and sample output
- [ ] README.md matches reality
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

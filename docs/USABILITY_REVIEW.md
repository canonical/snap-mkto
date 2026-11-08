# mkto usability review (2026-11-08)

How it was made: four agents did a first pass in parallel: an SDR critic, a RevOps/MOps critic, a Marketo API check against Adobe's docs, and a hands-on QA run of the HEAD binary against a fake HTTPS Marketo with invented data. A devil's-advocate agent then checked the claims against the code, scored the ideas and cut them to 20, and an architect agent turned the 20 into a plan. Nothing in the repo was changed apart from this file.

Evidence tags: **[QA]** seen when running the binary, **[CODE]** read in the source, **[API✓]** confirmed in Adobe's docs, **[API?]** not confirmed.

## Verdict

mkto is a well-built, safe engagement viewer. It cannot yet answer the questions SDRs and RevOps people actually bring to it:

| Job | Today |
|---|---|
| "Is she hot, what's her score, who owns her?" | ✗ The default `lead_fields` have no score, status, owner or SFDC id |
| "Why did this lead MQL / get DQ'd?" | ✗ Change Score and Change Data Value aren't fetched by default, and even with `--types` the table hides old→new and the reason |
| "Who else at Acme is engaged?" | ✗ No account view; lookup is by email only |
| "My 30 leads this morning" | ✗ One email per command, a new login every run |
| "Install it" | ✗ Needs snapcraft and LXD plus `--dangerous`, and API credentials from an admin |
| "What did she do last month?" | ~ Works, but [QA] 44 of 49 rows were Email Delivered, and web types cost 25–31 s per page |

## The 10 worst findings

1. **The timeline buries the story.** [QA] With the default types, 44 of 49 rows were Email Delivered. The score +25, the Nurture→MQL change, the bounce and the MQL→DQ change were never fetched. [CODE] `internal/config/template.toml:14-16`.
2. **Attributes are hidden in the human output.** [CODE] `cmd/mkto/activity.go:324` prints only date, type and primary value. [QA] A row reads `Change Score  Behavior Score`, while +25, 60→85 and "Filled out form" appear only in `--json`.
3. **`campaignId` is dropped even from `--json`.** [CODE] The `activityPage` struct in `internal/marketo/activities.go` doesn't decode it, so "which smart campaign fired" can't be answered. Whether Marketo sends it at all is [API?].
4. **The lead view has none of the sales fields:** no score, status, owner, SFDC ids, or the unsubscribed/emailInvalid flags.
5. **`lead` has no `--lead-id`** [CODE] `lead.go:74`, even though its own duplicate note tells you to use one. The note prints last, on stdout, after the tables. Lookup is by email only.
6. **Changing the defaults won't reach anyone.** [CODE] `init` writes `activity_types` and `lead_fields` into each user's config, so new defaults only reach people who run `init` again.
7. **Quota is spent before checks run.** [QA] An unwritable `--out` fails only after 7 calls, a malformed email costs 2, and `brief` asks for `numberOfEmployees` and hits a 1006 retry on every run.
8. **There's no way to discover names.** [QA] A typo gives `unknown activity type "Change Scores" skipped` with no suggestion. There's no command to list types or fields, and `mkto leed` gives no "did you mean" (`root.go:19` overrides `Args`).
9. **Error messages point to the wrong place.**
   - `--config` help says "see docs/SPEC.md".
   - brief-off sends you to `docs/LLM_LAYER.md`, which a snap user doesn't have.
   - An env-var secret error says "in your config file".
   - `--days 2w` prints a raw strconv error.
   - A fresh template reports only the first missing key and never flags the placeholder `123-ABC-456`.
10. **Setup is engineer-only:** not in the Snap Store, LXD build, and there's no connection test after `init`.

## The 20 ideas, ranked

I = impact (1–5), E = effort (S/M/L), R = risk, C = confidence the API supports it. Each acceptance test runs against the `internal/marketotest` fake server plus golden files.

| # | Idea | Change | Acceptance test | I / E / R / C |
|---|---|---|---|---|
| 1 | **Detail column** | 4th column per type: Change Score `+25 (60→85) reason`, Change Data Value `field old→new`, progression `→ Attended`, Click Email link, bounce category. IP, user agent and form fields are never read | Golden test shows `+25 (60→85)`; no IP or UA string anywhere in the output | 5/S/low/high |
| 2 | **Story-first defaults** | At most 10 types in one batch: Fill Out Form, Open/Click Email, Email Bounced, Email Bounced Soft, Unsubscribe, Change Score, Change Data Value, Change Status in Progression, New Lead. Drop Email Delivered; add `--web` to `activity`; missing default types give one quiet note | One pagingtoken call; ≤10 ids in `activityTypeIds`; no Email Delivered in the golden | 5/S/low/high |
| 3 | **Score/status/owner fields** | `profile_fields` in the config plus `--fields`. Missing fields are skipped through the existing drop-and-retry; health fields (unsubscribed, emailInvalid, SFDC ids) are suggested in the template | Missing field → one retry, a note naming it, exit 0 | 5/S/low/high |
| 4 | **`mkto why <email>`** | Score ledger, status transitions, the moment the threshold was crossed, and events within ±N h labelled "near", never "caused". Needs a `[why]` config (score_field, mql_threshold, status_field, mql_values, dq_values) and refuses without it | Score 60→85 with threshold 80 → golden `crossed 80 on …, near: Fill Out Form X`; no config → exit 2 | 5/L/med/med |
| 5 | **`mkto fields --search`** | `leads/describe.json` [API✓]: REST name, display name, type | Describe fixture plus `--search score` → 2-row golden | 4/S/low/high |
| 6 | **More ways to pick a lead** | `lead --lead-id`, `--by sfdcLeadId\|sfdcContactId\|<searchable field>` [API✓ any searchable field]. Duplicates shown side by side with only differing fields; error 1003 gives a "not searchable" hint | Two-lead fixture → column golden; 1003 → exit 2 plus hint | 4/S/low/high |
| 7 | **Call card header** | Name · title @ company · score · status · owner · last touch "3d ago" · 7-day counts · timezone label · optional `ui_url` link. Duplicate note goes to stderr, before the table | Golden test shows the header first; duplicate note on stderr | 4/S/low/high |
| 8 | **Smart campaign attribution** | Decode `campaignId` and `primaryAttributeValueId`; look names up via `asset/v1/smartCampaign/{id}.json` (cached, capped at 25). A 603 falls back to "campaign 1234" | `--json` has campaignId; 603 → one warning, exit 0 | 4/M/low/med |
| 9 | **Program credit** | Decode `acquiredBy` and `reachedSuccess`: they're already in the response and the fixture, but `programs.go:36` ignores them. Show "★ acquired" and success | Golden shows the marker; no new API calls | 4/S/low/med |
| 10 | **`mkto doctor`** | Checks config (all missing keys at once, placeholder host), token, types, describe, asset permission, and that the configured fields and types exist. About 4 calls | 603 on assets → `asset ✗` plus a hint about the API role | 4/S/low/high |
| 11 | **`mkto account <sfdcAccountId>`** | Roster of an account's leads with score, status and last touch; activities batched with `leadIds` ≤30 [API✓]. Named Accounts left out (ABM add-on [API?], and Adobe says that API can't link leads [API✓]) | 3 leads → one activities call with `leadIds=a,b,c` → golden | 4/M/med/high |
| 12 | **Fail before spending quota** | Check the email shape and open `--out` before any call; brief remembers fields missing on the instance | Bad `--out` → 0 requests to the fake server | 3/S/low/high |
| 13 | **Error polish** | Restore cobra's suggestions, friendly `--days` error, real config path in help, brief-off steps inline, name the env var when it was the source | `mkto leed` → "Did you mean lead?" | 3/S/low/high |
| 14 | **Snap Store edge plus arm64** | A CI job pushes to edge; `remote-build` for arm64 | `snap install mkto --edge && mkto version` in CI | 5/M/med/high |
| 15 | **`mkto types --search` and did-you-mean** | List types, including their attribute names; suggest the closest name for an unknown one | `--types "Change Scores"` → `did you mean "Change Score"` | 3/S/low/high |
| 16 | **Batch `mkto leads --from file\|-`** | One row per lead, `--csv` via `encoding/csv`. Chunks keep URLs under about 7 KB, because the long-URL fallback is a POST | 2 chunks → `Hits==2`; golden CSV header | 3/M/med/high |
| 17 | **`lead --lists`** | Static lists via `leads/{id}/listMembership.json` [API✓]. Smart lists aren't readable, and the help says so | Golden list table | 3/S/low/high |
| 18 | **Safe `--out`** | No silent overwrite without `--force`; prints `wrote <path>` | Existing file → exit 2, file unchanged | 2/S/low/high |
| 19 | **brief payload tells the story** | Drop Email Delivered; add score delta and new status through the redact allowlist; reason text stays out | Payload golden: no Email Delivered, no free text | 3/S/med/high |
| 20 | **`quota --by-user` and errors** | `stats/usage.json` users[] plus `stats/errors.json` | Two-user golden | 2/S/low/high |

**Killed:**
- Markdown/CSV formats on every command: `--json | jq` already covers it.
- `--copy`: strict confinement blocks the clipboard.
- Instance profiles: `--config` and `MKTO_CONFIG` already do this.
- Team config sharing: env overrides exist, so a docs line is enough.
- On-disk token cache: it breaks "secrets stay in the config file" to save one call.
- Bulk extract: needs POST, which breaks read-only.
- Merged into others: leadchanges (into #4), health (into #3), dupes (into #6), relative times and labels (into #7).

## Architecture (smallest shape that works)

- **A new pure package, `internal/story/`**, using stdlib and marketo types only, so it can be tested without a network.
  - `detail.go`: `map[typeName]func(attrs) string`, the same pattern as `assetTypes` in `internal/redact/redact.go:57`. Only the attributes it names are ever read, so the map is also the PII allowlist.
  - `summary.go`: counts, last touch, relative time.
  - `ledger.go`: `Build(acts, typeNames, Rules) Ledger`. A crossing is a step with `old < T <= new`, taken from each step's own old/new values rather than a running sum, because the window can start mid-history.
- **One view model for both outputs.**
  - `output.EventJSON` (`internal/output/output.go:66`) gains `Detail`, `CampaignID`, `Campaign` and `PrimaryID`, all omitempty. The text table renders from the same slice, so human output and `--json` can't drift, and existing goldens only grow.
- **Client (GET only).**
  - `LeadsByEmail` becomes `Leads(filterType, values, fields)` with paging, and the old name stays as a wrapper.
  - New: `DescribeLeads`, `SmartCampaign`, `ListMemberships`, `UsageByUser`/`Errors`, and later `LeadChanges` once its shape is confirmed.
  - `Activity` decodes `campaignId` and `primaryAttributeValueId`; `Membership` decodes `acquiredBy` and `reachedSuccess`.
- **Reuse instead of new code.**
  - Lift brief's web-type filter (`brief.go:112-124`) into `dropWebTypes` for activity.
  - Lift the capped program-name lookup (`activity.go:130-156`) into `lookupNames`, reused for smart campaigns.
  - `pickLead` stays the single duplicate policy.
- **Default migration.**
  - Move the defaults into Go vars and comment the keys out in `template.toml`.
  - A config list that exactly equals the v0.1.0 default is treated as unset, with a `--verbose` note. Hand-edited lists are kept, and `doctor` flags them.

```toml
[marketo]
# profile_fields = ["leadScore","leadStatus","unsubscribed","emailInvalid",
#                   "sfdcLeadId","sfdcContactId","sfdcAccountId"]   # missing ones are skipped
[why]
score_field   = "leadScore"
mql_threshold = 100
status_field  = "leadStatus"
mql_values    = ["MQL"]
dq_values     = ["Disqualified", "Recycled"]
near_hours    = 2
[output]
# ui_url = "https://app-xx00.marketo.com"
```

## Plan (one commit per line, TDD against the fake server)

**Phase A: free wins, no new endpoints (≈1–2 days).**

1. `--web` on activity; web types off by default (#2)
2. Story defaults plus migration of v0.1.0 lists (#2)
3. `internal/story` detail column (#1). Fixture `activities_story_page.json` [UNVERIFIED attribute names]
4. Decode `campaignId` and `primaryAttributeValueId` (#8a)
5. Call card; duplicate note to stderr at the top (#7)
6. Fail-fast checks plus safe `--out` (#12, #18)
7. Config errors: all missing keys, placeholder host, real path in help (#13a)
8. Error polish: suggestions, `--days`, brief-off steps (#13b)

**Phase B: discovery.** Run these on the real instance to confirm the UNVERIFIED shapes before Phase C.

9. `mkto types --search` plus did-you-mean (#15)
10. `mkto fields --search` (#5)
11. `mkto doctor` (#10)

**Phase C: scoring.**

12. `profile_fields` plus `--fields` (#3)
13. `Leads(filterType)`, `--by`, `--lead-id`, side-by-side duplicates (#6)
14. `story/ledger.go` pure tests: crossing up, drop then recross, missing old value, DQ (#4a)
15. `mkto why` (#4b)
16. Smart campaign names (#8b)
17. Status history via `leadchanges.json`, only if the real shape is confirmed (#4c)
18. `acquiredBy` and `reachedSuccess` (#9)

**Phase D: reach.**

19. `mkto leads --from` (#16)
20. `mkto account` (#11)
21. `lead --lists` (#17)
22. brief payload story types (#19), after sign-off
23. `quota --by-user` (#20)
24. Snap Store edge plus arm64 CI (#14): a parallel track once D1 is decided

**Order:**
- 1 before 2.
- 3 and 4 before 14–16 and 22.
- 9 and 10 before 11 and 13.
- 12 before 15, 19 and 20.
- 13 before 19 and 20.

**Docs to update with each step:**
- `docs/SPEC.md`: commands, endpoint rows with UNVERIFIED tags, the defaults/migration rule, and `why` meaning "correlation only".
- `docs/TASKS.md`: a new Phase 4.
- `docs/LLM_LAYER.md`: the allowlist for #19.
- `SNAP_PACKAGING.md`: store, arm64, and `--from` needing the home plug.
- `README.md`: a quickstart with `doctor` right after `init`.

> **Superseded in places by `docs/SPEC.md` (0.2.0):** `change_fields` empty shows every field (Erekle will supply the list); error 1003 exits 1 with Marketo's message plus a hint, since the docs disagree on its meaning.

## Decisions (Erekle, 2026-11-08)

- **D1 Snap Store:** the name `mkto` is free. Erekle publishes once everything is done, so #14 stays last. Optional: `snapcraft register mkto` reserves the name without publishing anything.
- **D2 brief payload (#19):** approved. Scores and statuses aren't sensitive. Only the score delta, the new score and the new status value go into the payload; other Change Data Value fields stay out.
- **D3 asset permission (#8):** the API user probably has it already. Smart campaign names don't help much, though. The readable names live in **program tokens**: gated assets (whitepapers) use `{{my.Name}}` and `{{my.WPlink}}`, and webinars and events mostly define `{{my.Name}}`. So the new #8b resolves the program's name and link tokens instead of, or before, smart campaign names, through the tokens-by-folder endpoint (`GET /rest/asset/v1/folder/{id}/tokens.json?folderType=Program`, **verify before coding**). It is cached per run and capped at 25 like the program names. Token names go in config (`name_tokens = ["my.Name"]`, `link_tokens = ["my.WPlink"]`), since they're an instance convention.
- **D4 MQL definition (#4):** Marketing owns it, so Marketing Ops sets `[why]` in the config. `why` prints the rule it used (`rule: leadScore ≥ 100, status in [MQL]`), so a wrong config shows up in the output instead of hiding.
- **D5 bulk PII (#11, #16):** no new sign-off. The data never leaves the machine: `account` and `leads` call only Marketo and print locally, like `lead` today. `--out` files are 0600. Only `brief` talks to an LLM, through `internal/redact`. Bulk output must never feed `brief`.
- **D6 changed defaults:** approved: runtime migration plus a release note.
- **Change Data Value filter:** filtered for **noise**, not for PII. CRM sync and other automations write Change Data Value rows for many fields, which would bury the story again. By default the human output shows only changes to `status_field`, the score fields and `profile_fields`; `--all-changes` shows every field. `--json` always has everything.

## Not verified

- Attribute names for Change Score, Change Data Value, progression and bounce.
- Whether `campaignId` is present on activities.
- The `leadchanges.json` shape, the error code 1003, and the smartCampaign path.
- Retention: Marketo is believed to purge Change Score and Change Data Value after about 90 days by default. `why` should warn on longer windows.

None of these need to be guessed: Phase B commands (`mkto types`, `mkto fields`) show the real shapes on the instance first.

# mkto specification (v1 and 0.2.0)

## Scope

Thirteen commands, all read-only:

| Group | Commands |
|---|---|
| Setup | `init`, `doctor`, `version` |
| One lead | `lead`, `activity`, `why`, `brief` |
| Many leads | `leads`, `account` |
| Instance | `programs`, `types`, `fields`, `quota` |

v1 (0.1.0) shipped `init`, `lead`, `activity`, `programs`, `quota`, `version` and, in Phases 2–3, `brief` (off by default, see `docs/LLM_LAYER.md`). Phase 4 (0.2.0) added `doctor`, `why`, `leads`, `account`, `types` and `fields`, plus new flags on the v1 commands. `docs/PLAN.md` §Phase 4 holds the task-by-task detail; this file is the spec. [Deviations from the plan](#deviations-from-the-plan) are listed at the end.

## Configuration

### Location (resolution order)

1. `--config <path>` flag
2. `$MKTO_CONFIG`
3. Running as a snap (`$SNAP_USER_COMMON` is set): `$SNAP_USER_COMMON/config.toml` → `~/snap/mkto/common/config.toml`
4. Otherwise: `os.UserConfigDir()/mkto/config.toml` → usually `~/.config/mkto/config.toml`

`--help` shows the path that applies, e.g. `--config string   config file path (default: /home/jane/snap/mkto/common/config.toml)`.

Why `$SNAP_USER_COMMON` and not `$HOME/.config`: under strict confinement `HOME` is `$SNAP_USER_DATA` (`~/snap/mkto/<revision>`), which snapd copies on every refresh. Keeping credentials in `common` means a single copy that survives refreshes and reverts.

`playbook.md`, `prompts/` and `briefs/` (Phase 2–3) live in the same directory as `config.toml`.

### Permissions

- `mkto init` creates the directory with mode `0700` and the config template with mode `0600`. It refuses to overwrite an existing file unless `--force` is given.
- On every load: if the file is group- or world-readable, print a warning to stderr with the exact `chmod 600 <path>` command. Don't fail. `doctor` reports the same as a `!` line.

### Format

`mkto init` writes `internal/config/template.toml`. The keys, with the defaults that apply when a key is left out:

```toml
[marketo]
base_url = "https://123-ABC-456.mktorest.com"   # the placeholder is rejected; see Validation
client_id = ""
client_secret = ""
daily_quota = 50000
# Commented out in the template, so the defaults below apply and follow mkto updates:
# lead_fields = ["id", "email", "firstName", "lastName", "title", "company",
#                "industry", "country", "leadSource", "createdAt", "updatedAt"]
# profile_fields = ["leadScore", "leadStatus", "sfdcLeadOwnerId", "unsubscribed",
#                   "emailInvalid", "sfdcLeadId", "sfdcContactId", "sfdcAccountId"]
# activity_types = ["Fill Out Form", "Open Email", "Click Email", "Email Bounced",
#                   "Email Bounced Soft", "Unsubscribe Email", "Change Score",
#                   "Change Data Value", "Change Status in Progression", "New Lead"]
# change_fields = ["Lead Status", "Lead Score", "Company Name"]
# name_tokens = ["my.Name"]
# link_tokens = ["my.WPlink"]

[output]
timezone = "Local"   # or an IANA name, e.g. "Asia/Tbilisi"
# lead_url = "https://<your Marketo UI host>/...{id}..."

[llm]                # mkto brief: off by default. Read docs/LLM_LAYER.md (sign-off) before enabling.
enabled = false
provider = "anthropic"
api_key = ""
model = "claude-sonnet-5-5"

# [why]              # mkto why; Marketing owns these values. Commented out in the template.
# score_field = "Behavior Score"
# mql_threshold = 100
# status_field = "Lead Status"
# mql_values = ["MQL"]
# dq_values = ["Disqualified", "Recycled"]
# near_hours = 2
```

| Key | Default | Names are | Notes |
|---|---|---|---|
| `marketo.base_url` | none (required) | — | Normalised to `https://<host>`: a trailing `/`, `/rest` or `/rest/v1` is dropped. |
| `marketo.client_id`, `client_secret` | none (required) | — | |
| `marketo.daily_quota` | `50000` | — | The API doesn't report the limit. Must be a whole number. |
| `marketo.lead_fields` | the list above | REST names | What `lead` shows. Absent or `[]` gives the defaults. A missing field is an error (1006). |
| `marketo.profile_fields` | the list above | REST names | Shown by `lead` after `lead_fields` (empty values skipped), on the `activity`/`why` call card, and in `leads`/`account`. Absent gives the defaults; `[]` turns them off. A field the instance lacks is dropped and the lookup retried once per field (`--verbose` notes it). The owner shown is the first non-empty of `leadOwnerEmailAddress` (add it to the list on instances that have it) and `sfdcLeadOwnerId` (a Salesforce user id). |
| `marketo.activity_types` | the list above | activity type names | What `activity`, `why`, `brief`, `account` and `leads --activity` fetch. 10 defaults, so 3 activity batches of at most 4 types. See [the v0.1.0 migration](#the-v010-migration). |
| `marketo.change_fields` | the list above | field labels | Which Change Data Value rows the `activity` text timeline shows. Absent gives the defaults; `[]` shows every row, as do `--all-changes` and `--json`. |
| `marketo.name_tokens`, `link_tokens` | `["my.Name"]`, `["my.WPlink"]` | token names | Program tokens shown as a program's title and link in `activity`. Matched case-insensitively, with or without a leading `my.`; first match wins. `[]` turns them off and saves one call per program. |
| `output.timezone` | `Local` | — | Any IANA name. |
| `output.lead_url` | none | — | A lead's Marketo record URL with `{id}` where the lead id goes; the card shows `open: <url>`. Without `{id}` it is ignored (`--verbose`: `note: output.lead_url has no {id}; not showing a record link`). The UI path isn't verified, so mkto never builds it. |
| `why.score_field`, `status_field` | none | field labels | Required by `why`. |
| `why.mql_threshold` | none | — | Required by `why`; a whole number above 0. |
| `why.mql_values` | none | values | Required by `why`. |
| `why.dq_values` | none | values | Optional. |
| `why.near_hours` | `2` | — | `0` or unset means 2; negative is an error. |
| `llm.*` | see above | — | `mkto brief` only; see `docs/LLM_LAYER.md`. |

**Labels vs REST names.** `lead_fields`, `profile_fields`, `lead --fields` and `--by` take REST names (the NAME column of `mkto fields`, e.g. `leadStatus`). `change_fields`, `why.score_field` and `why.status_field` take field labels (the LABEL column, e.g. `Lead Status`), because Change Score and Change Data Value activities name the field by label (UNVERIFIED on a real instance, checklist C16). `doctor` checks each list against the matching column. `why` looks up the REST name behind `status_field` itself (see `mkto why` under Commands).

### The v0.1.0 migration

0.2.0 changed the default activity types (D6). A config whose `activity_types` still equals one of the two v0.1.0 template lists (with or without "Interesting Moment"), compared in order, case-insensitively and ignoring surrounding spaces, is treated as unedited: mkto uses the 0.2.0 defaults instead. `--verbose` says so once per run: `note: activity_types is the unedited v0.1.0 list; using the 0.2.0 default activity types instead (delete activity_types from <path> to drop this note)`, and `doctor` gives the same advice as a `!` line. Any other list is the user's own and is used exactly as written.

Unknown type names warn only in a list the user set (`warning: unknown activity type "<name>" skipped; did you mean "<closest>"? …`; the suggestion appears when the closest name is within edit distance 3 or a substring match of at least 4 letters). A defaulted list, whose names are partly UNVERIFIED, gives one `--verbose` note instead: `note: default activity types not on this instance: <names>`. `--types` always warns.

### Environment overrides

`MKTO_BASE_URL`, `MKTO_CLIENT_ID`, `MKTO_CLIENT_SECRET` and `MKTO_LLM_API_KEY` (for `llm.api_key`) override the file (useful in CI). `MKTO_CONFIG` picks the file. mkto remembers which keys came from the environment, so errors can say where to look (see [Error handling additions](#error-handling-additions-020-phase-4)).

### Validation

On load, every problem with `base_url`, `client_id` and `client_secret` is reported together, one line each (`errors.Join`), naming the key, the config path and the env var:

```
config key marketo.base_url in <path> is still the template placeholder: set your REST endpoint from Admin > Web Services
config key marketo.client_secret is not set: edit <path> (or set the MKTO_CLIENT_SECRET environment variable)
```

- `base_url` must be `https://` with a host and at most `/rest` or `/rest/v1` after it; `123-ABC-456` (the template's host) is rejected.
- `daily_quota = "50k"` gives `config key marketo.daily_quota in <path> must be a whole number, e.g. 50000`, not go-toml's text.
- A TOML error on the `client_secret` or `api_key` line gives `config key marketo.client_secret in <path> is not a valid TOML string (put the value in double quotes)`, because go-toml's own message quotes the offending characters and would print part of the secret.
- An unknown `timezone` is checked after the keys above: `unknown timezone "Mars/Base": use "Local" or an IANA name such as "Asia/Tbilisi"`.
- `[why]` is checked by `mkto why` only, before any API call (exit 2).

## Marketo API usage

Verify every endpoint against https://developer.adobe.com/marketo-apis/ before implementing it. Tags below: **VERIFIED** = confirmed in Adobe's docs (marketo-api-checker, 2026-11-07/08); **CONFIRMED** = also seen on a real instance; **UNVERIFIED** = written from the docs pattern or from memory and listed on `docs/REAL_INSTANCE_CHECKLIST.md` Part C. Fixture-level detail is in `testdata/marketo/README.md`.

mkto sends only `GET` requests, plus the identity call. There is no `_method=GET` POST override: batch sizes keep every URL under 7000 bytes instead (Marketo answers 414 above 8 KB, VERIFIED).

### Authentication

- `GET {base}/identity/oauth/token?grant_type=client_credentials&client_id=…&client_secret=…`
- Response has `access_token` and `expires_in` (seconds). Cache the token in memory for the process and refresh 60 s before expiry.
- Send the token as an `Authorization: Bearer <token>` header. **Never** as a query parameter.
- This URL contains the secret: never log it, even with `--verbose`. Redirects are never followed.

### Response envelope

Marketo usually returns HTTP 200 even on failure. Always check `success`; on `false`, read `errors[].code` and `errors[].message`.

| Code | Meaning | Handling |
|---|---|---|
| 601 / 602 | Token invalid / expired | Refresh the token once, retry the request once. |
| 603 | Access denied (VERIFIED) | Name, token and campaign lookups degrade with one warning per kind: `… (check the API user's asset permissions)`. `doctor` prints `✗ assets: access denied. Ask Marketing Ops to give the API role Read-Only Assets …`. Elsewhere: show code and message, exit 1. HTTP 401/403 on asset calls is treated the same. |
| 606 | Rate limit (100 calls / 20 s per instance) | Back off (start 5 s, exponential, max 3 retries), then fail with a clear message. |
| 615 | Concurrency limit (10 concurrent calls) | Same backoff as 606. mkto runs at most 3 calls at once (activity batches only). |
| 713 | Transient error | Same backoff as 606 (Adobe: "wait … then retry"). |
| HTTP 502 | Bad gateway | Same backoff as 606 (Adobe: "retried with exponential backoff"). |
| 607 | Daily quota reached | Stop immediately, from any command or lookup. Tell the user, suggest `mkto quota`. Exit code 4. Quota resets at 12:00 AM US Central. |
| 1003 | Ambiguous in the docs: "Too many results match the filter" (leads page) vs "Invalid data" (error table) | Never hard-code a meaning. On a lead lookup, show Marketo's message and add `; check that <field> is a searchable field (names: mkto fields)`. Exit 1. |
| 1006 | Field not found | Can arrive response-level (`errors[]`) or record-level (`status: "skipped"`, `reasons[]`). Handle both: name the field. If it is in `lead_fields`, say to fix `lead_fields` in the config. With `lead --fields`, say `check --fields`. A field mkto added itself (`profile_fields` and the [optional fields](#optional-fields)) is dropped and the lookup retried; if the field is `id`, `email`, `firstName` or `lastName` (or the name can't be matched), say mkto needs `id`, `email`, `firstName` and `lastName` (the lookup key and the redaction guard) and this instance may not have that field. |
| other | — | Show code and message, exit 1. |

Also handle non-200 HTTP statuses, timeouts (30 s per request; 120 s for `activities.json`, see Endpoints), and non-JSON bodies (`unexpected non-JSON response from Marketo (HTTP <status>)`, never dump the body). Backoff sleeps stop at once when the context is cancelled (Ctrl-C).

Sources checked 2026-11-07: Adobe Experience League `marketo-developer/marketo/rest/{authentication,rest-api,error-codes,assets/programs}`; 2026-11-08: `lead-database/{leads,activities,usage}`, `assets/{smart-campaigns,tokens}`. Query-param `access_token` support was removed on 2026-08-31.

### Endpoints

| Purpose | Request | Status | Notes |
|---|---|---|---|
| Leads by field | `GET /rest/v1/leads.json?filterType={field}&filterValues={v1,v2,…}&fields={csv}[&nextPageToken={t}]` | VERIFIED shape; paging for filterType queries UNVERIFIED | `filterType` is `email` by default, or the `--by` field. URL-encode the values (`+` addresses!). Can return 0, 1 or several leads (duplicates); an email with no lead is simply absent. Up to 300 values per call (`leads`). mkto follows `nextPageToken` while `moreResult` is true and the page isn't empty, and stops with an error if the token repeats or after 1000 pages. `LeadsUpTo` adds a page cap: `account` fetches at most 10 pages (3000 leads) and errors above that. |
| Lead fields | `GET /rest/v1/leads/describe.json` | VERIFIED | Not paged. Decodes `displayName` (label), `dataType`, `length`, `rest.name` and `rest.readOnly`. A field with no `rest` child can't be used over REST and is hidden. `describe2` is UNVERIFIED and unused, so there is no `--searchable`. |
| Static lists | `GET /rest/v1/leads/{id}/listMembership.json[?nextPageToken={t}]` | VERIFIED fields; `createdAt` format UNVERIFIED | Decodes `listId` and `createdAt` (parsed like asset dates; unparsable shows `-`). Paged like leads. The endpoint has no list names, so mkto shows ids. Smart lists can't be read through the API. |
| Activity types | `GET /rest/v1/activities/types.json` | VERIFIED shape; ids and names other than id 2 UNVERIFIED | Fetch once per run; map name ↔ id, case-insensitive. Decodes `description`, `primaryAttribute.name` and `attributes[].name` for `types`. |
| Paging token | `GET /rest/v1/activities/pagingtoken.json?sinceDatetime={RFC3339}` | CONFIRMED 2026-11-07 | Start of the requested window; one per batch. |
| Activities | `GET /rest/v1/activities.json?nextPageToken={t}&activityTypeIds={ids}&leadIds={ids}` | VERIFIED envelope and top-level fields; `campaignId` and most attribute names UNVERIFIED | Marketo takes **at most 10 activity type ids and 30 lead ids per call** (both VERIFIED); mkto sends **at most 4 type ids** and 30 lead ids. Batches (leads × types) run **at most 3 at a time**, each with its own paging token, pages and guards below; the first error stops the other batches and is returned (a 607 wins over the cancellations it causes). Then merge all batches, drop repeated ids, and sort by `activityDate`, then id. Loop while `moreResult` is true; a page can be empty while `moreResult` is still true. Stop with an error if the same `nextPageToken` repeats or after 1000 pages. Up to 300 results per page. Decodes `id`, `leadId`, `activityDate`, `activityTypeId`, `primaryAttributeValue`, `primaryAttributeValueId`, `campaignId` (0 when absent) and `attributes`. **120 s per-request limit** (every other call keeps 30 s): Marketo scans instance-wide web traffic for Visit Webpage and Click Link, and on a real instance (2026-11-07) a 7-day page took up to 29 s while a 30-day page passed 30 s. A page slower than 5 s prints `scanning activity: page <n> took <s>s (Marketo scans every activity of these types instance-wide; web visits and field changes are the slow ones)` to stderr, with or without `--verbose`. Measured on a real instance (2026-11-09): the page count depends only on the window, about 0.4 pages a day (12 pages for 30 days, for every type group); the page time depends on the types per call, about 2.8 s for 4 types or fewer and about 29 s for the 10 defaults (no web types). Hence 4 types per call, 3 batches at a time: a 14-day lookup takes about 17 s and 21 activity calls, a 30-day one about 35 s and 39 calls. |
| Program membership | `GET /rest/v1/leads/{id}/programMembership.json` | VERIFIED fields, incl. `acquiredBy` and `reachedSuccess`; dates CONFIRMED 2026-11-08; paging UNVERIFIED | Program id is `id`. The membership date is `membershipDate`, falling back to `updatedAt` (parsed like asset-API dates). `nextPageToken` is followed only if sent with `moreResult`, and never repeated. |
| Program by id | `GET /rest/asset/v1/program/{id}.json` | VERIFIED; CONFIRMED 2026-11-07 | Resolve names of the shown memberships only; only the name is decoded, so a date-shape change can't break it. Cached per run; capped at 25 programs and says so if capped. Failed lookups give one warning per run: `warning: could not look up <n> of <m> program names (<reason>)`, where the reason is `check the API user's asset permissions` for 603 or HTTP 401/403, `unexpected response format from Marketo; please report it` for a decode error, else the error text. Quota exhaustion and Ctrl-C still stop the command. |
| Program tokens | `GET /rest/asset/v1/folder/{programId}/tokens.json?folderType=Program` | VERIFIED path, envelope and `name`/`value`; `folderType=Program` and the `my.` prefix UNVERIFIED | Only for programs whose name lookup succeeded, within the same 25 programs. Uses `value`, never `computedUrl` (a link into Marketo's UI, not the asset). Names are matched with or without `my.`, case-insensitively. Failures warn once: `warning: could not read program tokens for <n> of <m> programs (<reason>)`, and the program keeps its name. |
| Smart campaign by id | `GET /rest/asset/v1/smartCampaign/{id}.json` | VERIFIED (docs example) | For the `campaignId` of Change Score, Change Data Value and Change Status in Progression rows. Only `name` is decoded. Cached, at most 25 per run (`warning: showing names for 25 of <n> campaigns; the rest show their id`). Failures warn once (`could not look up <n> of <m> campaign names (<reason>)`) and the row shows `campaign <id>`. |
| Programs | `GET /rest/asset/v1/programs.json?maxReturn=200&offset={n}` | VERIFIED; empty last page UNVERIFIED | Page by offset until an empty result (don't rely on warning code 702). There is no multi-id lookup, so the 25-lookup cap above stays. `doctor` sends `maxReturn=1` to test asset access. |
| Usage today | `GET /rest/v1/stats/usage.json` | VERIFIED, incl. `users[{userId,count}]` | Sum `total` (across rows) for today, and collect the per-user split from the same call. "Today" is the `America/Chicago` calendar date, because the quota resets at 12:00 AM Central. |
| Usage, 7 days | `GET /rest/v1/stats/usage/last7days.json` | VERIFIED (sample inferred) | |
| Errors today | `GET /rest/v1/stats/errors.json` | VERIFIED `errors[{errorCode,count}]` | `quota --errors` only. Rows dated today (Chicago). |

Asset-API dates (`createdAt`, `updatedAt`) are **not** RFC 3339: a real instance (2026-11-07) sent `2026-11-01T01:08:44Z+0000`, a literal `Z` followed by an offset. mkto parses RFC 3339, `…Z+0000` / `…Z-0700` and plain `…+0000` with one helper.

<a id="optional-fields"></a>Fields mkto adds itself (beyond `lead_fields`) can be missing on an instance: on 1006 for one of them, mkto drops it and retries the lead lookup (once per such field), except `id`, `email`, `firstName` and `lastName`, which it needs (the lookup key and brief's redaction guard); `--verbose` prints `note: field "<f>" isn't on this Marketo instance; skipped`. A real instance (2026-11-07) lacked `numberOfEmployees`. 1006 for a field in `lead_fields` still errors. The added fields are `profile_fields` (`lead`, `activity`, `why`, `leads`, `account`), `updatedAt` and the card's `firstName`, `lastName`, `title`, `company`; `brief` adds its own fixed list and never `profile_fields`.

Activity records have `activityDate`, `activityTypeId`, `primaryAttributeValue` and an `attributes` list whose names vary by type (e.g. a webpage URL, a link, query parameters, client IP, user agent). The names mkto reads (`Change Value`, `Old Value`, `New Value`, `Reason`, `New Status`, `Success`, `Link`, `Category`, `Details`) are UNVERIFIED and live in one table in `internal/story/detail.go` (checklist C2–C4, C8).

Data retention: Marketo purges some high-volume activity types after a limited period, and is believed to purge Change Score and Change Data Value after about 90 days (exact period UNVERIFIED). An empty result for an older window isn't necessarily a bug; `activity --help` says so, and `why` notes it above 90 days.

## Commands

Global flags: `--config <path>`, `--json`, `--verbose` (method, URL path without query string, status, timings, retry notices — never secrets, tokens or lead emails).

Lead lookup arguments are trimmed of surrounding whitespace. With the default `--by email` the argument must look like an email before any API call (one `@`, a non-empty local part, a domain with a dot, no spaces, control characters or commas, at most 254 bytes), else exit 2 with `"<x>" doesn't look like an email address`. With another `--by`, the value must be one value (no comma): `--by <field> needs one value, e.g. mkto lead 1001 --by id`.

Exit codes: `0` ok, `1` error, `2` usage error / not implemented, `3` lead not found, `4` daily quota exhausted. Flag errors are usage errors; `--days 2w` gives `--days takes a whole number of days, e.g. --days 30` and `--lead-id x` gives `--lead-id takes a lead id number, e.g. --lead-id 1001`. An unknown command gets cobra's `Did you mean this?` list.

`--by <field>` (on `lead`, `activity`, `why`, `brief`; `account` has its own default) looks leads up by any searchable REST field name, e.g. `id`, `sfdcContactId`. `--lead-id N` picks one lead when several match; if none of the matches has that id, exit 3 with `lead <N> not found for <lookup>`. Without `--lead-id`, `activity`, `why` and `brief` pick the most recently updated lead and say which others exist.

### `mkto init [--force]`
Writes the commented template to the resolved config path (0700 dir, 0600 file) and prints the path plus next steps. `activity_types`, `lead_fields`, `profile_fields`, `change_fields`, `name_tokens`, `link_tokens`, `lead_url` and `[why]` are commented out, so their defaults apply. An existing file without `--force` is an error (exit 1).

### `mkto doctor`
Checks the setup in about four API calls and prints one line per check: `✓` ok, `!` warning, `✗` failure, then the check name and a detail with the key or command to fix. In order:

1. `config`: the file loads (a joined config error prints its extra lines indented). On failure doctor stops here.
2. `config permissions` (`!`, only if group- or world-readable).
3. `credentials`: a token is fetched (by the next call). On failure doctor stops.
4. `activity types`: `activities/types.json`.
5. `lead fields`: `leads/describe.json`.
6. `assets`: `asset/v1/programs.json?maxReturn=1`; 603 or HTTP 401/403 gives the Read-Only Assets hint.
7. Names, when 4 and 5 passed: `lead_fields` (`✗`) and `profile_fields` (`!`) against describe REST names (`id`, `email`, `firstName`, `lastName`, `createdAt`, `updatedAt` always count as present); `change_fields` and `[why]` `score_field`/`status_field` against describe labels (`!`); `activity_types` against types (`!`, or `✗` when none resolves), with did-you-mean for a user's list and the migration advice for a v0.1.0 list. All clear gives `✓ names: every configured field and activity type is on this instance`.

Exit 1 when any check fails, with `<n> check(s) failed` on stderr; exit 4 if the quota runs out mid-way. `--json`: `{"checks":[{"name","status":"ok|warn|fail","detail"}]}`. `name_tokens` and `link_tokens` aren't checked (tokens are per program).

### `mkto lead <email|value> [--by F] [--lead-id N] [--fields a,b] [--lists]`
- One lead: `Lead <id>`, then a two-column table of `lead_fields`, then the `profile_fields` that have a value.
- Several leads and no `--lead-id`: `note: <n> leads share this <by> (ids: …); use --lead-id to pick one` on stderr first, then one column per lead: `id`, `updatedAt`, then only the fields whose values differ, and `<n> fields are the same in every lead (not shown)`.
- `--fields a,b` replaces `lead_fields` and `profile_fields` for this run. They count as the user's own, so a missing one is an error: `field "<f>" not found on this Marketo instance: check --fields`. An empty list is exit 2.
- `--lists` adds `Static lists (<n>)` with LIST ID and ADDED, newest first (undated last), or `Static lists: none`. It needs one lead: with several, exit 2 asking for `--lead-id`. The help says smart lists can't be read.
- `--json`: an array of `{"id","fields"}` for every matching lead (profile fields only when non-null); with `--lists` the lead also has `"lists":[{"id","added"}]` (`[]` when none).

### `mkto activity <email|value> [--days 14] [--types "Open Email,Click Email"] [--all-types] [--web] [--all-changes] [--all-programs] [--by F] [--lead-id N] [--out FILE] [--force]`
- Resolves the lead. With duplicates, pick the most recent `updatedAt`, and print `note: <n> leads share this <by>; showing <id> (most recently updated). Others: …. Use --lead-id to pick one.` to stderr before any output (in `--json` it goes in `notes`).
- Default window 14 days; `--days` must be ≥ 1 (else exit 2). Above 90, print a quota warning.
- Default types come from `activity_types`. Visit Webpage and Click Link are skipped unless `--web` is given, with one stderr note: `note: skipping web visits and link clicks (slow on large Marketo instances); add --web to include them`. `--web` also adds them when the list lacks them (the defaults have neither). If only web types are configured, the run stops: `no activity types left to fetch: activity_types only has web types; add --web to include them`. `--types` overrides the list and ignores the web filter; `--all-types` uses every type (warn about quota). `--types` and `--all-types` together → exit 2.
- Prints the call card, the timeline (date, type, primary value, detail), then the Programs section.
- **Call card**, each line left out when it has nothing to show:
  1. `Jane Doe · Platform Engineer @ Example Corp · lead 1001`
  2. `score 85 · MQL · owner a.rep@example.com` (from `leadScore`, `leadStatus`, and the owner: the first non-empty of `leadOwnerEmailAddress` and `sfdcLeadOwnerId`; names UNVERIFIED, set in `cardFields` and `owner`)
  3. `last touch 9d ago · 14d: 3 opens, 1 click, 1 form, +2 more · times in Asia/Tbilisi`. A touch is any activity except Email Delivered. Relative times are `just now`, `5m ago`, `3h ago`, `2d ago`, or the date from 30 days. At most 5 counts, largest first.
  4. `open: <lead_url with {id} replaced>`, when `lead_url` contains `{id}`.
- **Detail column** (`internal/story`), by type: Change Score `+25 (60→85) Filled out form`; Change Data Value `Nurture→MQL` (the field label is the primary value); Change Status in Progression `→ Attended`, plus ` ✓ success` when Success is true; Click Email the link without query string or fragment; Email Bounced and Email Bounced Soft `<category>: <details>`; other types nothing. Score, data-value and progression rows with a `campaignId` end with `via "<campaign name>"`, or `via campaign <id>` when the name wasn't read. Detailers read only named attributes: Client IP Address, User Agent, Form Fields, Query Parameters and Referrer URL are never read. A Change Data Value side that looks like a token (40+ characters, no spaces, letters and digits, not a URL: e.g. a Salesforce session id) shows as `‹N-char token›`, and any other side longer than 80 characters is cut with `…`; `--json` keeps the full values in `attributes`.
- `change_fields` (by default Lead Status, Lead Score and Company Name; `[]` shows all) hides other Change Data Value rows from the text timeline and adds `Change Data Value rows hidden by change_fields: <n> (use --all-changes)` at the end. `--all-changes` shows them all. If every row is hidden: `Nothing to show: every activity in the last <n> days is a field change hidden by change_fields.`
- **Programs** from program membership: only memberships dated within the window, newest first, then `+ <n> older programs (use --all-programs)` when some are hidden. `--all-programs` shows every membership (newest first where dated). Undated memberships are always shown. Each row is `<program name>  <status>`, or `<my.Name> (<program name>)` when a name token has a value, with ` ★ acquired` (`acquiredBy`) and ` ✓ success` (`reachedSuccess`) after the status, and the link token's value on the next line. Program names and tokens are capped at 25 programs (`showing names for 25 of <n> programs` at the end).
- `--json` (`output.ActivityJSON`): `lead` (`lead_fields` plus the card's name, title, company and profile values when present), `since`, `events[]` (`id`, `date`, `type`, `primary`, `attributes`, and when set `primaryId`, `campaignId`, `campaign`, `detail`), `programs[]` (`id`, `name`, `status`, and when set `date`, `acquiredBy`, `success`, `title`, `link`) and `notes`. `--json` always has every event; raw `attributes` are unchanged from 0.1.0.
- `--out` writes the output (text or `--json`) to a file; see [`--out`](#--out-files).

Example (invented data):

```
Jane Doe · Platform Engineer @ Example Corp · lead 1001
score 85 · MQL · owner a.rep@example.com
last touch 9d ago · 14d: 1 form, 1 click, 1 open, 1 score change, 1 status change, +1 more · times in Asia/Tbilisi

2026-10-05 13:00  Change Data Value             Lead Status            Nurture→MQL via "Score Threshold"
2026-10-10 14:00  Fill Out Form                 Contact Us Form
2026-10-10 14:00  Change Score                  Behavior Score         +25 (60→85) Filled out form via "Behavior Scoring"
2026-10-11 12:30  Open Email                    Example Welcome Email
2026-10-11 12:31  Click Email                   Example Welcome Email  https://www.example.com/whitepaper
2026-10-12 16:00  Change Status in Progression  Example Webinar 2026   → Attended ✓ success via "Webinar Attendance"

Programs
  Example Webinar 2026  Attended ★ acquired ✓ success
+ 3 older programs (use --all-programs)
```

### `mkto why <email|value> [--days 90] [--web] [--by F] [--lead-id N]`
Explains a score or status (D4): the lead's changes to `score_field` and `status_field`, judged by the `[why]` rule.

- `[why]` is required. A missing or invalid key exits 2 before any API call, naming every key to set: `mkto why needs the [why] section in <path>: set score_field, mql_threshold (a whole number above 0), …`.
- Fetches Change Score, Change Data Value and Change Status in Progression plus `activity_types` (web types only with `--web`, as in `activity`). A change type the instance lacks warns: `activity type "<name>" is not on this instance; mkto why can't show those changes (see mkto types)`.
- Status history comes from the Change Data Value rows in the same activity walk (attributes `Old Value`, `New Value`, `Reason`: VERIFIED on a real instance, 2026-11-09). mkto does not call `leadchanges.json`: it returned the same changes at about 37 extra calls and 100 s per 90-day `why`.
- The ledger (`internal/story`) has one step per change to `score_field` (Change Score rows) or `status_field`. A crossing is judged on each step's own old and new values, never a running sum: `crossed <T>` when `old < T ≤ new`, `dropped below <T>` when `new < T ≤ old`; no crossing is claimed unless both values are numbers. `MQL` / `DQ` mark a status step whose new value is in `mql_values` / `dq_values`.
- **"Near" is timing only.** Each mark lists the other activities within ±`near_hours` of it (score and data-value rows excluded). A mark with none near lists instead up to 3 `before:` lines: the last such activities strictly before it in the window, newest first, with how long before (`3h before` under a day, else `2d before`). The help and README say it is not proof of what moved the score; output never says "caused".
- Output: the call card, then the rule line, the ledger table (DATE, FIELD, CHANGE `old→new`, Δ, REASON, CAMPAIGN; REASON and CAMPAIGN cut to 60 characters with `…`, `--json` keeps them whole), each mark with its `near:` or `before:` lines, and a `now:` line with the latest score and status values seen in the window (`?` when none). With no changes: `No score or <status_field> changes in the last <n> days.`

  ```
  rule: Behavior Score ≥ 80 · MQL when Lead Status in [MQL] · DQ in [Disqualified, Recycled]

  DATE              FIELD           CHANGE            Δ    REASON           CAMPAIGN
  2026-10-10 14:00  Behavior Score  60→85             +25  Filled out form  Behavior Scoring
  ...
  2026-10-10 14:00  crossed 80
    near: 2026-10-10 14:00 Fill Out Form Contact Us Form
  2026-10-18 13:00  dropped below 80
    before: 2026-10-17 13:00 Unsubscribe Email Example Newsletter (1d before)

  now: Behavior Score 75, Lead Status Disqualified
  ```

- The rule line prints what mkto used, so a wrong config shows in the output: `rule: <score_field> ≥ <T> · MQL when <status_field> in [<mql_values>]`, plus ` · DQ in [<dq_values>]` when set.
- Above 90 days, a stderr note: `note: Marketo may purge score and data-value changes after a retention period; older changes can be missing` (the exact period is UNVERIFIED).
- `--json` (`output.WhyJSON`): `lead`, `rule` (`scoreField`, `threshold`, `statusField`, `mqlValues`, `dqValues`, `nearHours`), `steps[]` (`date`, `kind` score|status, `field`, `old`, `new`, `reason`, `activityId`, and when set `delta`, `campaignId`, `campaign`), `marks[]` (`label`, `step`, `near[]` and, when set, `before[]`: events without raw attributes) and `notes`.

### `mkto leads --from FILE|- [--activity] [--web] [--csv] [--out FILE] [--force]`
Looks up many leads by email in few calls. Lead data stays local (D5) and never feeds `brief`.

- Input: one email per line, or the first CSV column (a BOM is skipped, rows may differ in width). `-` reads stdin. Values are trimmed and de-duplicated ignoring case, in input order. Lines without `@` (headers, blank cells) and invalid emails are counted on stderr (`note: skipped 2 lines without @ (headers or blank cells)`), never named. Caps: 10 MB and 30 000 emails, else exit 2. No emails left → exit 2. Under the snap, a permission error adds `the mkto snap can read only non-hidden paths in your home folder (home plug)`.
- Up to 300 emails per `leads.json` call, fewer when the URL would pass 7000 bytes. A missing profile field is dropped for the rest of the run.
- Table: EMAIL, ID, SCORE, STATUS, OWNER (as on the call card), UPDATED, one row per input email in input order. An email with no lead gets a `not found` row (the run still exits 0); an email shared by several leads gets one row per lead, marked `(dup)`. Empty cells show `-`.
- `--activity` adds LAST TOUCH and 14D (the top three touch counts) over 14 days from `activities.json`, 30 leads × 4 types per batch: a paging-token call plus one call per page for each batch, so the 10 default types cost about 6 calls per 30 leads plus extra pages. `--web` adds Visit Webpage and Click Link (2 more types) and is exit 2 without `--activity`.
- `--csv` (not with `--json`, exit 2) and `--out` follow [Output conventions](#output-conventions). `--json` (`output.LeadsJSON`): `leads[]` (as `lead --json`, plus `lastTouch` and `touches` with `--activity`) and `notFound[]`.

### `mkto account <sfdcAccountId> [--by F] [--days 14] [--web] [--csv] [--out FILE] [--force]`
Everyone at an account. Lead data stays local (D5) and never feeds `brief`.

- Finds the leads by `sfdcAccountId`, or another searchable field with `--by` (e.g. a custom domain field). At most 10 `leads.json` pages: more than 3000 matches is exit 2 (`more than 3000 leads match …; narrow --by`). None → exit 3 (`no leads found for <by> <value>`).
- Shows at most 200 leads, the highest scored (`note: showing the 200 highest-scored of <n> leads` on stderr).
- Fetches the leads' activities over `--days` (as `leads --activity`, `--web` for web types) and prints `Account <id> · <n> leads · last <n> days · times in <zone>`, then NAME, TITLE, SCORE, STATUS, OWNER, LAST TOUCH and `<days>D` (top three counts). Sorted by `leadScore`, highest first (missing or non-numeric last), then by last touch. A lead without a name shows `lead <id>`.
- `--csv` omits the header line. `--json` (`output.AccountJSON`): `account`, `by`, `since`, `total`, `leads[]` with `lastTouch` and `touches`.

### `mkto programs [--name SUBSTRING]`
Table of id, name, type, channel, workspace, created date. `--name` filters client-side (case-insensitive); no match prints `No programs match "<s>".` and exits 0.

### `mkto types [--search S]`
Every activity type, by id: ID, NAME, PRIMARY (the primary attribute's name) and ATTRIBUTES (the others, or `-`). `--search` is a case-insensitive substring match on the name or an attribute name. No match prints `No activity types match "<s>".` `--json`: `[{"id","name","description","primary","attributes"}]`.

### `mkto fields [--search S]`
Every lead field with a REST name, sorted by it: NAME, LABEL, TYPE, LENGTH (`-` when 0), READ-ONLY (`yes` or `-`). Fields without a REST name are left out, with `note: <n> fields have no REST name and are not shown` on stderr. `--search` matches the REST name or label, case-insensitively. `--json`: `[{"name","label","type","length","readOnly"}]`.

### `mkto quota [--by-user] [--errors]`
Today: used / `daily_quota` / remaining / percent, with the date (`Today (2026-11-08, US Central): …`). Then a 7-day table. Note that the limit comes from the config; usage above it warns on stderr.

- `--by-user` adds API USER / CALLS for today from the same `usage.json` call (no extra call), busiest first.
- `--errors` adds `API errors today` (ERROR, COUNT) from `stats/errors.json` (one extra call). Codes 601, 602, 606, 607, 615, 713 and 1006 get their meaning, e.g. `1006 (field not found)`. None: `No API errors today.`
- `--json` gains `byUser[{userId,count}]` and `errors[{code,count}]` only when the flag is given.

### `mkto brief <email|value>`
Phase 2 LLM brief: see `docs/LLM_LAYER.md` §CLI and §Output shape. Off unless `[llm] enabled = true`; `--show-payload` works without it. The off message lists the steps inline (set `[llm] enabled = true` and `api_key` or `MKTO_LLM_API_KEY` in `<path>`, preview with `--show-payload`) and links the sign-off doc. Flags: `--days 90`, `--by`, `--lead-id`, `--web` (as `activity`), `--no-research`, `--show-payload`, `--save`. Since 0.2.0 the payload carries score and status changes as numbers and status names only (D2), drops Email Delivered, and skips web types unless `--web`; see `docs/LLM_LAYER.md`. `brief` never fetches `profile_fields`. `--json` keys (`verified`, `declined`, `research`, `pitch`, `usage.*` and the rest) are listed in `docs/LLM_LAYER.md` §`--json` keys.
Exit codes: `0` ok (including a declined step), `1` error (brief off, missing `llm.api_key`, unsupported `llm.provider`, LLM or guard failure), `2` usage error, `3` lead not found, `4` daily quota exhausted.

### `mkto version`
Version, git commit (via `-ldflags`), Go version, and whether it's running as a snap (`$SNAP_NAME`, `$SNAP_REVISION`).

## Error handling additions (0.2.0, Phase 4)

- **Fail before spending quota.** The lookup argument, flag combinations, `[why]` and the `--out` file are checked before the first Marketo call.
- **Config errors list everything** (see [Validation](#validation)); the printer writes each joined part on its own line.
- **Where a credential came from.** An auth failure names each credential's source: `Marketo authentication failed (HTTP 401: …): check MKTO_CLIENT_SECRET (set in the environment) and client_id in <path>`.
- **Network hint.** A dial or DNS failure (including its timeout) ends with `check marketo.base_url and your network`, and errors that point at `base_url` add its source: `(base_url comes from MKTO_BASE_URL, set in the environment)` or `(base_url is in <path>)`. A slow answer on an open connection gets no hint.
- **Secret-safe TOML errors** for `client_secret` and `api_key` (see Validation).
- **1003** keeps Marketo's message and adds the `mkto fields` hint (see the error table).
- **Lookups fail soft.** Program names, program tokens and campaign names are cosmetic: a failure counts toward one warning per kind and the output falls back to `program <id>`, the program name, or `campaign <id>`. Quota exhaustion and Ctrl-C still stop the run.

## Output conventions

- Human output: plain text via `tabwriter`, no colour. Trailing padding is trimmed from timeline lines.
- Every piece of Marketo text printed as text or CSV goes through `output.Clean`, which replaces control and format characters (`Cf`: bidi overrides, zero-width characters) with U+FFFD, so a value can't drive the terminal.
- Marketo timestamps are UTC; display them in `output.timezone`. The binary imports `time/tzdata`, so IANA names work whatever zoneinfo the snap base ships. Lines that show times say which zone (`times in Asia/Tbilisi`).
- `--json`: stable, documented structs (defined in `internal/output`); timestamps in RFC 3339 UTC. Text and `--json` are rendered from the same data. Fields added in 0.2.0 are `omitempty`, so 0.1.0 output only grows.
- Data goes to stdout; errors, `warning:` and `note:` lines, progress lines and `wrote <path>` go to stderr. Notes that must not scroll away (duplicate leads, skipped web types) are printed before stdout output. In text mode `activity` keeps three short footers on stdout, after the programs: `+ <n> older programs (use --all-programs)`, `showing names for 25 of <n> programs`, and `Change Data Value rows hidden by change_fields: <n> (use --all-changes)`; in `--json` these go in `notes`.
- **CSV** (`leads`, `account`): `encoding/csv`, empty cells empty (the table shows `-`). Each cell is cleaned, then guarded against spreadsheet formulas: a cell starting with `=`, `+`, `-`, `@`, tab or CR gets a leading `'`, unless it parses as a number (so `-5` stays). `--csv` with `--json` is exit 2.

<a id="--out-files"></a>**`--out FILE`** (`activity`, `leads`, `account`) holds lead data:

- The file is opened before any Marketo call with `O_WRONLY|O_CREATE|O_EXCL` and mode `0600`, so a bad or taken path costs no quota.
- An existing file is exit 2: `<path> exists; use --force to overwrite`.
- `--force` writes a `0600` temp file `mkto-out-*.tmp` in the same directory (not a dot name, so the snap's `home` plug allows it) and renames it over the target only when the run succeeds; a failed run leaves the old file as it was.
- A failed run removes the file mkto created, so it leaves no empty or partial file.
- On success stderr gets `wrote <absolute path>`. Under the snap, a `/tmp` path adds `note: /tmp is private to the mkto snap; the file is at /tmp/snap-private-tmp/snap.mkto/tmp/… (readable with sudo)`, and a permission error adds `the mkto snap can write only to non-hidden paths in your home folder (home plug), e.g. ~/<name>`.
- `brief --save` writes to `briefs/` in the config directory (mode `0600`; see `docs/LLM_LAYER.md`).

## Testing

Use an `httptest` server serving fixtures from `testdata/marketo/*.json` (invented values; status per fixture in `testdata/marketo/README.md`). Cover at least:

- token caching, and refresh + single retry on 601/602
- 606/615 backoff and give-up; 607 stops with exit code 4
- `success: false` inside an HTTP 200
- activity pagination (`moreResult`) and batching when more than 4 type ids or 30 lead ids, at most 3 batches at once, one token refresh for parallel 601s, and a 607 winning over the cancellations
- leads and listMembership paging, with the repeat-token guard
- programs offset pagination
- email URL-encoding (address containing `+`), and the email check costing no calls
- duplicate leads
- config path resolution with and without `SNAP_USER_COMMON`, env overrides, permission warning, joined errors, the v0.1.0 migration
- `--out` rules: exists, `--force`, removal on failure, `wrote`
- the detail column never reading PII attributes
- `--json` golden files for every data command (plus text and CSV goldens in `testdata/golden/`); `doctor --json` is checked inline

`docs/PLAN.md` §Phase 4 names the test for each Phase 4 behaviour.

## Deviations from the plan

Where the code differs from `docs/PLAN.md` §Phase 4, the code is what this spec describes:

- `lead --lists` shows list ids and the date added, not names (T36 said id and name): `listMembership.json` has no names, and a per-list lookup would cost one call per list.
- The call card's first line ends with `lead <id>` (T20's example has no id), and the record link line starts with `open:`.
- `account` errors (exit 2) above 3000 matching leads, on top of T35's 200-lead display cap.
- Program tokens share the 25-*program* cap with program names, but each program costs up to two calls (name and tokens), so up to 50 asset calls per run (T30: "count toward the same 25-lookup cap").
- The 1003 hint reads `check that <field> is a searchable field (names: mkto fields)` (T27: `check the field is searchable: mkto fields`).
- `why`'s `now:` line shows the latest values in the window's ledger, not a fresh read of the lead's fields (T29: "the current values").
- T33 (status history via `leadchanges.json`) was removed after the real-instance run (T43): the Change Data Value attribute names it worked around are VERIFIED, and the second walk doubled `why`'s time (3 min 22 s for 90 days).
- T30 and T32 landed as one commit (`de8e5eb`), as did T31 and T35 (`c6ab674`, which also brought `leads --activity` from T34), because each pair rewrote the same code.
- `brief` sends Change Data Value rows for `status_field` only, not for score fields (T37 said "or a score field"; Change Score rows already carry the score), and ignores a `status_field` that looks like personal data (`redact.PersonalLabel`).
- Activity batches carry at most 4 type ids (Marketo allows 10) and run 3 at a time, and the merge drops repeated activity ids: T41, measured on a real instance (2026-11-09). On Marketo an activity has one type and one lead, so ids never repeat across batches; the dedup matters only for test servers that return the same page to every batch.
- Not done yet (T40): stripping query strings from Visit Webpage and Click Link primaries waits for checklist C10. Until then they print as Marketo sends them.

# mkto v1 specification

## Scope

v1 is six commands — `init`, `lead`, `activity`, `programs`, `quota`, `version`. Phase 2–3 add `mkto brief`, off by default (see `docs/LLM_LAYER.md`). Everything is read-only.

## Configuration

### Location (resolution order)

1. `--config <path>` flag
2. `$MKTO_CONFIG`
3. Running as a snap (`$SNAP_USER_COMMON` is set): `$SNAP_USER_COMMON/config.toml` → `~/snap/mkto/common/config.toml`
4. Otherwise: `os.UserConfigDir()/mkto/config.toml` → usually `~/.config/mkto/config.toml`

Why `$SNAP_USER_COMMON` and not `$HOME/.config`: under strict confinement `HOME` is `$SNAP_USER_DATA` (`~/snap/mkto/<revision>`), which snapd copies on every refresh. Keeping credentials in `common` means a single copy that survives refreshes and reverts.

`playbook.md`, `prompts/` and `briefs/` (Phase 2–3) live in the same directory as `config.toml`.

### Permissions

- `mkto init` creates the directory with mode `0700` and the config template with mode `0600`. It refuses to overwrite an existing file unless `--force` is given.
- On every load: if the file is group- or world-readable, print a warning to stderr with the exact `chmod 600 <path>` command. Don't fail.

### Format

```toml
[marketo]
# Admin > Web Services > REST API. Accept with or without a trailing /rest and normalise.
base_url = "https://123-ABC-456.mktorest.com"
client_id = ""
client_secret = ""
# The API doesn't report your subscription's limit, so it lives here.
daily_quota = 50000
lead_fields = ["id", "email", "firstName", "lastName", "title", "company",
               "industry", "country", "leadSource", "createdAt", "updatedAt"]
# Default activity types for `mkto activity`, resolved by NAME at runtime.
activity_types = ["Visit Webpage", "Fill Out Form", "Click Link", "Email Delivered",
                  "Open Email", "Click Email", "Unsubscribe Email",
                  "Change Status in Progression", "New Lead"]

[output]
timezone = "Local"   # or an IANA name, e.g. "Asia/Tbilisi"

[llm]                # mkto brief: off by default. Read docs/LLM_LAYER.md (sign-off) before enabling.
enabled = false
provider = "anthropic"
api_key = ""
model = "claude-sonnet-5-5"
```

### Environment overrides

`MKTO_BASE_URL`, `MKTO_CLIENT_ID`, `MKTO_CLIENT_SECRET` and `MKTO_LLM_API_KEY` (for `llm.api_key`) override the file (useful in CI). `MKTO_CONFIG` picks the file.

### Validation

On load, check `base_url`, `client_id`, `client_secret` are set. Missing values → error naming the key and the config path.

## Marketo API usage

Verify every endpoint against https://developer.adobe.com/marketo-apis/ before implementing it.

### Authentication

- `GET {base}/identity/oauth/token?grant_type=client_credentials&client_id=…&client_secret=…`
- Response has `access_token` and `expires_in` (seconds). Cache the token in memory for the process and refresh 60 s before expiry.
- Send the token as an `Authorization: Bearer <token>` header. **Never** as a query parameter.
- This URL contains the secret: never log it, even with `--verbose`.

### Response envelope

Marketo usually returns HTTP 200 even on failure. Always check `success`; on `false`, read `errors[].code` and `errors[].message`.

| Code | Meaning | Handling |
|---|---|---|
| 601 / 602 | Token invalid / expired | Refresh the token once, retry the request once. |
| 606 | Rate limit (100 calls / 20 s per instance) | Back off (start 5 s, exponential, max 3 retries), then fail with a clear message. |
| 615 | Concurrency limit (10 concurrent calls) | Same backoff as 606. mkto should never run more than a few calls concurrently anyway. |
| 713 | Transient error | Same backoff as 606 (Adobe: "wait … then retry"). |
| HTTP 502 | Bad gateway | Same backoff as 606 (Adobe: "retried with exponential backoff"). |
| 607 | Daily quota reached | Stop immediately. Tell the user, suggest `mkto quota`. Exit code 4. Quota resets at 12:00 AM US Central. |
| 1006 | Field not found | Can arrive response-level (`errors[]`) or record-level (`status: "skipped"`, `reasons[]`). Handle both: name the field. If it is in `lead_fields`, say to fix `lead_fields` in the config. A field mkto added itself is dropped and the lookup retried (see [optional fields](#optional-fields) under Endpoints); if the field is `id`, `email`, `firstName` or `lastName` (or the name can't be matched), say mkto needs `id`, `email`, `firstName` and `lastName` (the lookup key and the redaction guard) and this instance may not have that field. |
| other | — | Show code and message, exit 1. |

Also handle non-200 HTTP statuses, timeouts (30 s per request; 120 s for `activities.json`, see Endpoints), and non-JSON bodies (`unexpected non-JSON response from Marketo (HTTP <status>)`, never dump the body). Backoff sleeps stop at once when the context is cancelled (Ctrl-C).

Sources checked 2026-11-07: Adobe Experience League `marketo-developer/marketo/rest/{authentication,rest-api,error-codes,assets/programs}`. Query-param `access_token` support was removed on 2026-08-31.

### Endpoints

| Purpose | Request | Notes |
|---|---|---|
| Lead by email | `GET /rest/v1/leads.json?filterType=email&filterValues={email}&fields={csv}` | URL-encode the email (`+` addresses!). Can return 0, 1 or several leads (duplicates). |
| Activity types | `GET /rest/v1/activities/types.json` | Fetch once per run; map name ↔ id. Unknown names from config → warn and skip. |
| Paging token | `GET /rest/v1/activities/pagingtoken.json?sinceDatetime={RFC3339}` | Start of the requested window. |
| Activities | `GET /rest/v1/activities.json?nextPageToken={t}&activityTypeIds={ids}&leadIds={id}` | **Max 10 activity type ids per call**: batch them, with a fresh paging token per batch, then merge all batches sorted by `activityDate`. Loop while `moreResult` is true; a page can be empty while `moreResult` is still true. Stop with an error if the same `nextPageToken` repeats or after 1000 pages. Up to 300 results per page. **120 s per-request limit** (every other call keeps 30 s): Marketo scans instance-wide web traffic for Visit Webpage and Click Link, and on a real instance (2026-11-07) a 7-day page took up to 29 s while a 30-day page passed 30 s. A page slower than 5 s prints `scanning activity: page <n> took <s>s (web visits and link clicks are slow on large Marketo instances)` to stderr, with or without `--verbose`. |
| Program membership | `GET /rest/v1/leads/{id}/programMembership.json` | Returns program ids + progression status. The membership date is `membershipDate`, falling back to `updatedAt` (unconfirmed on a real instance; parsed like asset-API dates). |
| Program by id | `GET /rest/asset/v1/program/{id}.json` | Resolve names of the shown memberships only; only the name is decoded, so a date-shape change can't break it. Cache per run; cap at 25 lookups and say so if capped. Failed lookups give one warning per run: `warning: could not look up <n> of <m> program names (<reason>)`, where the reason is `check the API user's asset permissions` for 603 or HTTP 401/403, `unexpected response format from Marketo; please report it` for a decode error, else the error text. Quota exhaustion and Ctrl-C still stop the command. |
| Programs | `GET /rest/asset/v1/programs.json?maxReturn=200&offset={n}` | Page by offset until an empty result (don't rely on warning code 702). There is no multi-id lookup, so the 25-lookup cap above stays. |
| Usage today | `GET /rest/v1/stats/usage.json` | Sum `total` (across API users) for today. "Today" is the `America/Chicago` calendar date, because the quota resets at 12:00 AM Central. |
| Usage, 7 days | `GET /rest/v1/stats/usage/last7days.json` | |

Asset-API dates (`createdAt`, `updatedAt`) are **not** RFC 3339: a real instance (2026-11-07) sent `2026-11-01T01:08:44Z+0000`, a literal `Z` followed by an offset. mkto parses RFC 3339, `…Z+0000` / `…Z-0700` and plain `…+0000` with one helper.

<a id="optional-fields"></a>Fields mkto adds itself (beyond `lead_fields`) can be missing on an instance: on 1006 for one of them, mkto drops it and retries the lead lookup (once per such field), except `id`, `email`, `firstName` and `lastName`, which it needs (the lookup key and brief's redaction guard); `--verbose` prints `note: field "<f>" isn't on this Marketo instance; skipped`. A real instance (2026-11-07) lacked `numberOfEmployees`. 1006 for a field in `lead_fields` still errors.

Activity records have `activityDate`, `activityTypeId`, `primaryAttributeValue` and an `attributes` list whose names vary by type (e.g. a webpage URL, a link, query parameters, client IP, user agent). Inspect real responses (in a safe environment) and build fixtures from invented values.

Data retention: Marketo purges some high-volume activity types after a limited period. An empty result for an older window isn't necessarily a bug; mention this in `--help` for `activity`.

## Commands

Global flags: `--config <path>`, `--json`, `--verbose` (method, URL path without query string, status, timings, retry notices — never secrets, tokens or lead emails).

Lead emails are trimmed of surrounding whitespace before lookup.

Exit codes: `0` ok, `1` error, `2` usage error / not implemented, `3` lead not found, `4` daily quota exhausted.

### `mkto init [--force]`
Writes the commented template above to the resolved config path (0700 dir, 0600 file) and prints the path plus next steps.

### `mkto lead <email>`
Prints the configured `lead_fields` as a two-column table. If several leads match, print each one with its id and a note about duplicates.

### `mkto activity <email> [--days 30] [--types "Open Email,Click Email"] [--all-types] [--all-programs] [--lead-id N] [--out FILE]`
- Resolves the lead. With duplicates, pick the most recent `updatedAt`, list the other ids in a note, and point to `--lead-id`.
- Default window 30 days; `--days` must be ≥ 1 (else exit 2). Above 90, print a quota warning.
- Default types come from `activity_types` in the config. `--types` overrides; `--all-types` uses every type (warn about quota). `--types` and `--all-types` together → exit 2.
- Prints a chronological timeline, then a "Programs" section from program membership: only memberships dated within the window, newest first, then `+ <n> older programs (use --all-programs)` when some are hidden. `--all-programs` shows every membership (newest first where dated). If no membership carries a date, all are shown. In `--json`, `programs` is the shown set with a `date` (RFC 3339 UTC, omitted when unknown) and the older-count message goes in `notes`.
- `--out` writes the output (respecting `--json`) to a file instead of stdout, with mode `0600` (it holds lead data).

Example:

```
Lead 1234567 · Platform Engineer · Example Corp · last 30 days

2026-10-02 14:03  Email Delivered               Ubuntu Pro newsletter – Oct
2026-10-02 18:41  Open Email                    Ubuntu Pro newsletter – Oct
2026-10-03 09:12  Visit Webpage                 ubuntu.com/pro
2026-10-03 09:15  Fill Out Form                 Contact us – Ubuntu Pro

Programs
  2026-Q4 Ubuntu Pro webinar      Attended
```

### `mkto programs [--name SUBSTRING]`
Table of id, name, type, channel, workspace, created date. `--name` filters client-side (case-insensitive).

### `mkto quota`
Today: used / `daily_quota` / remaining / percent. Then a 7-day table. Note that the limit comes from the config.

### `mkto brief <email>`
Phase 2 LLM brief: see `docs/LLM_LAYER.md` §CLI and §Output shape. Off unless `[llm] enabled = true`; `--show-payload` works without it. `--json` keys (`verified`, `declined`, `research`, `pitch`, `usage.*` and the rest) are listed in `docs/LLM_LAYER.md` §`--json` keys.
Exit codes: `0` ok (including a declined step), `1` error (brief off, missing `llm.api_key`, unsupported `llm.provider`, LLM or guard failure), `2` usage error, `3` lead not found, `4` daily quota exhausted.

### `mkto version`
Version, git commit (via `-ldflags`), Go version, and whether it's running as a snap (`$SNAP_NAME`, `$SNAP_REVISION`).

## Output conventions

- Human output: plain text via `tabwriter`, no colour in v1.
- Marketo timestamps are UTC; display them in `output.timezone`. The binary imports `time/tzdata`, so IANA names work whatever zoneinfo the snap base ships.
- `--json`: stable, documented structs (define them in `internal/output`); timestamps in RFC 3339 UTC.
- Errors go to stderr; data goes to stdout.

## Testing

Use an `httptest` server serving fixtures from `testdata/marketo/*.json`. Cover at least:

- token caching, and refresh + single retry on 601/602
- 606/615 backoff and give-up; 607 stops with exit code 4
- `success: false` inside an HTTP 200
- activity pagination (`moreResult`) and batching when more than 10 type ids
- programs offset pagination
- email URL-encoding (address containing `+`)
- duplicate leads
- config path resolution with and without `SNAP_USER_COMMON`, env overrides, permission warning
- `--json` golden files for every command

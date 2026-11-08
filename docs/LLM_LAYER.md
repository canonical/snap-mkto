# mkto brief — LLM layer (Phases 2 and 3)

Do not start this until v1 and the snap (Phases 0–1.5 in `docs/TASKS.md`) are done and tested.

## Before using it with real leads

Get sign-off from Canonical's data protection / legal contact for sending **redacted** lead activity and company names to the chosen LLM provider. `mkto brief --show-payload` exists to make that review easy: it prints exactly what would be sent and sends nothing.

Until then, develop and test against invented fixtures only.

## Pipeline

```
fetch → redact → narrate → verify → render                (Phase 2)
                                 ↘ research → pitch ↗      (Phase 3)
```

1. **Fetch.** Reuse v1 code: lead fields and activities (default `--days 90`). No program memberships (memberships are not part of the redacted payload).
2. **Redact** (`internal/redact`). Build a `redact.Payload` using the rules below. This is the only input the narrate/verify steps accept.
3. **Narrate.** One LLM call. Input: the redacted lead profile and events, each with an id (`E1 … En`). Output: short prose (≤ 200 words) where every sentence cites at least one event id, e.g. `[E3]`. The prompt says: use only the listed events, don't invent dates or assets, describe intent only as a possibility ("may indicate").
4. **Verify.** Two checks:
   - Deterministic: every sentence has a citation; every cited id exists.
   - LLM: one call checks every sentence against its cited events and returns JSON `{"unsupported": [sentence indexes]}`. Unsupported sentences are removed. With `--verbose`, list them under "Removed claims".
5. **Research** (Phase 3). One LLM call with the provider's web search tool. Input: a separate `redact.CompanyQuery` with **only** company name, industry and country — no activity data, no title. Output (JSON): summary, approximate size, industry, infrastructure / tech signals (cloud, Kubernetes, OpenStack, AI/ML, IoT, compliance needs), recent news, and `sources[]` (URL + title). Discard any claim without a source.
6. **Pitch** (Phase 3). Input: verified narrative + research + the user's `playbook.md`. Output: at most 3 relevant offerings from the playbook, each with one line of reasoning tied to an event id or a research source; a suggested opening line; and a "What we don't know" list. If nothing in the playbook fits, say so.
7. **Render.** Markdown to stdout (or `--json`). `--save` writes `briefs/<UTC timestamp>-<lead id>.md` (e.g. `20261107T120000Z-1001.md`) in the config directory, as well as printing the brief: the directory is created 0700, the file 0600, and an existing file is never overwritten (a second save in the same second gets `-2`). With `--json`, stdout is JSON and the saved file is still the markdown brief. Saving is off by default, and saved briefs never contain the lead's name or email.

## Redaction rules (`internal/redact`)

**Approach: allowlist.** Build the payload from known-safe fields only; anything not listed here is dropped.

### Lead profile

- Keep: `company`, `industry`, `country`, `numberOfEmployees` (if present), generalised title (below).
- Drop: everything else — names, email, phone, address, city, postal code, IP, social profile URLs, all custom fields.
- Refer to the person only as "the lead".

### Titles

Always generalise, deterministically, **in code** (sending the raw title to an LLM to generalise it would defeat the purpose). Map keywords to `<seniority> <function>` buckets, e.g. `"VP of Platform Engineering"` → `"senior engineering leader"`, `"Data Scientist"` → `"individual contributor, data"`. Unknown → `"unknown role"`. Keep the mapping table in code with tests.

### Activities

- Keep: activity type name, timestamp (date + hour, UTC), and `primaryAttributeValue` for asset-type activities (email, form, program names).
- "Interesting Moment" descriptions are free text that can contain anything: keep only the type and timestamp, and drop `primaryAttributeValue`.
- Known limit: asset names can contain *other* people's names (e.g. a sales rep's). The guard below can't detect those; the `--show-payload` review exists for this.
- Known limit: `company`, `industry` and `country` are free text typed by the lead and are only pattern-checked by the guard, so other people's names and street addresses there pass.
- Known limit: the lead's own name is matched without Unicode normalisation, so accent variants (e.g. "Zoe" for "Zoë") pass the guard.
- Known limit: phone numbers split across URL path segments, and doubly-encoded `%2540` inside asset names, are not detected.
- Known limit: a phone written as a valid date/time on its own (e.g. 2026-10-11 09:00) passes. A phone split across two or more dates (digits, date, digits, date, digits) also passes; only deliberately crafted input looks like that.
- Known limit: in-place redaction removes only what the detectors find. Undetected personal data in the same value (e.g. a street address next to a redacted name) is sent. Review with `--show-payload` before sign-off.
- Review the payload with `--show-payload` before data-protection sign-off.
- For webpage / link activities: keep the URL reduced to scheme + host + path.
- Score and status changes go in the event's `change` value, numbers and status names only:
  - Change Score: the score field's label as `asset` (a field name, not personal data) and `change` = signed delta and old→new, e.g. `+25 (60→85)`. Each value is parsed as a number and re-formatted, so any text in it is dropped; a part whose values aren't numbers is left out.
  - Change Data Value: only rows whose field label equals `[why] status_field` (case-insensitive), with the label as `asset` and `change` = `old→new`. Rows for any other field are dropped entirely; with no `status_field` set, all of them are.
  - Change Status in Progression: the program name as `asset` (as before) and `change` = `→ <new status>`.
  - Never: the Reason text, the campaign, or any other attribute of these rows.
  - `change` is scanned and redacted in place by the final guard like every other value.
  - Approved by Erekle 2026-11-08 (D2). A `status_field` whose label looks like personal data (email, phone, name, address, city, postal, IP, birth, social) is ignored in code (`redact.PersonalLabel`), so a mis-set config can't send those changes.
- Email Delivered rows are dropped.
- Drop: all other attributes, including query parameters, client IP, user agent, referrer query strings, search queries.

### URLs

Parse properly (`net/url`). Drop the query string and fragment (this removes `mkt_tok` tracking tokens and prefilled form values). Then replace any path segment that looks like an email address, or a token-like segment (≥ 24 chars of `[A-Za-z0-9_-]`), with `{redacted}`.

### Final guard (redact in place, then fail closed)

Scan every value of the payload for:

- email-address patterns (including Unicode, `%40`, `(at)`/`[dot]` and spelled-out "at … dot")
- phone-like sequences (≥ 9 digits allowing spaces, dots, dashes, slashes, parentheses, leading `+`)
- IPv4 / IPv6 addresses
- the lead's first name, last name (case-insensitive, length ≥ 3), first and last name joined, and the local part of their email. `brief` always fetches `firstName`, `lastName` and `email` for this check, even when they aren't in `lead_fields`.

Each hit is replaced in place: exactly the matched span becomes `{redacted}` and the rest of the value is kept (`Follow-up for Jane` → `Follow-up for {redacted}`). Then the full guard runs again on the result, up to 3 passes. If a hit survives, abort with an error naming the kind and the field, **never the value**. `Payload.Redactions()` lists what was replaced (kind and JSON path, never the value); `--show-payload` prints it as a `# Redacted before sending: …` line, and `--verbose` prints it on a normal run. The research query comes from the already-redacted lead fields (`Jane's Bakery` → `{redacted}'s Bakery`).

Dates and times are not phones. Before the phone search, dates (`2026-10-11`, optionally with ` 01:20:10` or `T01:20:10.123+02:00`) and standalone times (`9:00`, `10:30:15`) are cut out, so their own digits don't count toward the 9. Only years 19xx/20xx and valid months, days, hours and minutes count as a date or time, and one directly next to another digit or `:` stays in the search. The digit runs directly around a cut date or time (joined to it through phone separators) are still added up: 9+ digits there make the runs and the date one phone (`555-0100 2026-10-11 199` → `{redacted}`).

URL path segments that `ScrubURL` replaced (an email or a token) are listed too, as `email or token in URL`. A `{redacted}` marker is never matched as the lead's name (a lead called Red Acted).

Decided by Erekle 2026-11-08 after a real-instance false positive: asset names like `Validation Report 2026-10-11 01:20:10.Email 1` were read as a phone number and the whole brief aborted.

### Type enforcement

`redact.Payload` and `redact.CompanyQuery` have unexported fields. A `Payload` can only be built with `redact.Build(...)`, which runs the guard. `CompanyQuery` can only be obtained from a built Payload via `Payload.Company()`. Every public function in `internal/llm` takes one of these types, never raw `marketo` types, and every `llm.Provider` method has a `redact.*` parameter (`TestLLMTakesOnlyRedactTypes` checks both). `Pitch` takes the `redact.Payload` plus an `llm.PitchInput`, which holds only the outputs of the earlier redact-gated steps (the checked narrative sentences, the research claims and sources) and the playbook text. Its valid event ids are derived inside `internal/llm`: the payload events that a kept sentence cites.

### Tests (table-driven, all invented data)

- email inside a URL path; `mkt_tok` in a query string; prefilled form URL with email in the query
- phone number in a custom field (dropped by the allowlist)
- lead's first name inside an email asset name, e.g. "Follow-up for Jane" when the lead is Jane → `Follow-up for {redacted}`
- IP in an attribute (dropped) and IP pasted into an asset name (redacted in place)
- a date-time in an asset name is kept; a phone next to a date is redacted and the date kept
- a broken redactor (test hook) makes `Build` fail closed
- title bucket mapping, including unknown titles
- the research query contains only company, industry, country

## Provider

- Anthropic Messages API, using the official Go SDK if it's current and supports server-side tools; otherwise plain `net/http`. Check https://docs.claude.com/en/api/overview first.
- Model comes from `llm.model` (default `claude-sonnet-5-5`). Don't hardcode model names anywhere else.
- The research step uses Anthropic's server-side web search tool. Look up the current tool type string in the docs; don't copy one from memory. Limit searches per run (e.g. `max_uses` 5).
- Define an `llm.Provider` interface so an OpenAI-compatible provider can be added later without touching pipeline code. Research needs a provider with web search; if unavailable, print "research unavailable for this provider" and continue.
- Per-step timeouts and `max_tokens`. With `--verbose`, print token usage per step and in total.
- API key from `llm.api_key` or `$MKTO_LLM_API_KEY`. Same masking rules as the Marketo secret.

## Prompts

Store prompt templates as markdown in `internal/llm/prompts/*.md`, embedded with `go:embed`. A file with the same name in `<config dir>/prompts/` overrides the embedded one, so prompts can be tuned without rebuilding the snap.

Files: `narrate.md`, `verify.md`, `research.md`, `pitch.md`.

## playbook.md

Lives in the config directory (`~/snap/mkto/common/playbook.md` under the snap). Never in the repo, never embedded in the binary — anyone can unpack a published snap. `PLAYBOOK.example.md` in the repo shows the shape. If the file is missing, skip the pitch step with a one-line message.

## CLI

```
mkto brief <email> [--days 90] [--lead-id N] [--no-research] [--show-payload] [--save] [--web] [--json]
```

- By default `brief` skips the web activity types: "Visit Webpage" and "Click Link" (any case) are left out of `activity_types` for the fetch, and one stderr note says so: `note: skipping web visits and link clicks (slow on large Marketo instances); add --web to include them`. They make Marketo scan instance-wide web traffic (25–31 s a page on a real instance, 2026-11-08). `--web` includes them; `--show-payload` honours it. Since 0.2.0 `mkto activity` skips them the same way and takes the same `--web` flag.

- Refuse to run unless `[llm] enabled = true`, with a message pointing to this doc's sign-off section.
- `--show-payload`: run fetch + redact, print the narrate payload and the research query (omitted with `--no-research`) exactly as they would be sent, send nothing, exit 0. Works even when `enabled = false`. It can't be combined with `--save` (exit 2).
- `--lead-id N` picks one lead when several share the email.
- `--save` refuses to write through a symlinked `briefs/` (exit 1), and warns with a `chmod 700` hint if an existing `briefs/` is open to group or others.

### `--json` keys

```
company     string   company name ("" if unknown)
days        int      the --days window
verified    bool     true unless a step was declined (every sentence shown passed verify)
declined    string   "", "narrate" or "verify"
sentences   [{text, cites}]           kept narrative sentences
removed     [{text, cites, reason}]   dropped sentences ("no valid citation" or "unsupported")
research    {summary, size, industry, signals, news, sources, unavailable} or null with --no-research
            claims are {text, sources} (1-based into sources); size/industry may be null;
            sources are {url, title}; unavailable is "" or why research is missing
pitch       {offerings: [{name, reason, cites}], opener, unknowns, no_fit, unavailable}
            cites are "E<n>" or "<n>" (a source number); unavailable is "" or why the pitch was skipped
events      [{id, date, type?, asset?, url?}]
usage       {narrate, verify, research, pitch, total}, each {in, out} tokens
```

JSON strings are never markdown-escaped. Skipped or failed steps also print a one-line note to stderr.

## Output shape

````markdown
# Lead brief · Example Corp · last 90 days

## What happened
The lead visited the pricing page [E1]. A day later the lead filled out the contact form, which may indicate buying interest [E2].

## About Example Corp
Example Corp is a German software company [2]. It has about 250 employees [2].

Signals:
- It runs workloads on Kubernetes [1].

News:
- It announced a Kubernetes migration in September 2026 [1][2].

## How Canonical could help
1. **Ubuntu Pro** — The pricing visit may indicate interest in support for what they run [E1].
2. **Kubernetes** — They announced a Kubernetes migration [E2][1].

Suggested opener: "I saw Example Corp is moving to Kubernetes; happy to share what other platform teams learned."

## What we don't know
- Whether the lead runs Ubuntu today.

## Events
```
E1  2026-11-01 10:00 UTC  Visit Webpage       https://www.example.com/pricing
E2  2026-11-02 11:00 UTC  Fill Out Form       Contact Us Form
E3  2026-11-03 08:00 UTC  Interesting Moment
```

## Sources
1. https://news.example.com/example-corp-k8s — Example Corp moves to Kubernetes
2. https://www.example.org/about — About Example Corp
````

(This is `testdata/golden/brief.md`.) Sections that have nothing to show say so in one italic line: `_Research unavailable: …_`, `_No playbook.md in <config dir>; skipped._`, `_playbook.md is larger than 64 KiB; skipped._`, `_Pitch unavailable: …_` or `_Nothing in the playbook fits these signals._`. "What we don't know" is left out when the list is empty. `--no-research` drops "About" and "Sources"; the pitch still runs, with no research claims.

### Pitch checks (R39)

The model's offerings are checked in code before rendering. An offering is kept only if:

- its name matches a `### ` heading in `playbook.md` (case-insensitive, trimmed; a trailing parenthetical such as "(placeholder — verify)" is optional, and the brief shows the heading without it);
- it has a reason and at least one cite, and every cite is either a payload event id that a kept narrative sentence cites (`E3`), or a research source number (`2`; `[2]` is accepted and shown as `[2]`).

Duplicates are dropped and at most 3 are kept. If none is left, or the model sets `no_fit`, the brief says nothing in the playbook fits.

### Markdown escaping (R38)

In the markdown brief (stdout and `--save`, not `--json`), every model or web string — narrative sentences, research claims, source titles, offering names, reasons, the opener and the unknowns — and the company name have the markdown characters `` \ ` [ ] ( ) ! < > * _ # ~ `` backslash-escaped after control characters are removed. So model text can't add link text, images or HTML. A bare URL in it may still be auto-linked by GFM viewers, but then the visible text always equals the target. Source URLs are not escaped; they come straight from the web search results. The Events block (Marketo asset names and decoded URL paths) is printed inside a code fence that is longer than any backtick run in it, so nothing there renders either.

## Snap impact

None. LLM calls use the existing `network` plug; prompts, playbook and briefs live in `$SNAP_USER_COMMON`.

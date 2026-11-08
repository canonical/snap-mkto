# Real-instance checklist

Commands for Erekle to run by hand. Claude can't run them: they need `sudo`, an installed snap, or real Marketo credentials.

**Never paste output that contains lead data (emails, names, companies, activity) into the repo, an issue or a chat with Claude.** Report only pass/fail per step and the text of any error message. Error messages from mkto never contain secrets; check before pasting anyway.

Run everything from the repo root, after `snapcraft pack` has produced `mkto_0.2.0_amd64.snap`.

## Part A: install and confinement (no Marketo needed)

| # | Command | Pass when |
|---|---|---|
| A1 | `sudo snap install --dangerous ./mkto_0.2.0_amd64.snap` | `mkto 0.2.0 installed` |
| A2 | `snap connections mkto` | `network` and `home` are both listed with a slot (`:network`, `:home`). If `home` shows `-`, run `sudo snap connect mkto:home` and note it. |
| A3 | `mkto version` | `mkto 0.1.0`, `commit:` a short hash (or `unknown`), `snap: mkto (x1)` |
| A4 | `mkto init` | Prints `Wrote /home/<you>/snap/mkto/common/config.toml` |
| A5 | `stat -c '%a %n' ~/snap/mkto/common/config.toml ~/snap/mkto/common` | File is `600`. Note the dir mode too: snapd creates `common/` and mkto leaves its mode alone. |
| A6 | `mkto init` (again) | Refuses to overwrite and mentions `--force`; exit code 1 (`echo $?`) |
| A7 | `sed -i 's/^timezone = .*/timezone = "Mars\/Base"/' ~/snap/mkto/common/config.toml && MKTO_BASE_URL=https://example.invalid MKTO_CLIENT_ID=x MKTO_CLIENT_SECRET=y mkto quota; echo $?` | `unknown timezone "Mars/Base"`, exit 1. The fake credentials only get past the "key not set" check; `example.invalid` never resolves, so nothing reaches Marketo. Inline values are fine here only because they are fake; never type the real secret inline (shell history). |
| A8 | `sed -i 's/^timezone = .*/timezone = "Asia\/Tbilisi"/' ~/snap/mkto/common/config.toml && MKTO_BASE_URL=https://example.invalid MKTO_CLIENT_ID=x MKTO_CLIENT_SECRET=y mkto quota; echo $?` | No timezone error; it fails with `GET /identity/oauth/token failed: dial tcp: lookup example.invalid ... no such host: check marketo.base_url and your network (base_url comes from MKTO_BASE_URL, set in the environment)`, exit 1. This proves the IANA zone loads and DNS works under confinement (`network` plug). A `permission denied` here instead means a confinement problem: report it. |
| A9 | `journalctl -k --since "15 min ago" \| grep -i apparmor \| grep mkto` | Nothing. For each `apparmor="DENIED"` line, copy `operation`, `name` and `requested_mask` (these hold no lead data). |
| A10 (optional) | `sudo snap install snappy-debug && sudo snappy-debug` in a second terminal, then repeat A3–A8 | Explains each denial and suggests the interface |

`--out` is only on `mkto activity`, which needs a real lead, so the `--out` confinement checks are B5a–B5c below.

## Part B: real instance, read-only

Use a production **API-only user whose role has read permissions only**. Edit `~/snap/mkto/common/config.toml`: `base_url`, `client_id`, `client_secret`, `daily_quota`, and set `timezone` back to the value you want (e.g. `Asia/Tbilisi`). Pick a test lead you're allowed to look at.

Each step costs a few API calls. `--verbose` prints method, path, status and timing only (never tokens or query strings); add it to any step that fails.

| # | Command | What to look for |
|---|---|---|
| B1 | `mkto quota` | Today's count and the `daily_quota` you set; a 7-day line. No error. |
| B2 | `mkto lead <email>` | Profile renders; timestamps shown in your timezone. Exit 0. |
| B3 | `mkto lead nobody-$(date +%s)@example.com; echo $?` | `no lead found for ...`, exit 3 |
| B4 | `mkto activity <email> --days 30` | Timeline renders and ends; programs section present. Exit 0. |
| B5a | `mkto activity <email> --days 30 --json --out ~/b5.json; stat -c %a ~/b5.json` | Written to your real home (`home` plug), mode 600, valid JSON (`python3 -m json.tool ~/b5.json > /dev/null`). **Don't share the file.** |
| B5b | `mkto activity <email> --days 7 --out /tmp/b5.txt; ls -l /tmp/b5.txt; sudo ls -l /tmp/snap-private-tmp/snap.mkto/tmp/` | `ls /tmp/b5.txt` says `No such file`; the file is in the snap's private tmp instead |
| B5c | `mkto activity <email> --days 7 --out ~/.b5.txt; echo $?` | Expected to fail with `permission denied`: the `home` plug excludes hidden files at the top of `$HOME`. Note the error text and the matching AppArmor line (A9). |
| | `rm -f ~/b5.json; sudo rm -f /tmp/snap-private-tmp/snap.mkto/tmp/b5.txt` | Clean up: these files hold lead data |
| B6 | `mkto programs --name <word>` | Matching programs listed; the command ends (no endless paging). |
| B7 | `mkto programs --name zzz-no-such-program-zzz; echo $?` | `No programs match "zzz-no-such-program-zzz".`, exit 0 |
| B8 | `mkto quota` (again) | Count rose by roughly the calls made above |

### Items marked UNVERIFIED in `testdata/marketo/README.md`

These fixtures were written from the docs without a real response. Report pass/fail for each:

1. **Paging-token body** (`GET /rest/v1/activities/pagingtoken.json`). B4 working at all confirms mkto reads `nextPageToken` correctly. With `--verbose`, the first activities call should follow a `pagingtoken.json` 200.
2. **`programMembership` shape and paging** (`GET /rest/v1/leads/{id}/programMembership.json`). B4's programs section should list the lead's programs with names and statuses. If the lead is in many programs (over ~300), check that the list looks complete and `--verbose` shows more than one membership call, or none is needed.
3. **1006 shapes** (field not found). Add a bogus field to `lead_fields`, e.g. `"notARealField"`, then run `mkto lead <email>`. Pass when the error names the field and tells you to fix `lead_fields`. Report the exact error text, and whether it looked response-level or record-level (`--verbose` shows the status). Remove the bogus field after.
4. **Programs empty page** (`GET /rest/asset/v1/programs.json` at the end of the list). B6 and B7 must end cleanly. If either hangs or errors with a paging message, report the text.

5. **Activity type names and ids** (`GET /rest/v1/activities/types.json`). The fixture's names and ids (other than id 2) are from memory, and mkto resolves `activity_types` by name. Run B4 with `--verbose` and report any `note: default activity types not on this instance: <names>` line (or `warning: unknown activity type "<name>" skipped` if you set `activity_types` yourself), and report `No activity in the last 30 days.` for a lead you know was active. Either one means a default name doesn't match your instance. Since 0.2.0 a default list never warns without `--verbose`, because some default names (e.g. Email Bounced Soft) are unverified.
6. **Last-7-days usage** (`GET /rest/v1/stats/usage/last7days.json`). The fixture's sample is inferred. On B1, check that the `Last 7 days` table has 7 dated rows with plausible totals, and that today's row (if listed) matches the count on the `Today (..., US Central)` line.

**Confirmed on the 2026-11-07 run** (real production instance):

- Item 1 passes: the paging token works.
- Visit Webpage is slow: 7-day activity pages took 29 s, 29 s and 8 s; a 30-day page passed the old 30 s limit. Email types took about 3 s per page. mkto now allows 120 s for `activities.json` and prints a `scanning activity: page <n> took <s>s` line for pages over 5 s. Next run: check a 30-day `mkto activity` finishes and the progress lines appear.
- Asset-API dates are `2026-11-01T01:08:44Z+0000` (Z plus offset), not RFC 3339. Program names failed to decode until this was handled. Next run: check program names show in `mkto activity` and `mkto programs` lists programs.
- `numberOfEmployees` isn't on that instance (1006 on `mkto brief`); mkto now skips its own optional fields when missing. Next run: `mkto brief <email> --show-payload --verbose` shows the `note: field "numberOfEmployees"` line and succeeds.
- "Interesting Moment" isn't an activity type there; it was removed from the default `activity_types`.
- Still to confirm: **membership dates** (`membershipDate` / `updatedAt` on `programMembership.json`) and their format. On the next run, the Programs section of `mkto activity` should list only programs joined within `--days`, newest first, with a `+ <n> older programs` line; if it lists every program with no older line, Marketo sent no usable date. Report which, and paste the date shape from one membership row if possible. **Confirmed 2026-11-08**, see below.

**Confirmed on the 2026-11-08 run** (real production instance):

- Membership dates work: the Programs window showed 4 recent programs and `+ 110 older`.
- Web types are the slow part: `mkto brief <email> --show-payload` over 90 days took about 11 minutes, 24 activities pages at 25–31 s each, all from Visit Webpage and Click Link (email types page in about 3 s). `brief` now skips those two types unless `--web` is given, with one stderr note. Next run: check the default brief finishes in well under a minute and prints the note.
- Date-time false positive: the run aborted with `redaction guard: phone number found in events[0].asset` because asset names like `<program>.Validation Report 2026-10-11 01:20:10.Email 1` hold a date-time whose digits the phone rule counted. Fixed: dates and times are cut out of the phone search, and the guard now redacts hits in place instead of aborting. Next run: `--show-payload` succeeds, keeps those asset names whole, and prints a `# Redacted before sending: …` line.

**Confirmed on the 2026-11-08 follow-up run:**

- The default `brief --show-payload` (web types skipped) finished quickly, printed the skip note, kept the `Validation Report <date-time>` asset names whole and printed `# Redacted before sending: nothing`.
- Confinement (Part A and B5): plugs connected, config 600, no AppArmor denials for `snap.mkto`, `--out` to home works, `/tmp` is snap-private, hidden home files are denied. mkto now explains the last two in its output (a note for `/tmp`, a hint in the permission error).

Also report: any message mentioning `unexpected non-JSON response`, any retry notice in `--verbose` (606/615/713/502), and anything that worked with `go run` but not in the snap.

## Part C: 0.2.0 (Phase 4) shapes to confirm

Phase 4 rests on names that Adobe's docs don't confirm. These checks show the real ones. Build from source (`make build`) and point it at your snap config: `./mkto --config ~/snap/mkto/common/config.toml <command>`. The same rules apply as above: report only pass/fail, counts and names, never lead data.

| # | Command | What to report |
|---|---|---|
| C1 | `./mkto --config ~/snap/mkto/common/config.toml doctor` | Every line that starts with `✗` or `!`, copied as is. These hold only config key names, field and type names, and counts. |
| C2 | `... types --search score` and `... types --search "data value"` | The NAME, PRIMARY and ATTRIBUTES columns for Change Score and Change Data Value. Are the attributes `Change Value`, `Old Value`, `New Value`, `Reason`? |
| C3 | `... types --search progression` and `... types --search bounce` | The attribute names for Change Status in Progression (`New Status`, `Success`?) and Email Bounced / Email Bounced Soft (`Category`, `Details`?). Does Email Bounced Soft exist? |
| C4 | `... types --search "click email"` | Is the clicked-link attribute called `Link`? |
| C5 | `... fields --search score`, `--search status`, `--search owner`, `--search sfdc` | The REST names of your lead score, lead status and owner fields, and the SFDC id fields. Do `leadScore`, `leadStatus`, `sfdcLeadOwnerId` (or `leadOwnerEmailAddress`), `sfdcLeadId`, `sfdcContactId` and `sfdcAccountId` exist? Are there behaviour or demographic score fields (custom `__c`)? |
| C6 | `... fields` | Does a `note: N fields have no REST name` line appear on stderr? Report N. Are `id`, `createdAt` and `updatedAt` listed? |
| C7 | `... activity <email> --days 30 --json --out ~/c7.json`, then `python3 -c "import json;e=json.load(open('$HOME/c7.json'))['events'];print(sum('campaignId' in x for x in e), len(e))"` | The two numbers: how many events carry `campaignId`, out of how many. Then `rm ~/c7.json`. |
| C8 | `... activity <email> --days 30` | Does the detail column show score changes as `+N (old→new) reason` and field changes as `old→new`? Pass or fail only. |
| C9 | `... activity <email> --days 30 --verbose 2>&1 >/dev/null \| grep note:` | Any `note:` lines: they name default types or profile fields that this instance lacks. |
| C10 | `... activity <email> --days 7 --web --json --out ~/c10.json`, then `python3 -c "import json;e=json.load(open('$HOME/c10.json'))['events'];print(sum('?' in x['primary'] for x in e if x['type'] in ('Visit Webpage','Click Link')))"` | One number: how many web-visit or link-click primaries carry a query string (`?`). Above 0 means mkto must strip them (tracking tokens can identify the lead). Then `rm ~/c10.json`. |
| C11 | `... activity <email> --days 7 --all-programs --json --out ~/c11.json 2>&1 \| grep -i token`, then `python3 -c "import json;p=json.load(open('$HOME/c11.json'))['programs'][:25];print(len(p),sum('title' in x for x in p),sum('link' in x for x in p))"` | Program tokens (`GET /rest/asset/v1/folder/{id}/tokens.json?folderType=Program`; `folderType=Program` and the `my.` prefix are UNVERIFIED). Report the three numbers (programs, with a title, with a link; names and tokens cover the first 25 programs only) and any `could not read program tokens` warning, copied as is. A warning with an error code means `folderType=Program` is wrong. No warning but 0 titles, when you know one of those programs sets `{{my.Name}}`, means the token names differ from `name_tokens`. mkto matches names with or without `my.`, so the prefix itself can't break it. Then `rm ~/c11.json`. |
| C12 | **Passed 2026-11-09** on a real MQL (Unqualified→MQL→SQL→Contact all shown, with reasons and campaigns). Re-run after changes: `./mkto --config ~/snap/mkto/common/config.toml why <email>` on a lead whose status changed in the last 90 days. | Pass/fail: the status steps match the lead's status changes in Marketo's activity log (same count, same old→new). Since T43 they come from Change Data Value rows, not `leadchanges.json`. |
| C13 | `... lead <email> --lists` (add `--lead-id <id>` if the lead has duplicates) | Static lists (`GET /rest/v1/leads/{id}/listMembership.json`; the `createdAt` format is UNVERIFIED). Report the count in `Static lists (<n>)` and how many rows show `-` under ADDED. All `-` while the lead is on lists means mkto can't parse the date. |
| C14 | Pick an account with between 300 and 3000 leads. `... account <sfdcAccountId> --json --out ~/c14.json --verbose 2>&1 \| grep -c 'GET /rest/v1/leads.json'`, then `python3 -c "import json;print(json.load(open('$HOME/c14.json'))['total'])"` | Paging for filterType queries (`GET /rest/v1/leads.json?filterType=sfdcAccountId`, UNVERIFIED). Report both numbers and the account's lead count in Marketo or Salesforce. Pass when the totals match and the leads.json count is 2 or more (each profile field C9 found missing adds one call). A total of exactly 300 means mkto didn't get a second page. Any `paging did not advance` error: copy it. About 20 calls. Then `rm ~/c14.json`. |
| C15 | Only if C7 found `campaignId`. `... activity <email> --days 30 2>&1 >/dev/null \| grep -i campaign` | Smart campaign names (`GET /rest/asset/v1/smartCampaign/{id}.json`, VERIFIED in the docs, not yet seen live). Report any `could not look up <n> of <m> campaign names` warning, copied as is. Pass/fail: in the timeline (on screen, don't paste it), score and status rows end with `via "<name>"`, not `via campaign <id>`. |
| C16 | `... activity <email> --days 90 --types "Change Score,Change Data Value" --all-changes` | Labels or REST names: mkto assumes these rows name the field by label, so `change_fields` and `[why]` take labels. Report whether the third column shows labels (e.g. `Lead Status`, as in the LABEL column of `mkto fields`) or REST names (`leadStatus`). Field names aren't lead data. |
| C17 (optional) | A lead whose score changed more than 90 days ago: `(umask 077; ./mkto --config ~/snap/mkto/common/config.toml why <email> --days 365 --json > ~/c17.json)`, then `python3 -c "import json;s=json.load(open('$HOME/c17.json'))['steps'];print(min(x['date'] for x in s) if s else 'none')"` | Retention of Change Score and Change Data Value (believed about 90 days, UNVERIFIED). Report the oldest date only. Then `rm ~/c17.json`. |

The answers go into `internal/story/detail.go` (attribute names), `DefaultProfileFields` and `cardFields`, decide T31 (campaign names need `campaignId`), and confirm or correct the UNVERIFIED tags in `docs/SPEC.md` §Endpoints (C11–C17). C10 decides whether `activity` and `why` must strip query strings from web primaries.

## Uninstall

```bash
sudo snap remove --purge mkto     # also deletes ~/snap/mkto, including config.toml
```

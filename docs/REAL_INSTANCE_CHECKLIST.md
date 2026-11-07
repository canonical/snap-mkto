# Real-instance checklist

Commands for Erekle to run by hand. Claude can't run them: they need `sudo`, an installed snap, or real Marketo credentials.

**Never paste output that contains lead data (emails, names, companies, activity) into the repo, an issue or a chat with Claude.** Report only pass/fail per step and the text of any error message. Error messages from mkto never contain secrets; check before pasting anyway.

Run everything from the repo root, after `snapcraft pack` has produced `mkto_0.1.0_amd64.snap`.

## Part A: install and confinement (no Marketo needed)

| # | Command | Pass when |
|---|---|---|
| A1 | `sudo snap install --dangerous ./mkto_0.1.0_amd64.snap` | `mkto 0.1.0 installed` |
| A2 | `snap connections mkto` | `network` and `home` are both listed with a slot (`:network`, `:home`). If `home` shows `-`, run `sudo snap connect mkto:home` and note it. |
| A3 | `mkto version` | `mkto 0.1.0`, `commit:` a short hash (or `unknown`), `snap: mkto (x1)` |
| A4 | `mkto init` | Prints `Wrote /home/<you>/snap/mkto/common/config.toml` |
| A5 | `stat -c '%a %n' ~/snap/mkto/common/config.toml ~/snap/mkto/common` | File is `600`. Note the dir mode too: snapd creates `common/` and mkto leaves its mode alone. |
| A6 | `mkto init` (again) | Refuses to overwrite and mentions `--force`; exit code 1 (`echo $?`) |
| A7 | `sed -i 's/^timezone = .*/timezone = "Mars\/Base"/' ~/snap/mkto/common/config.toml && MKTO_BASE_URL=https://example.invalid MKTO_CLIENT_ID=x MKTO_CLIENT_SECRET=y mkto quota; echo $?` | `unknown timezone "Mars/Base"`, exit 1. The fake credentials only get past the "key not set" check; `example.invalid` never resolves, so nothing reaches Marketo. Inline values are fine here only because they are fake; never type the real secret inline (shell history). |
| A8 | `sed -i 's/^timezone = .*/timezone = "Asia\/Tbilisi"/' ~/snap/mkto/common/config.toml && MKTO_BASE_URL=https://example.invalid MKTO_CLIENT_ID=x MKTO_CLIENT_SECRET=y mkto quota; echo $?` | No timezone error; it fails with `GET /identity/oauth/token failed: dial tcp: lookup example.invalid ... no such host`, exit 1. This proves the IANA zone loads and DNS works under confinement (`network` plug). A `permission denied` here instead means a confinement problem: report it. |
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

5. **Activity type names and ids** (`GET /rest/v1/activities/types.json`). The fixture's names and ids (other than id 2) are from memory, and mkto resolves `activity_types` by name. On B4 (add `--verbose` if needed), report any `warning: unknown activity type "<name>" skipped` line, and report `No activity in the last 30 days.` for a lead you know was active. Either one means a default name in `activity_types` doesn't match your instance.
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

## Uninstall

```bash
sudo snap remove --purge mkto     # also deletes ~/snap/mkto, including config.toml
```

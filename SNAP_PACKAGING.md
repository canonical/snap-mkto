# mkto snap packaging guide

How to build, install, run and publish mkto as a strictly confined snap.

> **Status (2026-11-09):** 0.2.0 is in the Snap Store on `latest/edge`: revision 1 (amd64) and revision 2 (arm64), both built by CI run 42527292021 from commit `fc59594`. Verified on amd64: the CI-built snap installed with `--dangerous` and then the store revision via `snap refresh --amend --edge`, `mkto doctor` all ✓, and `activity` and `why` against a real instance under strict confinement, with no AppArmor denials. Not yet verified: the arm64 snap on an arm64 machine. Not yet done: promotion to `beta` and `stable`.

## Prerequisites

```bash
sudo snap install snapcraft --classic   # verified with snapcraft 9.2.1
sudo snap install lxd                   # verified with LXD 5.21.9
lxd init --auto
sudo usermod -aG lxd "$USER" && newgrp lxd
```

The base, `core26` (Ubuntu 26.04 LTS), is on `latest/stable` in the store for amd64 and arm64 (and armhf, ppc64el, riscv64, s390x), and needs snapcraft 9.x.

If Docker is installed next to LXD, the build container may have no network: Docker sets the `FORWARD` policy to `DROP`. Allow the LXD bridge in Docker's chain, as the LXD docs describe ("Prevent connectivity issues with LXD and Docker", https://canonical.com/lxd/docs/en/latest/howto/network_bridge_firewalld/). The rules don't survive a reboot:

```bash
sudo iptables  -I DOCKER-USER -i lxdbr0 -j ACCEPT
sudo ip6tables -I DOCKER-USER -i lxdbr0 -j ACCEPT
sudo iptables  -I DOCKER-USER -o lxdbr0 -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT
sudo ip6tables -I DOCKER-USER -o lxdbr0 -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT
```

## snapcraft.yaml

`snap/snapcraft.yaml`:

```yaml
name: mkto
title: mkto
base: core26
version: '0.2.0'
summary: Read-only command-line client for the Marketo Engage REST API
description: |
  Look up a Marketo lead and see its score, status, owner and recent
  activity, with score and status changes shown old to new. mkto why
  explains an MQL or DQ, mkto account lists everyone at an account, and
  mkto leads checks a list of emails. mkto doctor checks the setup.
  mkto brief (optional, off by default) writes an LLM lead brief from
  redacted activity data. mkto never writes to Marketo.
grade: stable
confinement: strict
license: Apache-2.0
source-code: https://github.com/canonical/snap-mkto
issues: https://github.com/canonical/snap-mkto/issues

# Native builds only: each snap is built on its own architecture (CI uses an
# arm64 runner for arm64).
platforms:
  amd64:
  arm64:

parts:
  mkto:
    plugin: go
    source: .
    build-snaps:
      - go@1.27/stable
    build-packages:
      - git
    # The go plugin has no ldflags option, so build the one binary ourselves
    # (static: no cgo, nothing to link from the base).
    override-build: |
      COMMIT=$(git -c safe.directory='*' -C "$CRAFT_PART_SRC" rev-parse --short HEAD 2>/dev/null || echo unknown)
      CGO_ENABLED=0 go build -trimpath -buildvcs=false \
        -ldflags "-s -w -X main.version=$SNAPCRAFT_PROJECT_VERSION -X main.commit=$COMMIT" \
        -o "$CRAFT_PART_INSTALL/bin/mkto" ./cmd/mkto
    # Only the binary ships; nothing else from the project dir can reach the snap.
    prime:
      - bin/mkto

apps:
  mkto:
    command: bin/mkto
    plugs:
      - network
      - home
```

Why it looks like this:

- **`grade: stable`.** The yaml reference: "The grade must be stable to publish the snap to the candidate or stable channels on the snap store. If the grade is devel, it can only be published to the beta and edge channels." A stable grade also needs a stable base and build-base: `core26` is stable, and `build-base` is unset. The grade doesn't force a channel; 0.2.0 still goes to `edge` first (see [Publishing](#publishing-erekle)).
- **`platforms: {amd64, arm64}`.** On core24 and newer bases, `platforms` replaces `architectures`. When a platform name is a valid architecture, `build-on` and `build-for` both default to that name. So each entry is a native build: building on amd64 gives `mkto_0.2.0_amd64.snap`, building on arm64 gives `mkto_0.2.0_arm64.snap`, and snapcraft refuses to build on any other architecture. There is no cross-build: CI builds arm64 on an arm64 runner.
- **`build-snaps: go@1.27/stable`** matches `go 1.27.3` in `go.mod`; the store has `1.27/stable` at 1.27.3 for amd64 and arm64. The part-level reference marks the older `go/1.27/stable` form as deprecated in favour of `@`; snapcraft 9.2.1 resolves the `@` form (`snap list go` in the build instance: `1.27.3 … 1.27/stable`). The go plugin doesn't install Go itself; it expects `go` from a build snap, a build package or a part named `go-deps`.
- **`override-build` instead of the plugin's default.** The go plugin runs `go mod download all`, then `go install ./...`, and only accepts `go-buildtags` and `go-generate`: there is no ldflags option. The override builds one binary and stamps it:
  - `version` comes from `$SNAPCRAFT_PROJECT_VERSION`, the project's `version:` key, so it can't drift from the yaml.
  - `commit` comes from `git rev-parse` on the pulled source (`git` is a build package). `safe.directory='*'` stops git refusing the copied repo over ownership; if git still fails, the commit is `unknown` and the build carries on.
  - `CGO_ENABLED=0` gives a static binary that links nothing from the base. `-buildvcs=false` because the commit is stamped explicitly. `-s -w -trimpath` keep it small and free of build paths.
- **`prime: [bin/mkto]`.** `source: .` copies the whole project directory into the build, gitignored files included. The prime filter guarantees that only the binary ships. This was verified with canaries: a `config.toml` and `.env` in the project dir reached `parts/mkto/src` in the build container but not the snap.
- **Plugs `network` and `home` only.** mkto opens no ports, so it has no `network-bind`.
- **`title: mkto`** is the store display name; without it the metadata linter warns. The linter still warns that `contact` is missing (and notes that `website` is unset). Both are optional. Set them only if you have a real contact address or project site, for example `contact: https://github.com/canonical/snap-mkto/issues`.
- Keep `version:` equal to the git tag (`v0.2.0` ↔ `'0.2.0'`).

## Build

```bash
snapcraft pack        # or: make snap
```

Output from the 0.2.0 build on amd64 (trimmed; under a minute with a warm LXD instance, about 4.5 min cold):

```
Launching managed ubuntu 26.04 instance...
Installing build-packages
Installing build-snaps
Updating sources for mkto (source changed)
Rebuilding mkto ('override-build' property changed)
:: ++ git -c 'safe.directory=*' -C /root/parts/mkto/src rev-parse --short HEAD
:: + COMMIT=c6ab674
:: + CGO_ENABLED=0
:: + go build -trimpath -buildvcs=false -ldflags '-s -w -X main.version=0.2.0 -X main.commit=c6ab674' -o /root/parts/mkto/install/bin/mkto ./cmd/mkto
Staging mkto
Priming mkto
Packing...
Running linters...
Lint warnings:
- metadata: Metadata field 'contact' is missing or empty. (…#contact)
Lint information:
- metadata: Metadata field 'website' is missing or empty. (…#website)
Creating snap package...
Packed mkto_0.2.0_amd64.snap
```

The commit is whatever `HEAD` was at build time; uncommitted changes are built but not flagged. Build from the tagged commit before uploading.

The arm64 snap isn't built locally: `platforms.arm64` builds only on arm64. CI builds it (see [CI](#ci)). `GOARCH=arm64 CGO_ENABLED=0 go build ./cmd/mkto` on amd64 gives a static aarch64 ELF, which shows the code itself builds for arm64.

### What's in the snap

```console
$ unsquashfs -ll mkto_0.2.0_amd64.snap
drwxr-xr-x root/root                38 2026-11-08 19:26 squashfs-root
drwxr-xr-x root/root                27 2026-11-08 19:26 squashfs-root/bin
-rwxr-xr-x root/root          13942944 2026-11-08 19:26 squashfs-root/bin/mkto
drwxr-xr-x root/root                43 2026-11-08 00:09 squashfs-root/meta
drwxr-xr-x root/root                 3 2026-11-08 00:09 squashfs-root/meta/gui
-rw-r--r-- root/root               825 2026-11-08 19:26 squashfs-root/meta/snap.yaml
```

The snap is 3,940,352 bytes. The binary is 13.9 MB uncompressed, statically linked and stripped. The snap holds no `config.toml`, `.env`, `testdata/` or source files. (`meta/gui` is an empty directory snapcraft always creates.)

Most of the binary's size comes from `mkto brief`: anthropic-sdk-go/v2 and its dependencies (OpenTelemetry, gjson/sjson, jsonschema, yaml/v4, x/sync). The OpenTelemetry code is inert: mkto sets no tracer provider or exporter, so nothing is collected or sent.

The generated `meta/snap.yaml` carries `title: mkto`, `version: 0.2.0`, `base: core26`, `confinement: strict`, `grade: stable`, `architectures: [amd64]`, `license: Apache-2.0`, the two plugs, and the `source-code` and `issues` URLs under `links:`.

CI builds through `snapcore/action-build`, whose `build-info` input defaults to `true`. That adds `snap/manifest.yaml` and `snap/snapcraft.yaml` to the CI snap, so the store can match `stage-packages` against security notices. mkto has no stage packages, and the yaml holds no secrets.

### Smoke test without installing (unconfined)

This runs the binary outside the sandbox, so it proves the build and the ldflags, not confinement:

```console
$ unsquashfs -d squashfs-root mkto_0.2.0_amd64.snap && ./squashfs-root/bin/mkto version
mkto 0.2.0
commit: c6ab674
go: go1.27.3
snap: no
$ SNAP_NAME=mkto SNAP_REVISION=x1 ./squashfs-root/bin/mkto version --json
{
  "version": "0.2.0",
  "commit": "c6ab674",
  "go": "go1.27.3",
  "snap": "mkto (x1)"
}
```

## Interfaces

| Plug | Why | Connection |
|---|---|---|
| `network` | Marketo REST API, and the LLM API for `mkto brief` (off by default) | Auto-connects |
| `home` | `--out` files and `mkto leads --from <file>` in your home folder. The config does **not** need it. | Auto-connects on classic Ubuntu; on Ubuntu Core run `sudo snap connect mkto:home` (verified for 0.1.0 on 2026-11-08: `snap connections mkto` shows both connected) |

What `home` allows comes from the AppArmor rules snapd 2.78 generates for it. These lines are copied verbatim from the generated profile `/var/lib/snapd/apparmor/profiles/snap.mkto.mkto` on the build machine:

```
# Description: Can access non-hidden files in user's $HOME. This is restricted
# because it gives file access to all of the user's $HOME.

# Allow read access to toplevel $HOME for the user
owner @{HOME}/ r,

# Allow read/write access to all files in @{HOME}, except snap application
# data in @{HOME}/snap and toplevel hidden directories in @{HOME}.
owner @{HOME}/[^s.]**             rwklix,
owner @{HOME}/s[^n]**             rwklix,
owner @{HOME}/sn[^a]**            rwklix,
owner @{HOME}/sna[^p]**           rwklix,
owner @{HOME}/snap[^/]**          rwklix,
…
# Disallow writes to the well-known directory included in
# the user's PATH on several distributions
audit deny @{HOME}/bin/{,**} wl,
audit deny @{HOME}/bin wl,
```

In short (a paraphrase), mkto can read and write anywhere under your home folder except:

- `~/snap/`
- names starting with a dot at the top level
- `~/bin/`, which it can read but not write

So `--out ~/report.json` and `--out ~/Documents/report.json` work. `--out ~/.report.json` and anything under `~/.config/` are denied silently: snapd uses a quiet `deny` rule, so nothing reaches the kernel log. `--out ~/bin/x` is denied with an audit entry in the kernel log. Inside the snap, mkto adds the reason to the error: `…: permission denied; the mkto snap can write only to non-hidden paths in your home folder (home plug), e.g. ~/report.json`.

`--out <file> --force` over an existing file first writes a temp file `mkto-out-<random>.tmp` in the same directory as the target, then renames it over the target. In `$HOME` that is `~/mkto-out-*.tmp`, which `owner @{HOME}/[^s.]**` covers (the name never starts with a dot). A failed run removes the temp file and leaves the target as it was. This was traced unconfined with `strace`: `openat("…/mkto-out-390227908.tmp", O_RDWR|O_CREAT|O_EXCL, 0600)`, then `unlinkat` of that file after a DNS failure, with the old target unchanged.

## Where files live under strict confinement

- Inside the snap, `HOME` is `$SNAP_USER_DATA` → `~/snap/mkto/<revision>/` (with a `current` symlink). snapd copies this directory on every refresh.
- mkto keeps its config in `$SNAP_USER_COMMON` instead → **`~/snap/mkto/common/config.toml`**, so credentials exist once and survive refreshes and reverts.
- **`snap remove` deletes the config.** Removing the snap deletes `~/snap/mkto/` including `common/config.toml`; only refreshes and reverts keep it. snapd saves an automatic snapshot on remove: `snap saved mkto` lists it, `sudo snap restore <set> mkto` brings the config back, and `sudo snap forget <set>` deletes the snapshot (it holds the client secret). To upgrade or switch to the store build, use `sudo snap install --dangerous <file>` over the old version or `sudo snap refresh mkto --amend`, never remove and reinstall (2026-11-09: a remove lost the config; the snapshot restored it).
- `mkto brief` keeps its files in the same directory, and they need no new plugs: the LLM calls use the existing `network` plug.
  - `playbook.md`: your private offerings, never built into the snap.
  - `prompts/`: optional prompt overrides.
  - `briefs/`: written by `--save`. The directory is created 0700 and each brief is 0600, never overwritten. mkto refuses to save through a symlinked `briefs/`.
- `mkto init` writes the config file with mode **0600**, but doesn't change the mode of an existing directory.
  - snapd creates `~/snap/mkto/common/` before mkto runs, so that directory keeps whatever mode snapd gave it.
  - mkto sets 0700 only on a directory it creates itself (for example `~/.config/mkto/` from source). It leaves existing directories alone because `--config` may point into a directory the user chose.
  - Other local users may see that `config.toml` exists, but the 0600 file mode keeps them from reading it.
- Run from source, the config is at `~/.config/mkto/config.toml`.
- `/tmp` inside the snap is private to the snap: `--out /tmp/x` lands in `/tmp/snap-private-tmp/snap.mkto/tmp/` on the host (root-only), not in the host's `/tmp`. This was verified on 2026-11-08: the file is mode 600, and listing that directory needs `sudo`. Because that surprises people, mkto prints a note after writing there: `note: /tmp is private to the mkto snap; the file is at /tmp/snap-private-tmp/snap.mkto/tmp/x (readable with sudo)`.
- Timezones: the binary embeds the IANA database (`time/tzdata`), so `timezone = "Asia/Tbilisi"` works whatever zoneinfo the base ships. An unknown name fails the config check: `✗ config: config key output.timezone in …: unknown timezone "Mars/Olympus": use "Local" or an IANA name such as "Asia/Tbilisi"`.

## Install and run

```bash
sudo snap install --dangerous ./mkto_0.2.0_amd64.snap     # or _arm64.snap on arm64

mkto init                      # writes ~/snap/mkto/common/config.toml (mode 0600)
nano ~/snap/mkto/common/config.toml

mkto doctor
mkto quota
mkto lead someone@example.com
mkto leads --from ~/emails.txt
mkto activity someone@example.com --days 30 --out ~/activity.json
mkto programs --name webinar
mkto types --search email
mkto fields --search score
```

`--dangerous` is needed because a local build isn't signed by the store. Once the snap is published, `sudo snap install mkto` (or `--edge`, `--beta`) replaces it. The full install and real-instance test plan is `docs/REAL_INSTANCE_CHECKLIST.md`.

## CI

The `snap` job in `.github/workflows/ci.yml` runs after `test`, as a two-entry matrix:

| `arch` | `runs-on` | Artifact |
|---|---|---|
| amd64 | `ubuntu-latest` (Ubuntu 24.04) | `mkto-snap-amd64` |
| arm64 | `ubuntu-24.04-arm` | `mkto-snap-arm64` |

- **The arm64 runner.** GitHub lists `ubuntu-24.04-arm` (also `ubuntu-22.04-arm` and `ubuntu-26.04-arm`) as a standard hosted runner, free and unlimited on public repositories. Private repositories use the account's included minutes. Each runner builds its own native platform from `platforms:`, so no cross-building or emulation is involved.
- **The build action.** `snapcore/action-build` is pinned to the v1.3.0 commit SHA (`3bdaa03…`, still the newest tag on 2026-11-08), like every action in the workflow.
  - It installs LXD and snapcraft from `latest/stable` (9.x, which supports core26), runs `snapcraft` in the repo root and outputs the path of the `.snap`.
  - Its source has no architecture-specific logic: it installs snapd via apt, LXD and snapcraft via snap, and sets `SNAPCRAFT_BUILD_ENVIRONMENT=lxd`.
- **Artifact names.** Each artifact is named per architecture, because names must be unique within a run.
- **No credentials.** The workflow has no secrets and no store login or upload step. Publishing is manual.
- **Why not `snapcraft remote-build`?** It builds on Launchpad, uploads the source to a public Launchpad project unless you pass `--project`, needs a Launchpad login, and is marked experimental. In CI it would need credentials. Erekle can still run it locally as an alternative way to get the arm64 snap:

  ```bash
  snapcraft remote-build --build-for=arm64 --launchpad-accept-public-upload
  ```

  This builds the tree as committed: Launchpad gets a git push of the repo.

The arm64 job hasn't run yet: check the first run in the repo's Actions tab.

## Confinement re-test for 0.2.0 (Erekle)

These steps need `sudo`, so they haven't been run by Claude. Run them from the repo root after `snapcraft pack`.

Part 1 needs no Marketo instance: it uses a throwaway config and a fake host.

```bash
sudo snap install --dangerous ./mkto_0.2.0_amd64.snap
snap list mkto                       # 0.2.0, rev x…
snap connections mkto                # home and network: connected
mkto version                         # mkto 0.2.0 / commit: c6ab674 (or later) / snap: mkto (x…)

# init writes mode 0600; IANA timezone loads under confinement
mkto init --config ~/snap/mkto/common/t39.toml
stat -c '%a %n' ~/snap/mkto/common/t39.toml        # 600
sed -i 's|^timezone = "Local"|timezone = "Asia/Tbilisi"|' ~/snap/mkto/common/t39.toml
( export MKTO_BASE_URL=https://000-AAA-000.mktorest.example MKTO_CLIENT_ID=fake MKTO_CLIENT_SECRET=fake
  mkto doctor --config ~/snap/mkto/common/t39.toml
  #   ✓ config: …/t39.toml
  #   ✗ credentials: … lookup 000-AAA-000.mktorest.example …: no such host  (DNS works under confinement)

  # --out --force over an existing file: the ~/mkto-out-*.tmp temp file must be allowed
  echo old > ~/t39-x.json
  mkto activity someone@example.com --config ~/snap/mkto/common/t39.toml --out ~/t39-x.json --force
  #   expect the same DNS error, NOT "permission denied; the mkto snap can write only …"
  cat ~/t39-x.json; ls ~/mkto-out-*.tmp               # "old"; no leftover temp file

  # leads --from reads a file in $HOME
  printf 'someone@example.com\n' > ~/t39-emails.txt
  mkto leads --from ~/t39-emails.txt --config ~/snap/mkto/common/t39.toml
  #   expect the DNS error, NOT "open …: permission denied"
)
rm ~/snap/mkto/common/t39.toml ~/t39-x.json ~/t39-emails.txt
```

Part 2 runs against the real instance, with your own `~/snap/mkto/common/config.toml`:

```bash
mkto doctor                          # ✓ config, then ✓ credentials: access token fetched
mkto types --search email
mkto fields --search score
printf '%s\n' <real-email-1> <real-email-2> > ~/emails.txt
mkto leads --from ~/emails.txt
mkto activity <real-email> --out ~/x.json             # first write
mkto activity <real-email> --out ~/x.json --force     # over the existing file
stat -c '%a %n' ~/x.json; ls ~/mkto-out-*.tmp; python3 -m json.tool ~/x.json >/dev/null && echo valid
mkto activity <real-email> --out /tmp/t39.txt         # prints the snap-private /tmp note
sudo ls -l /tmp/snap-private-tmp/snap.mkto/tmp/t39.txt
rm ~/x.json ~/emails.txt; sudo rm /tmp/snap-private-tmp/snap.mkto/tmp/t39.txt

journalctl -k --since "-30 min" | grep 'apparmor="DENIED"' | grep 'profile="snap.mkto'   # expect nothing
```

If anything is denied, also run `sudo snap install snappy-debug && sudo snappy-debug -r` in a second terminal while you repeat the failing command; it explains each denial and suggests the interface.

- [ ] Part 1: install, connections, `version`, `init` mode 600, IANA timezone, DNS, `--out --force` temp file, `leads --from` read
- [ ] Part 2: `doctor` ✓✓, `types`, `fields`, `leads --from`, `--out` and `--out --force` (mode 600, valid JSON, no leftover temp), `/tmp` note
- [ ] `journalctl` shows no `snap.mkto` denials
- [ ] arm64: the CI artifact `mkto-snap-arm64` installs and passes Part 1 on an arm64 machine (or record that none was available)

## Publishing (Erekle)

Claude never logs in, registers, uploads or releases. Run these yourself, from the tagged commit, after the re-test above passes.

1. **Log in** with your Ubuntu One account. It prompts for email, password and 2FA, and stores the credentials in the system keyring when one is available.
   ```bash
   snapcraft login
   snapcraft whoami
   ```
2. **Register the name.** It's universally unique and can't be changed later. Registration is public by default; add `--private` to hide the snap from search. Decide first whether `mkto` should be registered by your personal account or by a Canonical publisher account.
   ```bash
   snapcraft register mkto
   ```
3. **Upload each architecture and release to edge.** `upload` blocks until the store's automated review finishes. With `--release` it shows the channel map afterwards.
   ```bash
   snapcraft upload --release=edge mkto_0.2.0_amd64.snap
   snapcraft upload --release=edge mkto_0.2.0_arm64.snap   # from the CI artifact mkto-snap-arm64
   snapcraft status mkto
   ```
4. **Try it from the store** on a clean machine, if you can, and repeat the [re-test](#confinement-re-test-for-020-erekle) against the store build:
   ```bash
   sudo snap refresh mkto --amend --edge   # switch a local (--dangerous) install to the store build; keeps ~/snap/mkto/common
   ```
5. **Promote edge → beta → stable.** `promote` moves the whole set of revisions in a channel, so both architectures move together. It asks for confirmation; `--yes` skips the prompt.
   ```bash
   snapcraft promote mkto --from-channel edge --to-channel beta
   snapcraft promote mkto --from-channel beta --to-channel stable
   ```
   Or release one revision at a time, with the revision numbers from `snapcraft revisions mkto`:
   ```bash
   snapcraft release mkto <revision> beta
   snapcraft release mkto <revision> stable
   ```
   `stable` and `candidate` accept only `grade: stable` snaps, which 0.2.0 is. Channels are `[<track>/]<risk>[/<branch>]`, and the track defaults to `latest`.

## Verified confinement run (0.1.0, 2026-11-08)

Erekle ran this on Ubuntu with snap revision x4 (and earlier x1), against a real Marketo instance, following Part A and B5 of `docs/REAL_INSTANCE_CHECKLIST.md`.

- [x] `sudo snap install --dangerous` succeeds; `snap connections mkto` shows `network` and `home` connected.
- [x] `mkto version` reports `snap: mkto (x…)` and the build commit.
- [x] `mkto init` writes `~/snap/mkto/common/config.toml` with mode 600.
- [x] DNS and HTTPS work under confinement: `quota`, `lead`, `activity`, `programs` and `brief --show-payload` all ran against the real instance.
- [x] `--out ~/b5.json` works (mode 600, valid JSON); `--out /tmp/b5.txt` lands in `/tmp/snap-private-tmp/snap.mkto/tmp/`; `--out ~/.b5.txt` fails with `permission denied`.
- [x] `journalctl -k | grep 'apparmor="DENIED"' | grep 'profile="snap.mkto'` prints nothing, not even for the hidden-file case (snapd's deny rule for it is quiet). Unrelated `snap-confine` `file_inherit` denials can appear when another tool captures a snap's output into a file under its own `/tmp` dir; they don't involve mkto's profile.

## Documentation used

Checked on 2026-11-08. The `documentation.ubuntu.com/snapcraft/stable/` URLs redirect to `ubuntu.com/docs/snapcraft/9/`, which documents snapcraft 9.2.

- snapcraft.yaml reference (`title`, `version`, `grade`, `confinement`, `platforms`, `architectures`, `build-snaps`, `override-build`, `prime`, `apps.*.plugs`): https://documentation.ubuntu.com/snapcraft/stable/reference/project-file/snapcraft-yaml/
- Go plugin: https://documentation.ubuntu.com/snapcraft/stable/common/craft-parts/reference/plugins/go_plugin/
- Select platforms: https://documentation.ubuntu.com/snapcraft/stable/how-to/crafting/select-platforms/
- Change from core24 to core26: https://documentation.ubuntu.com/snapcraft/stable/how-to/change-bases/change-from-core24-to-core26/
- Remote build: https://documentation.ubuntu.com/snapcraft/stable/explanation/remote-build/
- Channels: https://documentation.ubuntu.com/snapcraft/stable/reference/channels/
- Publishing how-tos: https://documentation.ubuntu.com/snapcraft/stable/how-to/publishing/authenticate/, …/register-a-snap/, …/publish-a-snap/, …/manage-revisions-and-releases/
- Command reference: https://documentation.ubuntu.com/snapcraft/stable/reference/commands/upload/, …/release/, …/promote/, …/register/
- snapd `home` interface: https://snapcraft.io/docs/home-interface; the rules quoted above come from the profile snapd generated on the build machine.
- GitHub-hosted runners (arm64 labels): https://docs.github.com/en/actions/reference/runners/github-hosted-runners
- snapcore/action-build (inputs, source): https://github.com/snapcore/action-build
- Store channel maps for `go` and `core26`: `https://api.snapcraft.io/v2/snaps/info/<name>` (header `Snap-Device-Series: 16`)

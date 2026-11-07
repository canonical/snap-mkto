# mkto snap packaging guide

How to build, install and run mkto as a strictly confined snap.

> **Status (2026-11-08):** `snap/snapcraft.yaml` is final for v0.1.0. `snapcraft pack` is verified (LXD build, file layout checked with `unsquashfs`), and install plus behaviour under strict confinement are verified on Ubuntu with a real Marketo instance; see [Verified confinement run](#verified-confinement-run-2026-11-08).

## Prerequisites

```bash
sudo snap install snapcraft --classic   # verified with snapcraft 9.2.1
sudo snap install lxd                   # verified with LXD 5.21.9
lxd init --auto
sudo usermod -aG lxd "$USER" && newgrp lxd
```

The base, `core26` (Ubuntu 26.04 LTS), is on `latest/stable` in the store and needs snapcraft 9.x.

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
base: core26
version: '0.1.0'
summary: Read-only command-line client for the Marketo Engage REST API
description: |
  Look up a Marketo lead by email, see its recent activity and program
  memberships, list programs, and check daily API quota usage.
  mkto brief (optional, off by default) writes an LLM lead brief from
  redacted activity data. mkto never writes to Marketo.
grade: devel
confinement: strict
license: Apache-2.0
source-code: https://github.com/canonical/snap-mkto
issues: https://github.com/canonical/snap-mkto/issues

parts:
  mkto:
    plugin: go
    source: .
    build-snaps:
      - go/1.27/stable
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

- **No `platforms` key.** It's optional on core24 and newer; without it snapcraft builds for the host architecture (`architectures: [amd64]` in the generated `meta/snap.yaml`).
- **`build-snaps: go/1.27/stable`** matches `go 1.27.3` in `go.mod`. The go plugin doesn't install Go itself; it expects `go` from a build snap, a build package or a part named `go-deps`.
- **`override-build` instead of the plugin's default.** The go plugin runs `go mod download all`, then `go install ./...`, and only accepts `go-buildtags` and `go-generate`: there is no ldflags option. The override builds one binary and stamps it:
  - `version` comes from `$SNAPCRAFT_PROJECT_VERSION`, the project's `version:` key, so it can't drift from the yaml.
  - `commit` comes from `git rev-parse` on the pulled source (`git` is a build package). `safe.directory='*'` stops git refusing the copied repo over ownership; if git still fails, the commit is `unknown` and the build carries on.
  - `CGO_ENABLED=0` gives a static binary that links nothing from the base. `-buildvcs=false` because the commit is stamped explicitly. `-s -w -trimpath` keep it small and free of build paths.
- **`prime: [bin/mkto]`.** `source: .` copies the whole project directory into the build (gitignored files included). The prime filter guarantees that only the binary ships. Verified with canaries: a `config.toml` and `.env` in the project dir reached `parts/mkto/src` in the build container but not the snap.
- **Plugs `network` and `home` only.** mkto opens no ports, so no `network-bind`.
- **`grade: devel`** until the first stable release; `devel` snaps can only go to `edge` or `beta`.
- Keep `version:` equal to the git tag (`v0.1.0` ↔ `'0.1.0'`).

## Build

```bash
snapcraft pack        # or: make snap
```

Output from a clean build (trimmed; 4.5 min with a cold LXD image, under a minute after that):

```
Launching managed ubuntu 26.04 instance...
Installing build-packages
Installing build-snaps
Pulling mkto
Building mkto
:: + COMMIT=aeca2c4
:: + CGO_ENABLED=0
:: + go build -trimpath -buildvcs=false -ldflags '-s -w -X main.version=0.1.0 -X main.commit=aeca2c4' -o /root/parts/mkto/install/bin/mkto ./cmd/mkto
Staging mkto
Priming mkto
Packing...
Running linters...
Creating snap package...
Packed mkto_0.1.0_amd64.snap
```

The commit is whatever `HEAD` was at build time; uncommitted changes are built but not flagged.

### What's in the snap

```console
$ unsquashfs -ll mkto_0.1.0_amd64.snap
drwxr-xr-x root/root                38 2026-11-07 23:19 squashfs-root
drwxr-xr-x root/root                27 2026-11-07 23:19 squashfs-root/bin
-rwxr-xr-x root/root          13476000 2026-11-07 23:19 squashfs-root/bin/mkto
drwxr-xr-x root/root                43 2026-11-07 14:34 squashfs-root/meta
drwxr-xr-x root/root                 3 2026-11-07 14:34 squashfs-root/meta/gui
-rw-r--r-- root/root               812 2026-11-07 23:19 squashfs-root/meta/snap.yaml
```

The snap is about 3.8 MB (3,788,800 bytes); the binary is 13.5 MB uncompressed, statically linked. No `config.toml`, `.env`, `testdata/` or source files. (`meta/gui` is an empty directory snapcraft always creates.)

The binary grew from 8.1 MB to 13.5 MB with `mkto brief`: anthropic-sdk-go/v2 and its dependencies (OpenTelemetry, gjson/sjson, jsonschema, yaml/v4, x/sync) account for the growth. The OpenTelemetry code is inert: mkto sets no tracer provider or exporter, so nothing is collected or sent.

The generated `meta/snap.yaml` carries `base: core26`, `confinement: strict`, `grade: devel`, `license: Apache-2.0`, the two plugs, and the `source-code` and `issues` URLs under `links:`.

### Smoke test without installing (unconfined)

This runs the binary outside the sandbox, so it proves the build and the ldflags, not confinement:

```console
$ unsquashfs -d squashfs-root mkto_0.1.0_amd64.snap && ./squashfs-root/bin/mkto version
mkto 0.1.0
commit: aeca2c4
go: go1.27.3
snap: no
$ SNAP_NAME=mkto SNAP_REVISION=x1 ./squashfs-root/bin/mkto version --json
{
  "version": "0.1.0",
  "commit": "aeca2c4",
  "go": "go1.27.3",
  "snap": "mkto (x1)"
}
```

## Interfaces

| Plug | Why | Connection |
|---|---|---|
| `network` | Marketo REST API, and the LLM API for `mkto brief` (off by default) | Auto-connects |
| `home` | Only for `--out` files in your home folder. The config does **not** need it. | Auto-connects on classic Ubuntu; on Ubuntu Core run `sudo snap connect mkto:home` (verified 2026-11-08: `snap connections mkto` shows both connected) |

What `home` allows comes from the AppArmor rules snapd 2.78 generates for it. These lines are copied verbatim from a generated profile on the build machine (`/var/lib/snapd/apparmor/profiles/`):

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
```

In short (a paraphrase): read/write anywhere under your home folder except `~/snap/` and names starting with a dot at the top level. So `--out ~/report.json` and `--out ~/Documents/report.json` work, and `--out ~/.report.json` or anything under `~/.config/` is denied. The denial is silent in the kernel log (snapd uses a quiet `deny` rule), so inside the snap mkto adds the reason to the error: `…: permission denied; the mkto snap can write only to non-hidden paths in your home folder (home plug), e.g. ~/report.json`.

## Where files live under strict confinement

- Inside the snap, `HOME` is `$SNAP_USER_DATA` → `~/snap/mkto/<revision>/` (with a `current` symlink). snapd copies this directory on every refresh.
- mkto keeps its config in `$SNAP_USER_COMMON` instead → **`~/snap/mkto/common/config.toml`**, so credentials exist once and survive refreshes and reverts. `mkto brief` keeps its files there too: `playbook.md` (your private offerings, never built into the snap), `prompts/` (optional prompt overrides) and `briefs/` (written by `--save`: the directory is created 0700 and each brief is 0600, never overwritten; mkto refuses to save through a symlinked `briefs/`). These need no new plugs: the LLM calls use the existing `network` plug.
- `mkto init` writes the config file with mode **0600**. It doesn't change the mode of an existing directory: snapd creates `~/snap/mkto/common/` before mkto runs, so that directory keeps whatever mode snapd gave it. mkto only sets 0700 on a directory it creates itself (for example `~/.config/mkto/` from source); it leaves existing directories alone because `--config` may point into a directory the user chose. Other local users may see that `config.toml` exists, but the 0600 file mode keeps them from reading it.
- Run from source, the config is at `~/.config/mkto/config.toml`.
- `/tmp` inside the snap is private to the snap: `--out /tmp/x` lands in `/tmp/snap-private-tmp/snap.mkto/tmp/` on the host (root-only), not in the host's `/tmp` (verified 2026-11-08: the file is mode 600, and listing that directory needs `sudo`). Because that surprises people, mkto prints a note after writing there: `note: /tmp is private to the mkto snap; the file is at /tmp/snap-private-tmp/snap.mkto/tmp/x (readable with sudo)`.
- Timezones: the binary embeds the IANA database (`time/tzdata`), so `timezone = "Asia/Tbilisi"` works whatever zoneinfo the base ships.

## Install and run

```bash
sudo snap install --dangerous ./mkto_0.1.0_amd64.snap

mkto init                      # writes ~/snap/mkto/common/config.toml (mode 0600)
nano ~/snap/mkto/common/config.toml

mkto quota
mkto lead someone@example.com
mkto activity someone@example.com --days 30
mkto programs --name webinar
```

`--dangerous` is needed because a local build isn't signed by the store. The full install and real-instance test plan is `docs/REAL_INSTANCE_CHECKLIST.md`.

## CI

The `snap` job in `.github/workflows/ci.yml` runs after `test`. `snapcore/action-build` (pinned to the v1.3.0 commit SHA, like every action in the workflow) installs LXD and snapcraft from `latest/stable` (9.x, which supports core26), runs `snapcraft` in the repo root and outputs the path of the `.snap`, which `actions/upload-artifact` saves as `mkto-snap`. The action has no base-specific logic. Check the first run in the repo's Actions tab.

## Verified confinement run (2026-11-08)

Run by Erekle on Ubuntu, snap revision x4 (and earlier x1), against a real Marketo instance. Steps: Part A and B5 of `docs/REAL_INSTANCE_CHECKLIST.md`.

- [x] `sudo snap install --dangerous` succeeds; `snap connections mkto` shows `network` and `home` connected.
- [x] `mkto version` reports `snap: mkto (x…)` and the build commit.
- [x] `mkto init` writes `~/snap/mkto/common/config.toml` with mode 600.
- [x] DNS and HTTPS work under confinement: `quota`, `lead`, `activity`, `programs` and `brief --show-payload` all ran against the real instance.
- [x] `--out ~/b5.json` works (mode 600, valid JSON); `--out /tmp/b5.txt` lands in `/tmp/snap-private-tmp/snap.mkto/tmp/`; `--out ~/.b5.txt` fails with `permission denied`.
- [x] `journalctl -k | grep 'apparmor="DENIED"' | grep 'profile="snap.mkto'` prints nothing, not even for the hidden-file case (snapd's deny rule for it is quiet). Unrelated `snap-confine` `file_inherit` denials can appear when another tool captures a snap's output into a file under its own `/tmp` dir; they don't involve mkto's profile.

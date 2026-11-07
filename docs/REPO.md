# GitHub repository spec

## Repository

| Setting | Value |
|---|---|
| Name | `snap-mkto` (https://github.com/canonical/snap-mkto). The snap and the binary are still named `mkto`. |
| Owner | The `canonical` org. Follow Canonical's repo setup; settings may be decided for you. |
| Description | Read-only CLI for the Marketo Engage REST API, packaged as a strictly confined Ubuntu snap. |
| Topics | `snap`, `snapcraft`, `ubuntu`, `marketo`, `marketo-api`, `cli`, `golang`, `marketing-automation` |
| Visibility | Public. Nothing secret is ever committed (see `.gitignore` and CLAUDE.md rule 2). |
| Default branch | `main` |
| Licence | Apache-2.0, copyright Canonical Ltd. It's the most common licence in the canonical org (270 of 588 public source repos on 2026-11-07; Go CLIs such as concierge, cos-tool and notary). Confirm with the org owner before the first push. |

When creating the repo on GitHub, leave **README, .gitignore and licence unchecked**: they already exist locally, and an initialised remote makes the first push conflict.

## Settings

- **Features:** Issues on. Wiki, Projects and Discussions off.
- **Pull requests:** squash merging only; automatically delete head branches.
- **Security:** Dependabot alerts, secret scanning and push protection on. These won't recognise Marketo client secrets, so `.gitignore` and the no-secrets rule remain the main defence.
- **Ruleset on `main`** (optional while working solo): require a pull request, require the `test` status check, block force pushes.

## Conventions

- Branches: `phase-1/config`, `fix/token-refresh`, `docs/snap-guide`.
- Commits: Conventional Commits — `feat:`, `fix:`, `docs:`, `test:`, `build:`, `ci:`, `chore:`; optional scope, e.g. `feat(activity): batch type ids`.
- One task from `docs/TASKS.md` per commit series / PR.
- Tags: semver `vX.Y.Z`, matching `version:` in `snap/snapcraft.yaml`.

## CI — `.github/workflows/ci.yml`

Create in Phase 0 with only the `test` job. Add the `snap` job in Phase 1.5, once `snap/snapcraft.yaml` exists.

```yaml
name: CI

on:
  push:
    branches: [main]
  pull_request:

permissions:
  contents: read

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          persist-credentials: false
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod
      - run: go mod tidy -diff
      - run: go vet ./...
      - run: test -z "$(gofmt -l .)"
      - run: go test -race ./...

  # Phase 1.5
  snap:
    needs: test
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          persist-credentials: false
      - uses: snapcore/action-build@3bdaa03e1ba6bf59a65f84a751d943d549a54e79 # v1.3.0
        id: build
      - uses: actions/upload-artifact@cf430e030ddbb5b0abf93d22962f4752f3646cd9 # v7.0.2
        with:
          name: mkto-snap
          path: ${{ steps.build.outputs.snap }}
```

Actions are pinned to full commit SHAs, with the release tag as a trailing comment (resolved on 2026-11-07 with `gh api repos/<owner>/<repo>/git/ref/tags/<tag>`, dereferencing annotated tags with `gh api repos/<owner>/<repo>/git/tags/<sha>`). Dependabot's `github-actions` updates bump the SHA and the comment together. `persist-credentials: false` keeps the job token out of `.git/config`, since no step needs to push.

## Dependabot — `.github/dependabot.yml`

```yaml
version: 2
updates:
  - package-ecosystem: gomod
    directory: /
    schedule:
      interval: weekly
  - package-ecosystem: github-actions
    directory: /
    schedule:
      interval: monthly
```

## snapcraft.yaml metadata

Once the repo URL is known, add to `snap/snapcraft.yaml`:

```yaml
source-code: https://github.com/canonical/snap-mkto
issues: https://github.com/canonical/snap-mkto/issues
```

## Releases

1. Bump `version:` in `snap/snapcraft.yaml`.
2. Tag `vX.Y.Z` and push the tag.
3. Create a GitHub release with notes; attach the `.snap` built by CI.
4. Optional: `snapcraft upload --release=edge mkto_*.snap`. Optionally connect the repo on snapcraft.io so Launchpad builds every push for multiple architectures.

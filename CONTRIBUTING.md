# Contributing to SysPI

## Development setup

- Go 1.25 (`go.mod` pins the toolchain)
- `git`; `mercurial` and `subversion` for the full test suite

```sh
git clone https://github.com/Open-MBEE/SysPI.git
cd SysPI
go build ./...
go test ./...
```

## Workflow

The repository follows git-flow:

- `develop` is the integration branch and the default branch. Every `feat/`, `fix/`, `docs/`,
  `ci/` and `test/` branch is cut from `develop`, and its pull request targets `develop`.
- `main` is the release branch; release tags (`v*`) are created there.

1. Create a branch from `develop`: `git checkout -b feat/my-change origin/develop`.
2. Make focused changes with Conventional Commit messages (`<type>(<scope>): <description>`;
   types `feat`, `fix`, `docs`, `test`, `refactor`, `chore`).
3. Add a changelog fragment: `changes/unreleased/<slug>.<section>.md` with the list item(s) only
   (see `changes/README.md`). Do not edit `CHANGELOG.md`.
4. Run the gate: `go build ./... && go vet ./... && test -z "$(gofmt -l .)" && SYSPI_REQUIRE_VCS=1 go test ./...`.
5. Open a **draft** pull request against `develop`; mark it ready for review once CI is green.
6. Respond to review; a maintainer merges.

## Pull request guidelines

- Include tests for new behaviour; the end-to-end suite in `tests/e2e` is the acceptance gate.
- Update `README.md` / `docs/` when a format, command or contract changes.
- Keep pull requests to one feature or fix.
- Never rebase or force-push a branch that has a pull request; merge `develop` into it instead.

## Releases

1. On a `release/x.y.z` branch from `develop`, run `python3 scripts/changelog.py release x.y.z`:
   it folds `changes/unreleased/` into a dated `## x.y.z` section of `CHANGELOG.md` and deletes
   the fragments. Open the pull request against `develop` and merge it.
2. Merge `develop` into `main` and tag `vx.y.z` on `main`; the tag equals the version, as the
   `.project.json` convention requires of the packages SysPI installs.
3. The release workflow (`.github/workflows/release.yml`) runs the gate on Linux, macOS and
   Windows, builds `syspi-<os>-<arch>.tar.gz` (`.zip` on Windows) for amd64 and arm64 with
   `scripts/build-release-artifacts.sh`, signs `SHA256SUMS.txt` with cosign (keyless; the
   identity is the workflow on the tag), creates the GitHub release with the changelog section
   as its notes, then installs it on all three systems with `install.sh` / `install.ps1`.
   A tag without a `CHANGELOG.md` section publishes nothing.

`scripts/install-test.sh` runs the installers against a fake release served locally; CI runs it
on every pull request. To rehearse a release locally: `scripts/build-release-artifacts.sh v0.0.0-rc dist`.

## Licence

By contributing you agree that your contributions are licensed under the Apache License 2.0.

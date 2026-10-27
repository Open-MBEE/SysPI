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

A maintainer tags `vX.Y.Z` on `main`; the release workflow builds Linux, macOS and Windows
binaries for amd64 and arm64 with a `SHA256SUMS` file and attaches them to the GitHub release.
The `.project.json` convention for packages applies to SysPI's own releases: the tag equals the
version.

## Licence

By contributing you agree that your contributions are licensed under the Apache License 2.0.

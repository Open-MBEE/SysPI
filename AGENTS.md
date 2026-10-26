# AGENTS.md — Guide for AI agents working on SysPI

> Correctness over expedience. Fix root causes, never weaken or skip a test to make it pass.

SysPI is the SysML Package Installer: a Go command-line tool (`cmd/syspi`) that resolves,
verifies, installs and locks KerML / SysML v2 project-interchange packages. It is a separate
project from OpenSysML, which only reads what SysPI installs.

## Golden rules

1. **The package contract is KerML 1.1 §10.3 and nothing else.** `.project.json` at a tag is
   mandatory; never synthesise one, never add a lax mode.
2. **Identities are version-control neutral.** `pkg:<vcs>/<host>/<path>[@<version>][#<subpath>]`
   with `git`, `svn`, `hg`; no provider vocabulary, nothing host- or tool-specific in identities,
   locks or installed trees. Everything VCS-specific lives behind `vcs.Source`.
3. **Verify before reading.** Checksums, revisions, digests, path confinement and size bounds are
   checked before any model content is used or written under `lib/`.
4. **Deterministic outputs.** The lock and `bundle` are byte-reproducible; sort, never rely on
   map order or wall-clock time (`SOURCE_DATE_EPOCH` is honoured).
5. **Standard library only.** The CLI is `flag` with subcommand dispatch; the one third-party
   dependency is the semver range library. Do not add a CLI framework or a purl parser.
6. **Never write `.sysand/lib/` or `sysand-lock.toml`.** They are read for interoperability only.

## Build, test, verify

```sh
go build ./...
go vet ./...
gofmt -l .          # must print nothing
go test ./...
SYSPI_REQUIRE_VCS=1 go test ./...   # CI: a missing hg or svn is a failure, not a skip
```

The end-to-end suite (`tests/e2e`) builds the binary and drives it on local git, hg and svn
fixtures and an `httptest` server; it needs `git`, `hg`, `svn` and `svnadmin` on `PATH`
(`apt-get install mercurial subversion`, `brew install mercurial subversion`). No test touches
the network.

## Layout

```
cmd/syspi/          the command: flag parsing and dispatch only
internal/origin/    resource parsing and canonical identities
internal/version/   semver, tag names, constraints
internal/vcs/       Source interface; git, svn, hg implementations
internal/digest/    the tree and file SHA-256 the lock records
internal/project/   .project.json, .meta.json, .kpar reading, verification, bundling
internal/lock/      .project.lock.json
internal/index/     sysand static index: read and emit
internal/install/   resolver: usages → lock entries → lib/
tests/e2e/          end-to-end suite on local fixtures
docs/               design and format notes
changes/unreleased/ changelog fragments
```

## Workflow

- Branch from `develop` as `<type>/<slug>` (`feat/`, `fix/`, `docs/`, `ci/`, `test/`); open the
  pull request against `develop` as a draft; mark it ready when CI is green. Merging is a
  maintainer's decision.
- Conventional Commits: `feat(install): …`, `fix(vcs): …`, `docs: …`, `test(e2e): …`, `chore: …`.
- Add a changelog fragment under `changes/unreleased/<slug>.<section>.md` (`added`, `changed`,
  `fixed`, `removed`); never edit `CHANGELOG.md` directly.
- Every Go file starts with `// SPDX-License-Identifier: Apache-2.0`.
- Keep comments to one or two lines; prefer good names.
- Never write session identifiers, internal work-item labels or tool-specific bookkeeping into
  files, commit messages or pull requests.

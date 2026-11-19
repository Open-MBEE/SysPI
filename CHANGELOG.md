# Changelog

All notable changes to SysPI are recorded here, in [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
form; SysPI follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html) and each release's tag
equals its version, as `.project.json` requires of the packages it installs.

## 0.2.0 - 2026-11-18

### Added

- A `.kpar` hosted at any http(s) URL can be named by an ECMA-427 Package-URL with the Annex B.3.5 `download_url` qualifier and an optional `checksum` (`pkg:generic/<name>[@<version>]?download_url=<url>[&checksum=<algorithm>:<hex>[,…]]`, percent-encoded; md5, sha1, sha224, sha256, sha384, sha512 and the sha3 family). The archive is fetched as a plain `.kpar` URL is, every declared checksum is verified before it is opened — a mismatch installs nothing — and it installs as `lib/<publisher>-<name>_<version>.kpar` with the archive's SHA-256 in the lock; `vcs_url` and `download_url` exclude each other, and `syspi add` writes the canonical form into `.project.json`.
- Accept a model exporter's index when bundling and publish verified KPARs to HTTPS artifact hosts that support conditional PUT.
- Prune VCS candidates using known constraints before export and reuse verified candidate metadata within a lock run.
- `syspi list [-json]` prints the locked packages in resource order with version, kind or VCS, revision, path under `lib/` and whether that installed copy is present; standard libraries and local directories are listed as recorded but never installed. It reads only the lock and `lib/`.
- `syspi remove <resource>…` drops a usage from `.project.json` (matched by its canonical identity, as `add` matches), re-resolves and re-locks what remains, and removes from `lib/` only the installed copies no remaining package of the closure needs — never a directory the lock did not place there, never one whose contents no longer match the lock. A package another locked package still uses stays, and the message names who uses it.
- Resolve transitive version conflicts by trying other matching repository tags while preferring compatible locked versions.
- A user name is accepted before the host of a `<vcs>+ssh://` location —
  `git+ssh://git@github.com/owner/repo@1.0.0`, `hg+ssh://…`, `svn+ssh://…` — so an SSH remote no
  longer needs a `User` entry in `ssh_config`. It is the SSH login, part of the identity
  (`…git%2Bssh:%2F%2Fgit%40github.com%2F…` in `.project.json` and the lock) and of the address
  the VCS executable is given. A password anywhere, and a user name on any other transport,
  stay refused.
- `syspi update [<resource>…]` re-resolves the named (or all) usages to the highest version their constraints allow, ignoring the lock — the one command that moves a lock entry — installs, rewrites the lock and removes the copies the moved entries left under `lib/`; `syspi outdated [-json] [<resource>…]` reports what `update` would move (`name old -> new`) and writes nothing.
- `syspi verify` checks every lock entry's installed copy under `lib/` against the lock — `.project.json`, the checksums `.meta.json` records, the tree or `.kpar` SHA-256, the usages it declares — and that every usage of `.project.json` is locked; it prints every discrepancy, exits 1 on any, contacts no repository and writes nothing.

### Changed

- `syspi publish` prints the canonical `pkg:generic/<name>@<version>?checksum=sha256:…&download_url=…` usage of the uploaded archive instead of a bare SHA-256.

### Removed

- A `.project.lock.json` is read exactly as this tool writes it: a repository `resource` in a non-canonical spelling (`pkg:git/…`, a bare `<vcs>+<transport>://…`) or a `usage` given as a bare string is refused with the canonical form, instead of being translated as pre-release locks were.

### Fixed

- The Windows CI and release gates install `hg` and `sliksvn` one Chocolatey package at a time, retry a failed install and trust only the binary on disk, so a transient feed error no longer slips through a `choco` that exits 0 and surfaces as `svn: command not found` later.
- Write one checksum object per KPAR member as required by the published KerML interchange schema and continue verifying legacy checksum arrays.
- A candidate reused within a `syspi lock` run claims its `lib/` slot again, so two packages installing under one name with different contents are still reported.
- Preserve the declared metamodel, `includesDerived` and `includesImplied` in KPAR bundles and require a versioned OMG SysML metamodel URI when bundling textual SysML models.

## 0.1.0 - 2026-10-30

### Added

- Releases publish `syspi-<os>-<arch>.tar.gz` (`.zip` on Windows) for Linux, macOS and Windows on amd64 and arm64, each holding `syspi` and `LICENSE`, with `SHA256SUMS.txt` signed keyless by the release workflow (`SHA256SUMS.txt.bundle`); the tag publishes nothing until the repository gate is green on all three systems. `install.sh` (Linux, macOS) and `install.ps1` (Windows) install a release after checking the download against the manifest and the installed binary against the tag.
- The `syspi` command: `init`, `add`, `install`, `sync`, `lock`, `bundle` and `version` for KerML / SysML v2 project-interchange packages — any git, svn or hg repository with `.project.json` at a tag, a local directory, a local or remote `.kpar` file, or an OMG standard-library IRI. Packages are verified before use, installed under `lib/<publisher>-<name>_<version>` beside `.project.json` and recorded in a deterministic `.project.lock.json` with a VCS-neutral tree hash.

### Changed

- Package identities are ECMA-427 Package-URLs: a repository package is `pkg:generic/<name>?vcs_url=<vcs>+<transport>://<host>/<path>[@<version>][#<subpath>]` with the Annex B.3.5 `vcs_url` qualifier percent-encoded (`<name>` is the repository's last path segment, `<version>` a tag naming a semantic version). `pkg:git/<host>/<path>[@<version>][#<subpath>]` and the bare `<vcs>+<transport>://…` notation are accepted as input and normalised to it; `syspi add` writes the canonical form into `.project.json`. `<vcs>+file:///path` addresses a repository on this machine. A plain `http(s)` IRI is only dereferenced as a project interchange file (KerML 1.1 §10.3 Table 12). Other spellings (`pkg:svn/…`, `pkg:hg/…`, clone URLs such as `https://host/path.git@v`, `git@host:path`, `file:///path@v`) are refused with a message naming the accepted ones.

### Fixed

- `syspi -h` names the ECMA-427 identity grammar and the two input spellings it normalises, instead of the refused `pkg:<vcs>/<host>/<path>` form.
- A drive letter in a `file://` resource is only a drive on Windows; on Linux and macOS `file:///C:/tools.kpar` names the directory `C:` again. The Windows spellings are documented, and the lock-rollback test runs on Windows too.
- On Windows, a repository on this machine is accepted as `git+file:///C:/path` (and `git+file://C:/path`), `file://` resources with a drive letter resolve, and the suite runs on Windows; CI gates on Windows with hg and svn installed.

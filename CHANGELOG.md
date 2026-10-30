# Changelog

All notable changes to SysPI are recorded here, in [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
form; SysPI follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html) and each release's tag
equals its version, as `.project.json` requires of the packages it installs.

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

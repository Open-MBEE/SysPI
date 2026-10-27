# SysPI — SysML Package Installer

A package manager for KerML / SysML v2 project-interchange packages: **a package is any git
repository with `.project.json` at a tag** (Subversion and Mercurial too). Binary: `syspi`.

SysPI fetches, verifies, installs and locks packages. It is independent of
[OpenSysML](https://github.com/Open-MBEE/OpenSysML), whose `sysml` and `sysml-lsp` only *read*
what package managers install.

## The contract, on one screen

- **Identity** — an ECMA-427 Package-URL of type `generic` with the Annex B.3.5 `vcs_url`
  qualifier: `pkg:generic/<name>?vcs_url=<vcs>+<transport>://<host>/<path>[@<version>][#<subpath>]`,
  the qualifier percent-encoded (`+` → `%2B`, `/` → `%2F`, `@` → `%40`, `#` → `%23`, `:` kept), e.g.
  `pkg:generic/OpenSysML-Extensions-Library?vcs_url=git%2Bhttps:%2F%2Fgithub.com%2FOpen-MBEE%2FOpenSysML-Extensions-Library%401.0.0`.
  `<vcs>` is `git`, `svn` or `hg`, `<transport>` one of Annex B.3.5's (`git`, `https`, `http`,
  `ssh` for git; `http`, `https`, `static-http`, `ssh` for hg; `svn`, `http`, `https`, `ssh` for svn)
  plus `file` for a repository on this machine; `<name>` is the repository's last path segment.
  Two shorter spellings are accepted as input and normalised to it: `pkg:git/<host>/<path>[@<version>][#<subpath>]`
  (git over https) and the bare notation `git+https://host/path@1.2.0`, `svn://host/repo@1.2.0`,
  `hg+https://…`, `git+file:///srv/repo@1.2.0`. Refused with a message naming those three:
  `pkg:svn/…`, `pkg:hg/…`, `pkg:github/…`, plain clone URLs (`https://host/path.git`, `ssh://…`,
  `git@host:path`, `file:///path@1.2.0`) and a commit id as version. SysPI hand-parses this one
  purl shape and uses no purl library. A plain `http(s)` IRI is only ever read as a project
  interchange file (KerML 1.1 §10.3 Table 12), never cloned.
- **Package** — the tagged tree (or `#<subpath>` within it) *is* the project directory:
  `.project.json` (KerML 1.1 §10.3, Table 12) at the tag is mandatory and its `version` must
  equal the tag, `v<semver>` preferred, bare `<semver>` accepted. No `.project.json` is an error;
  nothing is ever synthesised. For svn, tags are the directories under `tags/`.
- **Resolution** — pure version control through the user's own `git`, `svn` or `hg` executable,
  so credential helpers, SSH and proxy configuration apply: list tags, export the tagged tree,
  record the revision. No index is needed.
- **Install layout** — `lib/<publisher>-<name>_<version>/` (a tree) or
  `lib/<publisher>-<name>_<version>.kpar` (an archive) beside the consuming `.project.json`.
- **Lock** — `.project.lock.json` beside `.project.json`: one entry per package of the
  transitive closure with `resource`, `kind`, `vcs`, `source`, `tag`, `revision`, `sha256` and
  the usages it declares; deterministic bytes, so a fresh clone reproduces it exactly. A lock
  entry wins over a constraint.
- **Constraints** — `versionConstraint` in `.project.json`: exact (`1.2.0`), caret (`^1.2.0`),
  tilde (`~1.2.0`) and comparison ranges (`>=1.2.0 <2.0.0`). `@<version>` in the resource with no
  `versionConstraint` is an exact constraint.
- **Verification before use** — `.meta.json` checksums of every member (regenerated for the
  installed copy, and said so, when a tag tree has none), the recorded revision, the tree or
  `.kpar` SHA-256, and path-traversal, symlink and size bounds on every archive and export.
- **Other sources** — a local directory (editable, read in place), a local `.kpar`, a remote
  `.kpar` URL, OMG standard-library IRIs (`https://www.omg.org/spec/{KerML,SysML}/…/*.kpar`,
  recorded but never installed: every reader bundles them).

Details — the grammar, the svn `tags/` convention, the tree-hash algorithm and the lock format —
are in [docs/packages.md](docs/packages.md).

## Commands

```
syspi init [-C dir] [-publisher P] [-name N] [-version V]   write .project.json and .meta.json
syspi add <resource>[@<version>] [<constraint>]              add a usage, resolve, install, lock
syspi install | sync                                          resolve .project.json + lock, install what is missing or changed
syspi lock                                                    write .project.lock.json without installing
syspi bundle [-o out.kpar] [-tag T] <dir>                     deterministic .kpar; checks tag == version in a tagged checkout
```

Every project command takes `-C <dir>` and `-offline`. Every command works offline when the lock
and `lib/` already satisfy the project.

```sh
$ syspi init -publisher acme -name rover
initialised acme-rover_0.1.0 in . (.project.json, .meta.json)
$ syspi add pkg:git/github.com/MBSE4U/sysmod-sysmlv2@5.1.5
pkg:generic/sysmod-sysmlv2?vcs_url=git%2Bhttps:%2F%2Fgithub.com%2FMBSE4U%2Fsysmod-sysmlv2: tag 5.1.5: .meta.json records no checksum for 48 of 52 files; their integrity rests on the tree hash
installed sysmod 5.1.5 (git 5.1.5 07286ecd2128b64f53fc3d05aee811e98b991a85) -> lib/mbse4u-sysmod_5.1.5
1 package(s) locked: 1 installed, 0 already present, 0 not installed (standard libraries and local directories); .project.lock.json updated
$ sysml -validate Rover.sysml   # OpenSysML reads lib/ on its own; no flags
```

`bundle` takes its `created` timestamp from `SOURCE_DATE_EPOCH`, else from the checked-out git or
hg commit, so two builds of the same tree are byte-identical.

## Install

```sh
go install github.com/Open-MBEE/SysPI/cmd/syspi@latest
```

Release binaries for Linux, macOS and Windows (amd64, arm64) with a `SHA256SUMS` file are
attached to each `v*` tag. Requires `git` (and `svn` / `hg` for packages hosted there) on `PATH`.

## Interoperability

- **OpenSysML** reads `lib/` next to `.project.json` and honours `@<version>` in a usage IRI as
  an exact constraint; that is the whole contract between the tools.

## Credentials

SysPI never prompts and keeps no credential store of its own; each backend uses the user's own
configuration, and credentials never enter `.project.json`, the lock or a log (URLs with
`user:password@` are refused).

- **git** — the user's `git` runs with `GIT_TERMINAL_PROMPT=0`; configure a credential helper
  (`git config --global credential.helper …`) or use an SSH remote.
- **hg** — the user's `hg` runs with `HGPLAIN=1`, which keeps `[auth]`: add `prefix`, `username`,
  `password` for the host to your hgrc.
- **svn** — the user's `svn` runs `--non-interactive`. A netrc entry for the repository host
  (`$NETRC`, else `~/.netrc`) is passed as `--username <login> --password-from-stdin
  --no-auth-cache` (svn 1.14+; the password travels on stdin, never in argv, the environment or
  the auth cache), so CI boxes and containers without a keyring work. Otherwise svn's own cached
  credentials apply (`svn ls <url>` once interactively), or use `svn+ssh://`.
- **HTTP** (`.kpar` URLs) — HTTP Basic from your netrc: `$NETRC` if set, else `~/.netrc`
  (`%USERPROFILE%\_netrc` on Windows), matched by host and never sent to another host on a
  redirect. Proxy settings come from the environment (`HTTPS_PROXY`, `NO_PROXY`).

An authentication failure is reported with one hint naming the fix for that backend.

## Future

Design only, not built here: a SysML-rendered index; LSP integration; attestation / cosign of
releases; any server.

## Licence

Licensed under the Apache License, Version 2.0 (see `LICENSE`).

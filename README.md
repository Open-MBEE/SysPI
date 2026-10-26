# SysPI — SysML Package Installer

A package manager for KerML / SysML v2 project-interchange packages: **a package is any git
repository with `.project.json` at a tag** (Subversion and Mercurial too). Binary: `syspi`.

SysPI fetches, verifies, installs and locks packages. It sits beside sensmetry's `sysand` as a
second, interoperable client and is independent of [OpenSysML](https://github.com/Open-MBEE/OpenSysML),
whose `sysml` and `sysml-lsp` only *read* what package managers install.

## The contract, on one screen

- **Identity** — `pkg:<vcs>/<host>/<path>[@<version>][#<subpath>]` with `<vcs>` one of `git`,
  `svn`, `hg`: the repository itself, no provider vocabulary. A plain clone URL with `@<version>`
  (`https://host/org/repo.git@1.2.0`, `ssh://git@host/org/repo@1.2.0`, `git@host:org/repo@1.2.0`,
  `file:///srv/repo@1.2.0`) is accepted and normalised to it. `pkg:github/…` and `pkg:gitlab/…`
  are refused in favour of `pkg:git/github.com/…`; `pkg:cvs/…` is not supported.
  `pkg:git`, `pkg:svn`, `pkg:hg` and `pkg:sysand` are **not registered purl types**: SysPI
  hand-parses them and uses no purl library.
- **Package** — the tagged tree (or `#<subpath>` within it) *is* the project directory:
  `.project.json` (KerML 1.1 §10.3, Table 12) at the tag is mandatory and its `version` must
  equal the tag, `v<semver>` preferred, bare `<semver>` accepted. No `.project.json` is an error;
  nothing is ever synthesised. For svn, tags are the directories under `tags/`.
- **Resolution** — pure version control through the user's own `git`, `svn` or `hg` executable,
  so credential helpers, SSH and proxy configuration apply: list tags, export the tagged tree,
  record the revision. No index is needed.
- **Install layout** — `lib/<publisher>-<name>_<version>/` (a tree) or
  `lib/<publisher>-<name>_<version>.kpar` (an archive) beside the consuming `.project.json`.
  An existing `.sysand/lib/` is read so a project with sysand-installed dependencies works
  unchanged; it is never written.
- **Lock** — `.project.lock.json` beside `.project.json`: one entry per package of the
  transitive closure with `resource`, `kind`, `vcs`, `source`, `tag`, `revision`, `sha256` and
  the usages it declares; deterministic bytes, so a fresh clone reproduces it exactly. A lock
  entry wins over a constraint. `sysand-lock.toml` is never written.
- **Constraints** — `versionConstraint` in `.project.json`: exact (`1.2.0`), caret (`^1.2.0`),
  tilde (`~1.2.0`) and comparison ranges (`>=1.2.0 <2.0.0`). `@<version>` in the resource with no
  `versionConstraint` is an exact constraint.
- **Verification before use** — `.meta.json` checksums of every member (regenerated for the
  installed copy, and said so, when a tag tree has none), the recorded revision, the tree or
  `.kpar` SHA-256, and path-traversal, symlink and size bounds on every archive and export.
- **Other sources** — a local directory (editable, read in place), a local `.kpar`, a remote
  `.kpar` URL, OMG standard-library IRIs (`https://www.omg.org/spec/{KerML,SysML}/…/*.kpar`,
  recorded but never installed: every reader bundles them) and `pkg:sysand/<publisher>/<name>`
  through a configured static index.

Details — the grammar, the svn `tags/` convention, the tree-hash algorithm and the lock format —
are in [docs/packages.md](docs/packages.md).

## Commands

```
syspi init [-C dir] [-publisher P] [-name N] [-version V]   write .project.json and .meta.json
syspi add <resource>[@<version>] [<constraint>]              add a usage, resolve, install, lock
syspi install | sync                                          resolve .project.json + lock, install what is missing or changed
syspi lock                                                    write .project.lock.json without installing
syspi bundle [-o out.kpar] [-tag T] <dir>                     deterministic .kpar; checks tag == version in a tagged checkout
syspi index emit [-o dir] <package dir or .kpar>...           static sysand-compatible index
```

Every project command takes `-C <dir>`, `-offline` and `-index <url|dir>` (default
`$SYSPI_INDEX`). Every command works offline when the lock and `lib/` already satisfy the project.

```sh
$ syspi init -publisher acme -name rover
initialised acme-rover_0.1.0 in . (.project.json, .meta.json)
$ syspi add pkg:git/github.com/MBSE4U/sysmod-sysmlv2@5.1.5
pkg:git/github.com/MBSE4U/sysmod-sysmlv2: tag 5.1.5: .meta.json records no checksum for 48 of 52 files; their integrity rests on the tree hash
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

- **OpenSysML** reads `lib/` next to `.project.json` (and `.sysand/lib/`) and honours
  `@<version>` in a usage IRI as an exact constraint; that is the whole contract between the tools.
- **sysand** users can install packages from an index SysPI emits
  (`sysand --index <url>`); SysPI resolves `pkg:sysand/<publisher>/<name>` from any sysand index
  with the configuration a sysand user already has. Honoured: the static layout
  (`index.json`, `<publisher>/<name>/versions.json`, `<version>/{.project.json,.meta.json,project.kpar}`);
  the discovery document `<location>/sysand-index-config.json` (`index_root`, `api_root`;
  absent means the location is the flat root; relative URLs are an error; `api_root` is recorded,
  never called); URL templates with `{path}` or `{path_raw}`; `versions.json` validation
  (descending semver, no build metadata, `sha256:<64-hex>`, no duplicates, `removed` is a hard
  error, `yanked` is never newly selected); the index chain `-index`/`$SYSPI_INDEX`, then
  `$SYSAND_INDEX`, then `$SYSAND_DEFAULT_INDEX` (else `https://sysand.com`), all comma-separated,
  `-no-index` to use none; and the `SYSAND_CRED_*` credential grammar below. Not honoured: the
  keyring credential store and `sysand auth login/logout/whoami` (use `SYSAND_CRED_*` or netrc),
  `sysand publish`, `sysand-lock.toml` as output.

## Credentials

SysPI never prompts and keeps no credential store of its own; each backend uses the user's own
configuration, and credentials never enter `.project.json`, the lock or a log (URLs with
`user:password@` are refused).

- **git** — the user's `git` runs with `GIT_TERMINAL_PROMPT=0`; configure a credential helper
  (`git config --global credential.helper …`) or use an SSH remote.
- **hg** — the user's `hg` runs with `HGPLAIN=1`, which keeps `[auth]`: add `prefix`, `username`,
  `password` for the host to your hgrc.
- **svn** — the user's `svn` runs `--non-interactive`: run `svn ls <url>` once interactively so it
  caches the credentials, or use `svn+ssh://`. Builds with plaintext password storage disabled
  (Debian, Ubuntu) cache the username only; use `svn+ssh://` or a keyring store there.
- **HTTP** (sysand index files, `.kpar` URLs) — first sysand's environment grammar:
  `SYSAND_CRED_<LABEL>=<URL glob>` with `SYSAND_CRED_<LABEL>_BEARER_TOKEN` or
  `SYSAND_CRED_<LABEL>_BASIC_USER` + `SYSAND_CRED_<LABEL>_BASIC_PASS` (`*` and `?` stop at `/`,
  `**` does not). A matching URL is requested anonymously and retried with the credential on a
  4xx other than 429; several matching globs are warned about and tried in label order until one
  answers non-4xx; a credential follows a redirect only to the same host. Then HTTP Basic from
  your netrc: `$NETRC` if set, else `~/.netrc` (`%USERPROFILE%\_netrc` on Windows), matched by
  host. Then anonymous. Proxy settings come from the environment (`HTTPS_PROXY`, `NO_PROXY`).

An authentication failure is reported with one hint naming the fix for that backend.

## Future

Design only, not built here: a SysML-rendered index; LSP integration; attestation / cosign of
releases; mirroring sysand.com; any server.

## Licence

Licensed under the Apache License, Version 2.0 (see `LICENSE`).

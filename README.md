# SysPI — SysML Package Installer

A package manager for KerML / SysML v2 project-interchange packages: **a package is any git
repository with `.project.json` at a tag** (Subversion and Mercurial too). Binary: `syspi`.

SysPI fetches, verifies, installs and locks packages. It is independent of
[OpenSysML](https://github.com/Open-MBEE/OpenSysML), whose `sysml` and `sysml-lsp` only *read*
what package managers install.

## The contract, on one screen

- **Identity** — an ECMA-427 Package-URL of type `generic` with the Annex B.3.5 `vcs_url`
  qualifier for a repository (or `download_url` for a hosted `.kpar`, under "Other sources"):
  qualifier: `pkg:generic/<name>?vcs_url=<vcs>+<transport>://[<user>@]<host>/<path>[@<version>][#<subpath>]`,
  the qualifier percent-encoded (`+` → `%2B`, `/` → `%2F`, `@` → `%40`, `#` → `%23`, `:` kept), e.g.
  `pkg:generic/OpenSysML-Extensions-Library?vcs_url=git%2Bhttps:%2F%2Fgithub.com%2FOpen-MBEE%2FOpenSysML-Extensions-Library%401.0.0`.
  `<vcs>` is `git`, `svn` or `hg`, `<transport>` one of Annex B.3.5's (`git`, `https`, `http`,
  `ssh` for git; `http`, `https`, `static-http`, `ssh` for hg; `svn`, `http`, `https`, `ssh` for svn)
  plus `file` for a repository on this machine; `<name>` is the repository's last path segment.
  `<user>@` is the SSH login and is accepted on `<vcs>+ssh` only
  (`git+ssh://git@github.com/owner/repo@1.2.0`); it is part of the identity, so it is written to
  `.project.json` and the lock as `…git%2Bssh:%2F%2Fgit%40github.com%2F…`, and the same location
  without it is another identity. A password, or a user name on any other transport, is refused.
  Two shorter spellings are accepted as input and normalised to it: `pkg:git/<host>/<path>[@<version>][#<subpath>]`
  (git over https) and the bare notation `git+https://host/path@1.2.0`, `git+ssh://git@host/path@1.2.0`, `svn://host/repo@1.2.0`,
  `hg+https://…`, `git+file:///srv/repo@1.2.0` (`git+file:///C:/repo@1.2.0` on Windows). Refused with a message naming those three:
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
  entry wins over a constraint everywhere but in `update`, the one command that moves an entry
  the graph can keep (`outdated` shows what it would move); an entry whose version conflicts
  with another usage is re-resolved to another matching tag.
- **Constraints** — `versionConstraint` in `.project.json`: exact (`1.2.0`), caret (`^1.2.0`),
  tilde (`~1.2.0`) and comparison ranges (`>=1.2.0 <2.0.0`). `@<version>` in the resource with no
  `versionConstraint` is an exact constraint.
- **Verification before use** — `.meta.json` checksums of every member (regenerated for the
  installed copy, and said so, when a tag tree has none), the recorded revision, the tree or
  `.kpar` SHA-256, and path-traversal, symlink and size bounds on every archive and export.
- **Other sources** — a local directory (editable, read in place), a local `.kpar`, a remote
  `.kpar` URL, OMG standard-library IRIs (`https://www.omg.org/spec/{KerML,SysML}/…/*.kpar`,
  recorded but never installed: every reader bundles them). A `.kpar` hosted at any http(s) URL
  — a release asset, a repository manager's export — is also a Package-URL with the Annex B.3.5
  `download_url` qualifier and an optional `checksum`:
  `pkg:generic/<name>[@<version>]?download_url=<url>[&checksum=<algorithm>:<hex>[,…]]`, both
  percent-encoded (`pkg:generic/tools?checksum=sha256:9f86…&download_url=https:%2F%2Fdist.example.org%2Ftools.kpar`).
  The archive is fetched as a plain `.kpar` URL is (netrc, redirects, size bound), the declared
  checksums are verified before it is opened (a mismatch installs nothing), `<version>` is the
  archive's own and is held to `@<version>` / `versionConstraint`, and the lock's `sha256` is the
  archive digest.

Details — the grammar, the svn `tags/` convention, the tree-hash algorithm and the lock format —
are in [docs/packages.md](docs/packages.md).

## Commands

```
syspi init [-C dir] [-publisher P] [-name N] [-version V]   write .project.json and .meta.json
syspi add <resource>[@<version>] [<constraint>]              add a usage, resolve, install, lock
syspi remove <resource>...                                    drop a usage, re-lock, remove what nothing else needs from lib/
syspi update [<resource>...]                                  move the named (or all) usages to the highest version allowed, install, re-lock
syspi outdated [-json] [<resource>...]                        report what update would move; write nothing
syspi install | sync                                          resolve .project.json + lock, install what is missing or changed
syspi lock                                                    write .project.lock.json without installing
syspi verify                                                  check lib/ against the lock, offline; exit 1 on any discrepancy, write nothing
syspi list [-json]                                            the locked packages, where each is in lib/ and whether it is present
syspi bundle [-o out.kpar] [-tag T] [-index index.json | -index-opensysml sysml | -index-toolkit sysmlv2] <dir>
syspi check-model -opensysml sysml [-C dir] [-direct]        validate a project with OpenSysML
syspi check-model -toolkit sysmlv2 [-library dir] [-direct]  validate the project and the locked dependencies
syspi publish <archive.kpar> <https-url.kpar>               verify and upload to a conditional PUT artifact host
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

## Optional model validation and index generation

SysPI runs without OpenSysML or sysml-toolkit. Install either executable
separately when you want model checks in your release workflow. Put the
executable on `PATH`, or pass its path in place of `sysml` or `sysmlv2`.
SysPI invokes a tool only when you pass its flag.

### Use OpenSysML with a SysPI project

1. Get the `sysml` CLI for your platform from the [OpenSysML
   releases](https://github.com/Open-MBEE/OpenSysML/releases). Use a build
   that supports `-validate` and `-bundle`. If it is outside `PATH`, pass
   its full filename, including `.exe` if applicable.
2. From the project directory containing `.project.json`, restore and verify
   dependencies, then validate the project:

   ```sh
   syspi install
   syspi verify
   syspi check-model -opensysml sysml
   ```

   SysPI checks the lock and gives OpenSysML a temporary copy of the project's
   textual models and the textual files from the exact locked dependencies.
   Validation warnings cause the check to fail. Use `-C <dir>` to check a
   project from another directory.

   Add `-direct` to check that the current project's references also resolve
   with only its own files and directly declared dependencies:

   ```sh
   syspi check-model -opensysml sysml -direct
   ```

   SysPI first checks the full graph. The second pass reports unresolved
   references in the current project's files. It allows unresolved references
   *inside* a direct dependency when those references were resolved in the full
   graph. Run this check for each project you release. A model that relies on
   an indirect dependency for name lookup may need to declare it directly.
3. Declare the project's metamodel in `.meta.json`. For SysML 2.0 use
   `"metamodel": "https://www.omg.org/spec/SysML/20250201"`. Then bundle:

   ```sh
   syspi bundle -index-opensysml sysml -o vehicle-1.0.0.kpar .
   ```

   SysPI asks OpenSysML to generate an index from a temporary copy of the
   project's textual models. The parser finds top-level names and short names.
   SysPI uses the generated index in the final KPAR. This mode rejects a project
   that mixes `.sysml` and `.kerml` sources. If you already have an
   exporter-generated index, use `-index index.json` instead. Choose one
   index option per bundle.

### Use sysml-toolkit instead of, or alongside, OpenSysML

Download [sysml-toolkit 0.13.0](https://github.com/Open-MBEE/sysml-toolkit/releases/tag/v0.13.0)
for your platform and verify the archive against the release's `SHA256SUMS`.
Extract it and add the directory containing `sysmlv2` to `PATH`, or pass the
executable path directly (`sysmlv2.exe` on Windows). Confirm the version with
`sysmlv2 --version`. The toolkit is only needed when you select it.
From the directory containing `.project.json`, run:

```sh
syspi install
syspi verify
syspi check-model -toolkit sysmlv2 -direct
syspi bundle -index-toolkit sysmlv2 -o vehicle-1.0.0.kpar .
```

Set the SysML metamodel in `.meta.json` before bundling. With no filenames,
SysPI supplies the verified project and dependency files to the toolkit.
The toolkit generates the parser-based index with `kpar-index`; SysPI writes
the archive using its own project metadata and checksum rules.
SysPI can also install a standalone KPAR created by
`sysmlv2 convert --to kpar -o model.kpar Model.sysml`.
`-direct` checks the project and each installed package with textual
sources against its own declared direct dependencies. If a package fails,
SysPI reports its locked resource. Toolkit warnings fail validation. Use
`-library <dir-or-bundle>` when the toolkit needs the standard library.
To run both validators against the same
lock, use `syspi check-model -opensysml sysml -toolkit sysmlv2 -direct`.

For a standalone strict check, pass explicit filenames to
`syspi check-model -toolkit sysmlv2 <file>...`. In that mode the toolkit
checks the given files together. The index and direct dependency workflows
cover textual models. JSON and XMI need a suitable exporter and validator.

`syspi dependency-sources` exports verified textual dependencies as JSON for
editors. It includes both `.sysml` and `.kerml` members, follows dependencies
declared by metadata-only packages, and emits a shared installed copy once.

To run the release integration test, set `SYSPI_TEST_TOOLKIT` to the absolute
path to the 0.13.0 executable and run
`go test ./tests/e2e -run '^TestToolkitRelease$'`.

## Install

Linux and macOS:

```sh
curl -fsSL https://raw.githubusercontent.com/Open-MBEE/SysPI/main/install.sh | sh
```

Windows (PowerShell):

```powershell
irm https://raw.githubusercontent.com/Open-MBEE/SysPI/main/install.ps1 | iex
```

Both install the latest release's `syspi` for this machine (`/usr/local/bin` or `~/.local/bin`;
`%LOCALAPPDATA%\Programs\SysPI` on Windows) after checking the archive against the release's
`SHA256SUMS.txt`, and run it once to confirm it reports the release. `install.sh --help` lists
the options (`--version v0.1.0`, `--prefix`, `--bin-dir`, `--verify-signature` for the cosign
check of the manifest, `--dry-run`); `install.ps1` takes the same as `-Version`, `-InstallDir`,
`-VerifySignature`, `-DryRun`, and the environment variables `SYSPI_VERSION`, `SYSPI_PREFIX` /
`SYSPI_INSTALL_DIR` and `SYSPI_DOWNLOAD_BASE` configure a piped run. The checksum guards against a
corrupt or partial download; it comes from the same place as the archive, so against a replaced
release only the signature check helps — pass `--verify-signature` / `-VerifySignature` (needs
[cosign](https://docs.sigstore.dev/cosign/system_config/installation/)) or verify by hand as below.

Each release attaches `syspi-<os>-<arch>.tar.gz` (`.zip` on Windows) for Linux, macOS and
Windows on amd64 and arm64, `SHA256SUMS.txt`, and its keyless cosign signature
`SHA256SUMS.txt.bundle`, verifiable with

```sh
cosign verify-blob SHA256SUMS.txt --bundle SHA256SUMS.txt.bundle \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity https://github.com/Open-MBEE/SysPI/.github/workflows/release.yml@refs/tags/v0.1.0
```

From source, with Go 1.25 or later:

```sh
go install github.com/Open-MBEE/SysPI/cmd/syspi@latest
```

Requires `git` (and `svn` / `hg` for packages hosted there) on `PATH`.

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
  redirect (a redirect target gets only its own `machine` entry, never `default`). Proxy settings come from the environment (`HTTPS_PROXY`, `NO_PROXY`).

An authentication failure is reported with one hint naming the fix for that backend.

## Future

Design only, not built here: a SysML-rendered index; LSP integration; any server.

## Licence

Licensed under the Apache License, Version 2.0 (see `LICENSE`).

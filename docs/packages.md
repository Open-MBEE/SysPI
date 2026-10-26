# Packages, identities, locks

## Identity grammar

```
resource   = "pkg:" vcs "/" host "/" path [ "@" version ] [ "#" subpath ]
vcs        = "git" | "svn" | "hg"
host       = DNS name (lower-cased), or empty for a repository on this machine
path       = repository path on the host, without a leading "/" and without ".git"
version    = semantic version (MAJOR.MINOR.PATCH[-pre][+build]); an exact constraint
subpath    = directory within the tagged tree that is the project directory
```

Examples: `pkg:git/github.com/MBSE4U/sysmod-sysmlv2@5.1.5`,
`pkg:hg/hg.example.org/models/rover#packages/power`, `pkg:svn/svn.example.org/repos/rover@1.0.0`,
`pkg:git//srv/git/rover.git@1.0.0` (empty host: a local repository).

Plain clone addresses are normalised to the identity: `https://host/path[.git]`,
`ssh://[user@]host[:port]/path`, `user@host:path` (scp style), `git://`, `svn://`, `svn+ssh://`
and `file:///path`, each with an optional `@<version>` and `#<subpath>`. The canonical identity
(`pkg:<vcs>/<host>/<path>[#<subpath>]`) is what the lock records as `resource`; the address the
user wrote is kept as `source`, so an SSH remote stays SSH.

Refused: `pkg:github/…`, `pkg:gitlab/…` and other provider vocabularies (the host is part of the
identity; write `pkg:git/github.com/…`); `pkg:cvs/…` and any other version-control system
("not supported; supported: git, svn, hg"); `@latest` or any non-semver version.

None of these is a registered purl type; SysPI hand-parses them and does not use a purl library.

## What a package is

The tagged tree — the repository root, or `#<subpath>` within it — is the project directory. It
must contain `.project.json` (the `InterchangeProject` of KerML 1.1 §10.3, Table 12: `name` and
`version` mandatory, `usage[]` of `{resource, versionConstraint}`) and its `version` must equal the
version the tag name carries. Tags are `v<semver>` (preferred) or `<semver>`; when both exist for
one version the `v` form is used. A tag without `.project.json` is an error, never a package with a
synthesised manifest. `.meta.json` (Table 13) is verified when present; when the tag tree lacks
one, SysPI generates it for the installed copy and says so.

### Per system

| | versions | tree | `revision` |
|---|---|---|---|
| git | `git ls-remote --tags` (annotated tags peeled to their commit) | `git clone --depth 1 --branch <tag>` | commit SHA |
| hg | `hg tags` of a `--noupdate` clone | `hg archive` of the tag's changeset, without `.hg_archival.txt` and `.hgtags` | 40-hex changeset node id |
| svn | `svn ls <url>/tags` — tags are directories under `tags/` by convention, named `v<semver>` or `<semver>` | `svn export <url>/tags/<tag>@<rev>` | last-changed revision of the tag directory |

An svn revision number is local to its repository, not content-addressed: for svn the lock's
integrity rests on `sha256` and `revision` is a reproducibility hint. A git or hg tag whose
revision differs from the lock is an error.

Each system is driven through the user's own executable on `PATH`, so credential helpers, SSH
configuration and proxies apply. A missing executable is reported by name.

## Tree hash

`sha256` of a tree entry is one deterministic SHA-256 over the exported tree, so the same package
served from git, svn or hg hashes alike:

1. Walk the tree; skip `.git`, `.hg` and `.svn` directories. Every entry must be a regular file;
   a symbolic link is an error.
2. Take each file's path relative to the project directory with `/` separators and sort the
   paths bytewise.
3. For each path, feed the hash: the decimal byte length of the path, `\n`, the path, `\n`, the
   decimal byte length of the content, `\n`, the content, `\n`.
4. File modes, timestamps and empty directories do not contribute.

When SysPI generated the `.meta.json` of an installed copy, the lock entry carries
`"metaGenerated": true` and that file is left out when the installed tree is re-hashed.

For a `.kpar` (local, remote or from an index) `sha256` is the digest of the archive file.

## Lock file

`.project.lock.json`, beside the consuming `.project.json`, sorted by `resource`, two-space
indented, keys in a fixed order:

```json
{
  "lockVersion": 1,
  "packages": [
    {
      "resource": "pkg:git/github.com/MBSE4U/sysmod-sysmlv2",
      "kind": "vcs",
      "publisher": "mbse4u",
      "name": "sysmod",
      "version": "5.1.5",
      "vcs": "git",
      "source": "https://github.com/MBSE4U/sysmod-sysmlv2.git",
      "tag": "5.1.5",
      "revision": "07286ecd2128b64f53fc3d05aee811e98b991a85",
      "sha256": "…64 hex…",
      "usage": [
        { "resource": "git@private.example:team/core.git", "versionConstraint": "^1.0.0" }
      ]
    }
  ]
}
```

`kind` is `vcs`, `archive` (a `.kpar`), `local` (an editable directory, read in place) or
`standard` (an OMG standard-library IRI; recorded, never installed). `usage` lists the usages the
package's own `.project.json` declares — resource and `versionConstraint` as written, so the
constraint and the transport (an SSH address stays SSH) survive — and an offline run walks the
closure from the lock alone. Two packages that would install under the same
`<publisher>-<name>_<version>` with different contents are an error. New packages are staged and
published into `lib/` only after every usage resolved and verified; a failing run leaves `lib/`
and the lock as they were. A lock entry wins over the constraint in `.project.json` as long as it satisfies it;
`install` then verifies the installed copy's digest and fetches only what is missing or changed.

## Install layout

```
.project.json
.project.lock.json
lib/
  mbse4u-sysmod_5.1.5/        exported tree of a tagged repository
  acme-tools_3.0.0.kpar       archive source, verified and copied as is
```

The directory name is `<publisher>-<name>_<version>` (`<name>_<version>` without a publisher),
the layout sysand uses under `.sysand/lib/`, which SysPI also reads — a locked package already
present there is not installed again — but never writes.

## Static index

`syspi index emit -o <dir> <packages>…` writes sysand's static layout: `<dir>/index.json`
listing `pkg:sysand/<publisher>/<name>` IRIs and, per project,
`<publisher>/<name>/versions.json` (`version`, `usage`, `kpar_size`, `kpar_digest`, newest first)
with `<version>/{.project.json,.meta.json,project.kpar}`. `pkg:sysand/<publisher>/<name>`
resources resolve through the index named by `-index` or `$SYSPI_INDEX`.

## Bundle reproducibility

`syspi bundle` writes members sorted with fixed 1980-01-01 ZIP timestamps and regenerates
`.meta.json`. Its `created` time is `SOURCE_DATE_EPOCH` when set, otherwise the commit time of the
checked-out git or hg revision, otherwise the current time; so a bundle of an unchanged checkout
is byte-identical across runs. The output file is never a member of itself when written inside
the project.

## Credentials

SysPI shells out to the user's `git`, `svn` and `hg` with the user's environment, so their
credential helpers, SSH agents, hgrc `[auth]` sections and svn auth caches apply unchanged; its
own HTTP downloads (index files, `.kpar` URLs) take HTTP Basic credentials from the netrc file
(`$NETRC`, else `~/.netrc` / `%USERPROFILE%\_netrc`), matched by host and sent only to that
host — Go's client strips `Authorization` on a cross-host redirect and SysPI applies the netrc
per request host, so a redirect elsewhere gets that host's entry or none. Proxies come from the
environment through `http.DefaultTransport`.

Deliberately not done: no prompting (`GIT_TERMINAL_PROMPT=0`, `svn --non-interactive`), no
credential storage or cache of SysPI's own, no credentials in URLs (`https://user:pw@…` is
refused), nothing credential-like in `.project.json`, the lock or the output (URL user-info is
redacted from error text).

Authentication failures carry one hint each: git `could not read Username` / `Authentication
failed` → configure a credential helper or use an SSH remote; svn `E170001` / `E215004` → run
`svn ls <url>` once interactively so svn caches the credentials (on builds without plaintext
password storage, Debian and Ubuntu among them, the cache holds only the username — use
`svn+ssh://` or a keyring store); hg `authorization failed` / `http error 401` → add an `[auth]`
section to hgrc (`HGPLAIN=1` does not disable it); HTTP 401 / 403 → a netrc entry for the host.

`tests/e2e/auth_test.go` proves each path against local servers: git over dumb HTTP behind Basic
auth with a credential helper configured through `GIT_CONFIG_*`, hg over `static-http://` with an
`[auth]` section in `HGRCPATH`, svn against `svnserve` with `anon-access = none` and a seeded
auth cache, and the index and `.kpar` downloads behind Basic auth with a netrc.

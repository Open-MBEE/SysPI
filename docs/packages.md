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
with `<version>/{.project.json,.meta.json,project.kpar}`.

`pkg:sysand/<publisher>/<name>` resources resolve through the indexes, in order, of `-index` /
`$SYSPI_INDEX`, then `$SYSAND_INDEX`, then `$SYSAND_DEFAULT_INDEX` (else `https://sysand.com`);
each is a comma-separated list, an empty entry is an error (`hint: check for a stray `,``), and
`-no-index` consults none. The first index whose `versions.json` exists for the project decides.

An index location is an absolute `http(s)` URL without userinfo or fragment, a local directory,
or a URL template holding exactly one `{path}` (the index-relative path percent-encoded as one
segment, `/` → `%2F`, for file APIs like GitLab's) or `{path_raw}` (literal `/`). Discovery
fetches `<location>/sysand-index-config.json` first: absent (404) means the location itself is
the index root; present, its `index_root` (URL or template) is where the files live and
`api_root` (plain URL) is recorded for diagnostics and never called — SysPI has no publish or
whoami. Relative, non-HTTP(S) or userinfo-bearing values are errors.

`versions.json` is validated as sysand validates it: every `version` a semantic version without
build metadata, listed once, in descending precedence (`10.0.0-beta.1` before `10.0.0` is an
error), `kpar_digest` = `sha256:<64-hex>`. Entries with `status: "yanked"` or `"removed"` are
never newly selected; re-fetching a locked version that is now `removed` is a hard error, a
`yanked` one still downloads (its files are served). The lock's `source` is the resolved
`project.kpar` URL.

## Bundle reproducibility

`syspi bundle` writes members sorted with fixed 1980-01-01 ZIP timestamps and regenerates
`.meta.json`. Its `created` time is `SOURCE_DATE_EPOCH` when set, otherwise the commit time of the
checked-out git or hg revision, otherwise the current time; so a bundle of an unchanged checkout
is byte-identical across runs. The output file is never a member of itself when written inside
the project.

## Credentials

SysPI shells out to the user's `git`, `svn` and `hg` with the user's environment, so their
credential helpers, SSH agents, hgrc `[auth]` sections and svn auth caches apply unchanged; its
own HTTP downloads (index files, `.kpar` URLs) take credentials in this order:

1. sysand's environment grammar, read verbatim so a sysand user's CI variables work unchanged:
   `SYSAND_CRED_<LABEL>` is a URL glob (`*`, `?` within one path segment, `**` across, `[...]`,
   `{a,b}`), paired with `SYSAND_CRED_<LABEL>_BEARER_TOKEN` or with
   `SYSAND_CRED_<LABEL>_BASIC_USER` and `SYSAND_CRED_<LABEL>_BASIC_PASS`. A group missing its
   pattern, half of a Basic pair, both schemes or a label is an error naming the variable. A URL
   that matches is requested without credentials first and, on any 4xx except 429 (some hosts
   answer 404 to anonymous requests for protected files), retried with the credential; when
   several globs match, a warning lists them and each is tried in label order until one answers
   non-4xx, else the first credentialed response stands. On a redirect the credential is sent only
   to a target on the same host.
2. the netrc file (`$NETRC`, else `~/.netrc` / `%USERPROFILE%\_netrc`) as HTTP Basic, matched by
   host and sent only to that host; a redirect elsewhere gets that host's entry or none.
3. anonymous.

Proxies come from the environment through `http.DefaultTransport`. Not implemented: sysand's
keyring credential store and `sysand auth login/logout/whoami` (their blob format is theirs; use
the variables above or netrc), `sysand publish`.

Deliberately not done: no prompting (`GIT_TERMINAL_PROMPT=0`, `svn --non-interactive`), no
credential storage or cache of SysPI's own, no credentials in URLs (`https://user:pw@…` is
refused), nothing credential-like in `.project.json`, the lock or the output (URL user-info is
redacted from error text).

Authentication failures carry one hint each: git `could not read Username` / `Authentication
failed` → configure a credential helper or use an SSH remote; svn `E170001` / `E215004` → run
`svn ls <url>` once interactively so svn caches the credentials (on builds without plaintext
password storage, Debian and Ubuntu among them, the cache holds only the username — use
`svn+ssh://` or a keyring store); hg `authorization failed` / `http error 401` → add an `[auth]`
section to hgrc (`HGPLAIN=1` does not disable it); HTTP 401 / 403 → a `SYSAND_CRED_*` group
matching the URL or a netrc entry for the host.

`tests/e2e/auth_test.go` proves each path against local servers: git over dumb HTTP behind Basic
auth with a credential helper configured through `GIT_CONFIG_*`, hg over `static-http://` with an
`[auth]` section in `HGRCPATH`, svn against `svnserve` with `anon-access = none` and a seeded
auth cache, and the index and `.kpar` downloads behind Basic auth with a netrc.
`tests/e2e/sysand_test.go` proves the sysand side: a discovery document whose `index_root` is a
second server, bearer and Basic `SYSAND_CRED_*` groups (anonymous request first), an unmatched
glob ending in 401 with a hint naming both mechanisms, a credential dropped on a cross-host
redirect, and `SYSAND_INDEX` / `SYSAND_DEFAULT_INDEX` picked up with no index configured.

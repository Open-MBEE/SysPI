# Packages, identities, locks

## Identity grammar

A repository package is identified by an ECMA-427 Package-URL (Clause 5: VCS URLs are not purl
types and go in a qualifier) of type `generic` carrying the Annex B.3.5 `vcs_url` qualifier:

```
resource   = "pkg:generic/" name "?vcs_url=" encode(vcs_url)
vcs_url    = vcs "+" transport "://" [ user "@" ] host "/" path [ "@" version ] [ "#" subpath ]
vcs        = "git" | "svn" | "hg"
transport  = git: "git" | "https" | "http" | "ssh"      (Annex B.3.5; "git" and "git+git" are one)
             hg:  "http" | "https" | "static-http" | "ssh"
             svn: "svn" | "http" | "https" | "ssh"      ("svn" and "svn+svn" are one)
             any: "file"   (a repository on this machine, pip's notation; host empty, path absolute)
name       = last segment of path, a ".git" suffix stripped
user       = SSH login, on transport "ssh" only; as written (case kept), never with a password
host       = DNS name (lower-cased), with an optional port
path       = repository path on the host as written (a ".git" suffix stays), without a leading "/"
version    = semantic version (MAJOR.MINOR.PATCH[-pre][+build]) naming a tag; an exact constraint
subpath    = directory within the tagged tree that is the project directory
```

`encode` is the purl canonical percent-encoding: every byte outside `A-Z a-z 0-9 - . _ ~` and
`:` becomes `%XX` (`+` → `%2B`, `/` → `%2F`, `@` → `%40`, `#` → `%23`); any valid
percent-encoding is accepted on input. Examples:

- `pkg:generic/OpenSysML-Extensions-Library?vcs_url=git%2Bhttps:%2F%2Fgithub.com%2FOpen-MBEE%2FOpenSysML-Extensions-Library%401.0.0`
- `pkg:generic/OpenSysML-Extensions-Library?vcs_url=git%2Bssh:%2F%2Fgit%40github.com%2FOpen-MBEE%2FOpenSysML-Extensions-Library%401.0.0`
- `pkg:generic/rover?vcs_url=hg%2Bhttps:%2F%2Fhg.example.org%2Fmodels%2Frover%23packages%2Fpower`
- `pkg:generic/rover?vcs_url=svn%2Bsvn:%2F%2Fsvn.example.org%2Frepos%2Frover%401.0.0`
- `pkg:generic/rover?vcs_url=git%2Bfile:%2F%2F%2Fsrv%2Fgit%2Frover.git%401.0.0`

Two input spellings normalise to it (and `syspi add` writes the canonical form into
`.project.json`): `pkg:git/<host>/<path>[@<version>][#<subpath>]`, meaning `git+https`; and the
bare vcs_url notation `git+https://…`, `git+ssh://[<user>@]…`, `git://…`, `hg+https://…`,
`hg+static-http://…`, `svn://…`, `svn+https://…`, `svn+ssh://…`, `<vcs>+file:///…`, each with an
optional `@<version>` and `#<subpath>`. A repository on a Windows machine is
`<vcs>+file:///C:/path` (RFC 8089); `<vcs>+file://C:/path` is accepted as input and normalised to
it, and the same two forms name a local `.kpar` or project directory (`file:///C:/tools.kpar`).
On other systems `/C:/path` is an ordinary directory named `C:`. The version-free identity (`vcs_url` without `@<version>`)
is what the lock records as `resource`; what the VCS executable is given is kept as `source`
(`git+ssh` stays SSH; the path is cloned exactly as written, so `…/repo` and `…/repo.git` are
two locations and the one your server serves is the one to write).

Refused, each with a message naming the three accepted spellings: `pkg:svn/…` and `pkg:hg/…`
(not Package-URL types), `pkg:github/…`, `pkg:gitlab/…` and other provider vocabularies (the
host is part of the location), plain clone URLs as identities (`https://host/path.git@1.0.0`,
`ssh://…`, `git@host:path`, `file:///path@1.0.0`, `/path@1.0.0`), a password in the host on any
transport and a user name in the host on any transport but `ssh` (Annex B.3.5; see below), a
transport outside the lists above, `bzr` or any other VCS, a commit id or
`@latest` as version. A plain `http(s)` IRI is only ever dereferenced as a project interchange
file — a `.kpar` — as KerML 1.1 §10.3 Table 12 describes `resource`; it is never cloned, and
`user:password@` in it is refused (credentials belong in the netrc, never in `.project.json`).

## The user name of an SSH location

`git+ssh://git@github.com/owner/repo@1.0.0`, `hg+ssh://hg@host/repo` and `svn+ssh://svn@host/repo`
are accepted: on the `ssh` transport the part before `@` is the login the server expects, the
same for everyone, and not a secret. Annex B.3.5 (informative) says "Using user names and password
in the host_name is not supported and should be reported by tools as an error", and in the same
clause that its syntax is pip's, whose own examples are `git+ssh://git@git.example.com/MyProject`
and `svn+ssh://user@svn.example.com/MyProject`. SysPI follows that recommendation wherever the
user name would be a credential and departs from it for this one case:

| written | result |
| --- | --- |
| `<vcs>+ssh://user@host/path` | accepted |
| `<vcs>+ssh://user:password@host/path`, `user:@host` | refused: a password |
| `<vcs>+<any other transport>://user@host/path` | refused: there a user name is a credential (a token) |
| `pkg:git/user@host/path` | refused: it means `git+https://user@host/path` |
| `git@host:path` (scp style) | refused: not a URL; the message names `git+ssh://git@host/path` |

The alias and the bare notation agree: both are the same `vcs_url` once normalised, and the one
rule above is applied to that `vcs_url`. An SSH location is therefore always written as
`git+ssh://…` or as the canonical `pkg:generic/…?vcs_url=git%2Bssh:…`; `pkg:git/…` has no SSH form.

**The user name is part of the identity.** RFC 3986 §3.2 makes userinfo a component of the
authority, and neither its normalisation rules (§6) nor Annex B.3.5 drop it, so SysPI keeps it:

- `.project.json` and the lock's `resource` carry it inside the percent-encoded `vcs_url`, the
  `@` as `%40` like every other one:
  `pkg:generic/repo?vcs_url=git%2Bssh:%2F%2Fgit%40github.com%2Fowner%2Frepo`. The lock's `source`
  is `ssh://git@github.com/owner/repo`, and that is the address the VCS executable is given
  (`git ls-remote --tags -- ssh://git@github.com/owner/repo`).
- The user name is kept as written: its case is significant, and a character outside RFC 3986's
  userinfo set stays percent-encoded in the address (`first.last%40corp@host`, hence `%2540` in
  the `resource`). An empty user name (`git+ssh://@host/path`) is none. One starting with `-` is
  refused, since `ssh` would read it as an option.
- `git+ssh://git@host/owner/repo` and `git+ssh://host/owner/repo` are **two identities**, exactly
  as `git+https://host/owner/repo` and `git+ssh://host/owner/repo` are: SysPI cannot know that
  the login `ssh_config` supplies for the second is the `git` of the first. A usage of one never
  matches a lock entry of the other; each is resolved through its own address and locked under
  its own `resource`. Changing a usage from one spelling to the other is changing the identity:
  the next `install` resolves it anew over the network (`-offline` refuses, as for any identity
  the lock does not hold) and replaces the lock entry.
- The installed directory does not carry it: `lib/<publisher>-<name>_<version>` is named from the
  package's own `.project.json`, never from the location. Two usages that differ only in the user
  name — in one project, or one in the project and one in a dependency — therefore resolve to the
  same directory, are locked as two entries with the same `revision` and `sha256`, and are
  installed once. Were the two addresses to serve different contents for that publisher, name and
  version, the run fails naming both (as for any two packages that collide on a directory)
  instead of letting one overwrite the other. `lib/` is matched to a usage only through the
  lock: an entry whose `resource` is the usage's identity, and an installed tree whose digest is
  that entry's `sha256`.

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
      "resource": "pkg:generic/sysmod-sysmlv2?vcs_url=git%2Bhttps:%2F%2Fgithub.com%2FMBSE4U%2Fsysmod-sysmlv2",
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
        { "resource": "pkg:generic/core?vcs_url=git%2Bssh:%2F%2Fprivate.example%2Fteam%2Fcore", "versionConstraint": "^1.0.0" }
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
the convention OpenSysML reads.

## Bundle reproducibility

`syspi bundle` writes members sorted with fixed 1980-01-01 ZIP timestamps and regenerates
`.meta.json`. Its `created` time is `SOURCE_DATE_EPOCH` when set, otherwise the commit time of the
checked-out git or hg revision, otherwise the current time; so a bundle of an unchanged checkout
is byte-identical across runs. The output file is never a member of itself when written inside
the project.

## Credentials

SysPI shells out to the user's `git`, `svn` and `hg` with the user's environment, so their
credential helpers, SSH agents, hgrc `[auth]` sections and svn auth caches apply unchanged. For
svn over `svn://` or `http(s)://` it additionally reads the netrc (same lookup as below) for the
repository host and, when an entry exists and svn is 1.14 or newer, runs
`svn --username <login> --password-from-stdin --no-auth-cache …` with the password on stdin —
never an argument, never in the environment, never written to `~/.subversion/auth`; an older svn
runs as before and the hint points at the cache or `svn+ssh://`. Debian and Ubuntu build svn
without plaintext password storage, so on CI boxes and containers the netrc is the way. Its own
HTTP downloads (`.kpar` URLs) take HTTP Basic from the netrc file (`$NETRC`, else `~/.netrc` /
`%USERPROFILE%\_netrc`), matched by host and sent only to that host; a redirect elsewhere gets
that host's own `machine` entry or none — the `default` entry never follows a redirect. URLs in
diagnostics are shown without user info or query.

Proxies come from the environment through `http.DefaultTransport`.

Deliberately not done: no prompting (`GIT_TERMINAL_PROMPT=0`, `svn --non-interactive`), no
credential storage or cache of SysPI's own, no credentials in URLs (`https://user:pw@…` is
refused), nothing credential-like in `.project.json`, the lock or the output (URL user-info is
redacted from error text).

Authentication failures carry one hint each: git `could not read Username` / `Authentication
failed` → configure a credential helper or use an SSH remote; svn `E170001` / `E215004` → a
netrc entry for the host (svn 1.14+), or `svn ls <url>` once interactively so svn caches the
credentials (on builds without plaintext password storage, Debian and Ubuntu among them, the
cache holds only the username), or `svn+ssh://`; hg `authorization failed` / `http error 401` → add an `[auth]`
section to hgrc (`HGPLAIN=1` does not disable it); HTTP 401 / 403 → a netrc entry for the host.

`tests/e2e/auth_test.go` proves each path against local servers: git over dumb HTTP behind Basic
auth with a credential helper configured through `GIT_CONFIG_*`, hg over `static-http://` with an
`[auth]` section in `HGRCPATH`, svn against `svnserve` with `anon-access = none` and a netrc
entry (no auth cache written, wrong password not echoed, password not in argv), and a `.kpar`
download behind Basic auth with a netrc.

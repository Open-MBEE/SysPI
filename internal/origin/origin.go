// SPDX-License-Identifier: Apache-2.0

// Package origin parses the resource IRIs a project may use (KerML 1.1
// §10.3 Table 12) and turns each into a canonical package identity.
//
// A version-controlled package is identified by an ECMA-427 Package-URL of
// type generic carrying the Annex B.3.5 vcs_url qualifier:
//
//	pkg:generic/<name>?vcs_url=<vcs>+<transport>://<host>/<path>[@<version>][#<subpath>]
//
// with the qualifier value percent-encoded. pkg:git/<host>/<path>[@v][#sub]
// and the bare vcs_url notation are accepted as input and normalised to it.
package origin

import (
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Open-MBEE/SysPI/internal/version"
)

// Kind classifies where a package comes from.
type Kind int

const (
	// VCS is a tagged tree in a version-control repository (git, svn or hg).
	VCS Kind = iota
	// LocalDir is a project directory on this machine, used in place (editable).
	LocalDir
	// LocalArchive is a .kpar file on this machine.
	LocalArchive
	// RemoteArchive is a .kpar file fetched over http(s).
	RemoteArchive
	// Standard is an OMG standard-library .kpar IRI; every reader bundles those,
	// so they are recorded but never installed.
	Standard
)

// String names the kind for messages.
func (k Kind) String() string {
	switch k {
	case VCS:
		return "vcs"
	case LocalDir:
		return "local directory"
	case LocalArchive:
		return "local .kpar"
	case RemoteArchive:
		return "remote .kpar"
	case Standard:
		return "standard library"
	}
	return fmt.Sprintf("kind(%d)", int(k))
}

// SupportedVCS lists the version-control systems a package may live in, in
// the order messages name them.
var SupportedVCS = []string{"git", "svn", "hg"}

// transports are the vcs_url schemes of ECMA-427 Annex B.3.5 per tool, plus
// file for a repository on this machine (pip's git+file notation).
var transports = map[string]map[string]bool{
	"git": {"git": true, "https": true, "http": true, "ssh": true, "file": true},
	"hg":  {"http": true, "https": true, "static-http": true, "ssh": true, "file": true},
	"svn": {"svn": true, "http": true, "https": true, "ssh": true, "file": true},
}

// Spellings names the three accepted ways to write a repository package.
const Spellings = "pkg:generic/<name>?vcs_url=<vcs>%2B<transport>:%2F%2F<host>%2F<path>[%40<version>][%23<subpath>], " +
	"pkg:git/<host>/<path>[@<version>][#<subpath>], or <vcs>+<transport>://<host>/<path>[@<version>][#<subpath>] " +
	"(vcs: git, svn, hg; transports per ECMA-427 Annex B.3.5, plus file for a repository on this machine)"

// Origin is a parsed resource.
type Origin struct {
	Kind Kind
	// VCS and Transport are the vcs_url scheme parts for Kind == VCS.
	VCS, Transport string
	// Host is the repository host (empty for a repository on this machine).
	Host string
	// Path is the repository path on the host as written, without a leading
	// slash; for a repository on this machine it is the absolute path.
	Path string
	// Subpath is the project directory within the repository, "" for the root.
	Subpath string
	// Version is the exact version named in the resource, "" when none.
	Version string
	// Name is the purl name: the last segment of Path without a .git suffix.
	Name string
	// Source is what the VCS executable is given, or the archive path/URL, or
	// the directory for LocalDir, or the IRI for Standard.
	Source string
	// Resource is the resource exactly as written.
	Resource string
}

// VCSURL is the Annex B.3.5 location: <vcs>+<transport>://<host>/<path>[@v][#sub].
func (o *Origin) VCSURL(withVersion bool) string {
	var b strings.Builder
	b.WriteString(o.VCS)
	b.WriteByte('+')
	b.WriteString(o.Transport)
	b.WriteString("://")
	b.WriteString(o.Host)
	b.WriteByte('/')
	b.WriteString(strings.TrimPrefix(o.Path, "/"))
	if withVersion && o.Version != "" {
		b.WriteByte('@')
		b.WriteString(o.Version)
	}
	if o.Subpath != "" {
		b.WriteByte('#')
		b.WriteString(o.Subpath)
	}
	return b.String()
}

// Identity is the canonical, version-free identity of the package:
// pkg:generic/<name>?vcs_url=… for a repository, otherwise the source.
func (o *Origin) Identity() string {
	if o.Kind != VCS {
		return o.Source
	}
	return "pkg:generic/" + Encode(o.Name) + "?vcs_url=" + Encode(o.VCSURL(false))
}

// Key is the identity with a .git suffix dropped from the repository path:
// what a lock entry and a usage are matched by, however the suffix was spelled.
func (o *Origin) Key() string {
	if o.Kind != VCS || !strings.HasSuffix(o.Path, ".git") {
		return o.Identity()
	}
	k := *o
	k.Path = strings.TrimSuffix(k.Path, ".git")
	return k.Identity()
}

// Canonical is the identity with the exact version inside vcs_url when one
// was named.
func (o *Origin) Canonical() string {
	if o.Kind != VCS {
		return o.Source
	}
	return "pkg:generic/" + Encode(o.Name) + "?vcs_url=" + Encode(o.VCSURL(true))
}

// Encode percent-encodes a purl component the way the canonical purl form
// does: every byte outside A-Z a-z 0-9 - . _ ~ and ':' becomes %XX.
func Encode(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case 'A' <= c && c <= 'Z', 'a' <= c && c <= 'z', '0' <= c && c <= '9', c == '-', c == '.', c == '_', c == '~', c == ':':
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		}
	}
	return b.String()
}

var (
	providerTypes = map[string]bool{"github": true, "gitlab": true, "bitbucket": true, "gitea": true, "codeberg": true, "sourceforge": true}
	scpLike       = regexp.MustCompile(`^([A-Za-z0-9._~-]+@)?([A-Za-z0-9.-]+):([^/].*)$`)
	vcsScheme     = regexp.MustCompile(`^(git|hg|svn|bzr|cvs|fossil|darcs)(\+[a-z-]+)?://`)
	standardHost  = "www.omg.org"
)

// Parse classifies and normalises a resource IRI.
func Parse(resource string) (*Origin, error) {
	res := strings.TrimSpace(resource)
	if res == "" {
		return nil, fmt.Errorf("empty resource")
	}
	if rest, ok := strings.CutPrefix(res, "pkg:"); ok {
		return parsePkg(res, rest)
	}
	if vcsScheme.MatchString(res) {
		return parseVCSURL(res, res)
	}
	if strings.HasPrefix(res, "http://") || strings.HasPrefix(res, "https://") {
		return parseHTTP(res)
	}
	if rest, ok := strings.CutPrefix(res, "file://"); ok {
		rest, ok = fileURIPath(rest)
		if !ok {
			return nil, fmt.Errorf("resource %q: file URIs must name a path on this machine (file:///path)", res)
		}
		return parseLocal(res, rest)
	}
	if strings.Contains(res, "://") {
		return nil, fmt.Errorf("resource %q: unsupported URL scheme; a repository is written as %s", res, Spellings)
	}
	if m := scpLike.FindStringSubmatch(res); m != nil && !filepath.IsAbs(res) && !strings.HasPrefix(res, ".") {
		return nil, fmt.Errorf("resource %q: scp-style clone addresses are not identities; write git+ssh://%s/%s or another of %s", res, m[2], strings.TrimSuffix(m[3], ".git"), Spellings)
	}
	return parseLocal(res, res)
}

func parsePkg(res, rest string) (*Origin, error) {
	typ, tail, _ := strings.Cut(rest, "/")
	typ = strings.ToLower(typ)
	switch {
	case typ == "generic":
		return parseGeneric(res, tail)
	case typ == "git":
		return parseGitAlias(res, tail)
	case typ == "svn" || typ == "hg":
		return nil, fmt.Errorf("resource %q: pkg:%s is not a Package-URL type; write %s", res, typ, Spellings)
	case providerTypes[typ]:
		return nil, fmt.Errorf("resource %q: pkg:%s is not a package type here; the host is part of the identity — write pkg:git/%s.com/%s or %s", res, typ, typ, strings.TrimSuffix(tail, "/"), Spellings)
	default:
		return nil, fmt.Errorf("resource %q: pkg:%s is not supported; write %s", res, typ, Spellings)
	}
}

// parseGeneric handles pkg:generic/<name>[@<version>]?vcs_url=<encoded>.
func parseGeneric(res, tail string) (*Origin, error) {
	namePart, query, hasQuery := strings.Cut(tail, "?")
	if !hasQuery {
		return nil, fmt.Errorf("resource %q: pkg:generic needs a vcs_url qualifier; write %s", res, Spellings)
	}
	q, err := url.ParseQuery(strings.ReplaceAll(query, "+", "%2B"))
	if err != nil {
		return nil, fmt.Errorf("resource %q: invalid qualifiers: %v", res, err)
	}
	for k := range q {
		if k != "vcs_url" {
			return nil, fmt.Errorf("resource %q: qualifier %q is not supported; only vcs_url is", res, k)
		}
	}
	if len(q["vcs_url"]) != 1 || q["vcs_url"][0] == "" {
		return nil, fmt.Errorf("resource %q: pkg:generic needs exactly one vcs_url qualifier", res)
	}
	o, err := parseVCSURL(res, q["vcs_url"][0])
	if err != nil {
		return nil, err
	}
	if strings.Contains(namePart, "/") {
		return nil, fmt.Errorf("resource %q: pkg:generic takes no namespace; the name is the repository's last path segment", res)
	}
	name, ver, _ := strings.Cut(namePart, "@")
	name, err = url.PathUnescape(name)
	if err != nil || name == "" {
		return nil, fmt.Errorf("resource %q: invalid purl name", res)
	}
	if name != o.Name {
		return nil, fmt.Errorf("resource %q: purl name %q must be the repository's last path segment %q", res, name, o.Name)
	}
	if ver != "" {
		if _, err := version.Parse(ver); err != nil {
			return nil, fmt.Errorf("resource %q: %q is not a semantic version", res, ver)
		}
		if o.Version != "" && o.Version != ver {
			return nil, fmt.Errorf("resource %q: version %s in the purl and %s in vcs_url disagree", res, ver, o.Version)
		}
		o.Version = ver
	}
	o.Resource = res
	return o, nil
}

// parseGitAlias handles pkg:git/<host>/<path>[@<version>][#<subpath>] as
// git+https://<host>/<path>.
func parseGitAlias(res, tail string) (*Origin, error) {
	if strings.HasPrefix(tail, "/") {
		abs, _ := filepath.Abs(filepath.FromSlash("/" + strings.TrimLeft(tail, "/")))
		return nil, fmt.Errorf("resource %q: pkg:git needs a host; a repository on this machine is git+file://%s", res, FileURIPath(abs))
	}
	if tail == "" || !strings.Contains(tail, "/") {
		return nil, fmt.Errorf("resource %q: expected pkg:git/<host>/<path>[@<version>][#<subpath>]", res)
	}
	return parseVCSURL(res, "git+https://"+tail)
}

// parseVCSURL handles <vcs>[+<transport>]://<host>/<path>[@<version>][#<subpath>].
func parseVCSURL(res, raw string) (*Origin, error) {
	spec, sub := cutFragment(raw)
	spec, ver, err := cutVersion(res, spec)
	if err != nil {
		return nil, err
	}
	scheme, rest, ok := strings.Cut(spec, "://")
	if !ok {
		return nil, fmt.Errorf("resource %q: %q is not a vcs_url (%s)", res, raw, Spellings)
	}
	vcs, transport, _ := strings.Cut(strings.ToLower(scheme), "+")
	if transport == "" {
		transport = vcs // git and git+git, svn and svn+svn are equivalent
	}
	if transports[vcs] == nil {
		return nil, fmt.Errorf("resource %q: version-control system %q is not supported; supported: %s", res, vcs, strings.Join(SupportedVCS, ", "))
	}
	if !transports[vcs][transport] {
		return nil, fmt.Errorf("resource %q: %s+%s is not a vcs_url scheme for %s (ECMA-427 Annex B.3.5 allows %s)", res, vcs, transport, vcs, allowed(vcs))
	}
	if transport == "file" {
		// file:///C:/path is the RFC 8089 form; file://C:/path is accepted as input.
		if drive.MatchString(rest) {
			rest = "/" + rest
		}
	}
	u, err := url.Parse(transport + "://" + rest)
	if err != nil {
		return nil, fmt.Errorf("resource %q: not a valid URL: %v", res, err)
	}
	if u.User != nil {
		return nil, fmt.Errorf("resource %q: a user name or password in the host is not supported (ECMA-427 Annex B.3.5); use the credentials of your %s configuration", res, vcs)
	}
	if u.RawQuery != "" {
		return nil, fmt.Errorf("resource %q: a vcs_url must not carry a query", res)
	}
	o := &Origin{Kind: VCS, VCS: vcs, Transport: transport, Subpath: sub, Version: ver, Resource: res}
	if transport == "file" {
		if u.Host != "" || !strings.HasPrefix(u.Path, "/") {
			return nil, fmt.Errorf("resource %q: a repository on this machine is %s+file:///absolute/path", res, vcs)
		}
		o.Path = u.Path
		if drive.MatchString(o.Path[1:]) {
			o.Path = o.Path[1:]
		}
		o.Path = filepath.ToSlash(filepath.Clean(o.Path))
		o.Name = strings.TrimSuffix(path.Base(o.Path), ".git")
		switch vcs {
		case "svn":
			o.Source = "file://" + FileURIPath(o.Path)
		default:
			o.Source = filepath.FromSlash(o.Path)
		}
		return o, nil
	}
	if u.Host == "" {
		return nil, fmt.Errorf("resource %q: repository URL has no host", res)
	}
	p := strings.Trim(u.Path, "/")
	if p == "" {
		return nil, fmt.Errorf("resource %q: repository URL has no path", res)
	}
	if strings.Contains(p, "..") {
		return nil, fmt.Errorf("resource %q: repository path must not contain \"..\"", res)
	}
	o.Host, o.Path, o.Name = strings.ToLower(u.Host), p, strings.TrimSuffix(path.Base(p), ".git")
	o.Source = source(vcs, transport, o.Host, p)
	return o, nil
}

func allowed(vcs string) string {
	var out []string
	for _, t := range []string{"git", "svn", "http", "https", "static-http", "ssh", "file"} {
		if transports[vcs][t] {
			out = append(out, vcs+"+"+t)
		}
	}
	return strings.Join(out, ", ")
}

// source is the address the VCS executable is given for a vcs_url.
func source(vcs, transport, host, p string) string {
	switch vcs {
	case "svn":
		if transport == "ssh" {
			return "svn+ssh://" + host + "/" + p
		}
		return transport + "://" + host + "/" + p
	}
	return transport + "://" + host + "/" + p // hg
}

func parseHTTP(res string) (*Origin, error) {
	spec, sub := cutFragment(res)
	spec, ver, err := cutVersion(res, spec)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(spec)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("resource %q: not a valid URL", res)
	}
	if u.User != nil {
		return nil, fmt.Errorf("resource %q: credentials in an http(s) IRI are not supported; add a netrc entry for %s instead and keep the IRI bare", res, u.Hostname())
	}
	if !strings.HasSuffix(strings.ToLower(u.Path), ".kpar") {
		return nil, fmt.Errorf("resource %q: an http(s) IRI is only dereferenced as a project interchange file (.kpar); a repository is written as %s", res, Spellings)
	}
	if sub != "" {
		return nil, fmt.Errorf("resource %q: a .kpar cannot have a #<subpath>", res)
	}
	kind := RemoteArchive
	if strings.EqualFold(u.Host, standardHost) {
		kind = Standard
	}
	return &Origin{Kind: kind, Source: spec, Version: ver, Resource: res}, nil
}

// drive matches the start of a Windows path, C:/ or C:\.
var drive = regexp.MustCompile(`^[A-Za-z]:[/\\]`)

// FileURIPath is the path part of a file:// URI naming abs: /C:/dir on Windows, /dir elsewhere.
func FileURIPath(abs string) string {
	abs = filepath.ToSlash(abs)
	if !strings.HasPrefix(abs, "/") {
		abs = "/" + abs
	}
	return abs
}

// fileURIPath turns what follows file:// into a path on this machine; a host is refused.
func fileURIPath(rest string) (string, bool) {
	if drive.MatchString(rest) {
		return rest, true
	}
	if !strings.HasPrefix(rest, "/") {
		return "", false
	}
	if drive.MatchString(rest[1:]) {
		return rest[1:], true
	}
	return rest, true
}

// parseLocal handles file:// URIs and plain paths: a .kpar is a local
// archive, anything else an editable project directory.
func parseLocal(res, p string) (*Origin, error) {
	p, sub := cutFragment(p)
	p, ver, err := cutVersion(res, p)
	if err != nil {
		return nil, err
	}
	if p == "" {
		return nil, fmt.Errorf("resource %q: empty path", res)
	}
	if strings.HasSuffix(strings.ToLower(p), ".kpar") {
		if sub != "" {
			return nil, fmt.Errorf("resource %q: a .kpar cannot have a #<subpath>", res)
		}
		return &Origin{Kind: LocalArchive, Source: p, Version: ver, Resource: res}, nil
	}
	if ver != "" {
		abs, _ := filepath.Abs(p)
		return nil, fmt.Errorf("resource %q: a path with @<version> is not an identity; a repository on this machine is git+file://%s@%s (or svn+file, hg+file)", res, FileURIPath(abs), ver)
	}
	if sub != "" {
		p = path.Join(p, sub)
	}
	return &Origin{Kind: LocalDir, Source: p, Resource: res}, nil
}

func cutFragment(s string) (string, string) {
	spec, frag, ok := strings.Cut(s, "#")
	if !ok {
		return s, ""
	}
	frag = strings.Trim(path.Clean("/"+frag), "/")
	if frag == "." {
		frag = ""
	}
	return spec, frag
}

func isHex(s string) bool {
	return s != "" && strings.Trim(strings.ToLower(s), "0123456789abcdef") == ""
}

// cutVersion splits a trailing @<version> when <version> parses as semver;
// a commit id or any other reference after @ is an error that says so.
func cutVersion(res, s string) (string, string, error) {
	i := strings.LastIndexByte(s, '@')
	if i < 0 {
		return s, "", nil
	}
	v := s[i+1:]
	if v == "" {
		return "", "", fmt.Errorf("resource %q: empty version after @", res)
	}
	if strings.ContainsAny(v, "/:") {
		return s, "", nil
	}
	if _, err := version.Parse(v); err != nil {
		if isHex(v) && (len(v) == 40 || len(v) == 64 || (len(v) >= 7 && len(v) <= 12)) {
			return "", "", fmt.Errorf("resource %q: %q after @ looks like a commit id; a vcs_url revision may be any reference, but SysPI resolves only a tag naming a semantic version (v<semver> or <semver>, e.g. @1.2.0)", res, v)
		}
		return "", "", fmt.Errorf("resource %q: %q after @ is not a semantic version (SysPI versions are tags naming a semantic version, e.g. @1.2.0)", res, v)
	}
	return s[:i], v, nil
}

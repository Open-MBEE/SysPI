// SPDX-License-Identifier: Apache-2.0

// Package origin parses the resource IRIs a project may use and turns each
// into a canonical package identity.
//
// The canonical identity of a version-controlled package is
//
//	pkg:<vcs>/<host>/<path>[@<version>][#<subpath>]
//
// with <vcs> one of git, svn or hg. The grammar is hand-parsed here; pkg:git,
// pkg:svn, pkg:hg and pkg:sysand are not registered purl types and this
// package makes no attempt to be a purl parser. Plain clone URLs
// (https://host/path.git@1.0.0, ssh://git@host/path@1.0.0, git@host:path@1.0.0,
// file:///path@1.0.0) are accepted and normalised to the pkg:<vcs> form.
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
	// Index is pkg:sysand/<publisher>/<name>, resolved through a static index.
	Index
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
	case Index:
		return "sysand index"
	}
	return fmt.Sprintf("kind(%d)", int(k))
}

// SupportedVCS lists the version-control systems a package may live in, in
// the order messages name them.
var SupportedVCS = []string{"git", "svn", "hg"}

// Origin is a parsed resource.
type Origin struct {
	Kind Kind
	// VCS is git, svn or hg for Kind == VCS.
	VCS string
	// Host is the repository host (empty for a repository on this machine).
	Host string
	// Path is the repository path on the host, without a leading slash and
	// without a .git suffix; for a local repository it is the absolute path.
	Path string
	// Subpath is the project directory within the repository, "" for the root.
	Subpath string
	// Version is the exact version named in the resource, "" when none.
	Version string
	// Source is the URL the VCS executable is given. For Kind == VCS it is the
	// clone URL as the user wrote it, or the https form derived from a pkg:
	// identity. For archives it is the file path or URL; for LocalDir the
	// directory; for Index and Standard the resource itself.
	Source string
	// Publisher and Name are set for Kind == Index only.
	Publisher, Name string
	// Resource is the resource exactly as written.
	Resource string
}

// Identity is the canonical, version-free identity of the package:
// pkg:<vcs>/<host>/<path>[#<subpath>] for a repository, otherwise the source.
func (o *Origin) Identity() string {
	if o.Kind != VCS {
		if o.Kind == Index {
			return "pkg:sysand/" + o.Publisher + "/" + o.Name
		}
		return o.Source
	}
	var b strings.Builder
	b.WriteString("pkg:")
	b.WriteString(o.VCS)
	b.WriteByte('/')
	b.WriteString(o.Host)
	b.WriteByte('/')
	b.WriteString(strings.TrimPrefix(o.Path, "/"))
	if o.Subpath != "" {
		b.WriteByte('#')
		b.WriteString(o.Subpath)
	}
	return b.String()
}

// Canonical is the identity with the exact version appended when one was named.
func (o *Origin) Canonical() string {
	id := o.Identity()
	if o.Version == "" || o.Kind == Standard || o.Kind == LocalArchive || o.Kind == RemoteArchive {
		return id
	}
	if o.Kind == VCS {
		// The version sits before the fragment: pkg:git/h/p@1.0.0#sub.
		if i := strings.IndexByte(id, '#'); i >= 0 {
			return id[:i] + "@" + o.Version + id[i:]
		}
	}
	return id + "@" + o.Version
}

var (
	// providerTypes are purl-style provider vocabularies this tool refuses:
	// the host is part of the identity, not the type.
	providerTypes = map[string]bool{"github": true, "gitlab": true, "bitbucket": true, "gitea": true, "codeberg": true, "sourceforge": true}
	scpLike       = regexp.MustCompile(`^([A-Za-z0-9._~-]+@)?([A-Za-z0-9.-]+):([^/].*)$`)
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
	if strings.HasPrefix(res, "http://") || strings.HasPrefix(res, "https://") {
		return parseHTTP(res)
	}
	if strings.HasPrefix(res, "ssh://") || strings.HasPrefix(res, "git+ssh://") || strings.HasPrefix(res, "git://") || strings.HasPrefix(res, "svn://") || strings.HasPrefix(res, "svn+ssh://") || strings.HasPrefix(res, "static-http://") || strings.HasPrefix(res, "static-https://") {
		return parseSchemeURL(res)
	}
	if rest, ok := strings.CutPrefix(res, "file://"); ok {
		return parseLocal(res, rest)
	}
	if m := scpLike.FindStringSubmatch(res); m != nil && !filepath.IsAbs(res) && !strings.HasPrefix(res, ".") {
		return parseSCP(res, m)
	}
	if strings.Contains(res, "://") {
		return nil, fmt.Errorf("resource %q: unsupported URL scheme", res)
	}
	return parseLocal(res, res)
}

func parsePkg(res, rest string) (*Origin, error) {
	typ, tail, _ := strings.Cut(rest, "/")
	typ = strings.ToLower(typ)
	switch {
	case typ == "sysand":
		return parseIndex(res, tail)
	case providerTypes[typ]:
		return nil, fmt.Errorf("resource %q: pkg:%s is not a package type here; the host is part of the identity — write pkg:git/<host>/<path>[@<version>][#<subpath>], e.g. pkg:git/%s.com/%s", res, typ, typ, strings.TrimSuffix(tail, "/"))
	case isSupportedVCS(typ):
		return parseVCS(res, typ, tail)
	default:
		return nil, fmt.Errorf("resource %q: version-control system %q is not supported; supported: %s", res, typ, strings.Join(SupportedVCS, ", "))
	}
}

func isSupportedVCS(v string) bool {
	for _, s := range SupportedVCS {
		if s == v {
			return true
		}
	}
	return false
}

// parseVCS handles the tail after "pkg:<vcs>/": <host>/<path>[@<version>][#<subpath>],
// or an absolute local path when the host is empty (pkg:git//abs/path@1.0.0).
func parseVCS(res, vcs, tail string) (*Origin, error) {
	tail, sub := cutFragment(tail)
	tail, ver, err := cutVersion(res, tail)
	if err != nil {
		return nil, err
	}
	o := &Origin{Kind: VCS, VCS: vcs, Subpath: sub, Version: ver, Resource: res}
	if strings.HasPrefix(tail, "/") {
		// Empty host: a repository on this machine.
		p := strings.TrimSuffix(tail, "/")
		if p == "" {
			return nil, fmt.Errorf("resource %q: empty repository path", res)
		}
		o.Path = filepath.ToSlash(filepath.Clean(p))
		o.Source = o.Path
		if vcs == "svn" {
			// svn addresses a local repository only by URL.
			o.Source = "file://" + o.Path
		}
		return o, nil
	}
	host, p, _ := strings.Cut(tail, "/")
	host = strings.ToLower(host)
	p = strings.TrimSuffix(strings.TrimSuffix(p, "/"), ".git")
	if host == "" || p == "" {
		return nil, fmt.Errorf("resource %q: expected pkg:%s/<host>/<path>[@<version>][#<subpath>]", res, vcs)
	}
	if strings.Contains(p, "..") {
		return nil, fmt.Errorf("resource %q: repository path must not contain \"..\"", res)
	}
	o.Host, o.Path = host, p
	o.Source = "https://" + host + "/" + p
	if vcs == "git" {
		o.Source += ".git"
	}
	return o, nil
}

func parseIndex(res, tail string) (*Origin, error) {
	tail, ver, err := cutVersion(res, tail)
	if err != nil {
		return nil, err
	}
	pub, name, ok := strings.Cut(strings.Trim(tail, "/"), "/")
	if !ok || pub == "" || name == "" || strings.Contains(name, "/") {
		return nil, fmt.Errorf("resource %q: expected pkg:sysand/<publisher>/<name>[@<version>]", res)
	}
	return &Origin{Kind: Index, Publisher: pub, Name: name, Version: ver, Source: "pkg:sysand/" + pub + "/" + name, Resource: res}, nil
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
	if strings.HasSuffix(strings.ToLower(u.Path), ".kpar") {
		if sub != "" {
			return nil, fmt.Errorf("resource %q: a .kpar cannot have a #<subpath>", res)
		}
		kind := RemoteArchive
		if strings.EqualFold(u.Host, standardHost) {
			kind = Standard
		}
		return &Origin{Kind: kind, Source: spec, Version: ver, Resource: res}, nil
	}
	if u.User != nil || u.RawQuery != "" {
		return nil, fmt.Errorf("resource %q: a repository URL must not carry credentials or a query", res)
	}
	p := strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(u.Path, "/"), "/"), ".git")
	if p == "" {
		return nil, fmt.Errorf("resource %q: repository URL has no path", res)
	}
	return &Origin{Kind: VCS, VCS: "git", Host: strings.ToLower(u.Host), Path: p, Subpath: sub, Version: ver, Source: spec, Resource: res}, nil
}

// parseSchemeURL handles ssh://, git://, svn:// and their + variants.
func parseSchemeURL(res string) (*Origin, error) {
	spec, sub := cutFragment(res)
	spec, ver, err := cutVersion(res, spec)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(spec)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("resource %q: not a valid URL", res)
	}
	vcs := "git"
	switch {
	case strings.HasPrefix(u.Scheme, "svn"):
		vcs = "svn"
	case strings.HasPrefix(u.Scheme, "static-http"):
		vcs = "hg" // Mercurial's scheme for a repository on a plain file server
	}
	p := strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(u.Path, "/"), "/"), ".git")
	if p == "" {
		return nil, fmt.Errorf("resource %q: repository URL has no path", res)
	}
	return &Origin{Kind: VCS, VCS: vcs, Host: strings.ToLower(u.Hostname()), Path: p, Subpath: sub, Version: ver, Source: spec, Resource: res}, nil
}

// parseSCP handles git@host:path[.git][@version][#subpath].
func parseSCP(res string, m []string) (*Origin, error) {
	spec, sub := cutFragment(res)
	spec, ver, err := cutVersion(res, spec)
	if err != nil {
		return nil, err
	}
	m = scpLike.FindStringSubmatch(spec)
	if m == nil {
		return nil, fmt.Errorf("resource %q: not a valid scp-style clone address", res)
	}
	p := strings.TrimSuffix(strings.TrimSuffix(m[3], "/"), ".git")
	return &Origin{Kind: VCS, VCS: "git", Host: strings.ToLower(m[2]), Path: p, Subpath: sub, Version: ver, Source: spec, Resource: res}, nil
}

// parseLocal handles file:// URIs and plain paths: a .kpar is a local archive,
// a path with @<version> is a local repository, anything else an editable directory.
func parseLocal(res, p string) (*Origin, error) {
	p, sub := cutFragment(p)
	if strings.HasPrefix(res, "file://") {
		if i := strings.IndexByte(p, '/'); i > 0 {
			return nil, fmt.Errorf("resource %q: file URIs must name a path on this machine (file:///path)", res)
		}
	}
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
		abs, err := filepath.Abs(p)
		if err != nil {
			return nil, fmt.Errorf("resource %q: %v", res, err)
		}
		abs = filepath.ToSlash(abs)
		return &Origin{Kind: VCS, VCS: "git", Path: abs, Subpath: sub, Version: ver, Source: abs, Resource: res}, nil
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

// cutVersion splits a trailing @<version> when <version> parses as semver;
// an @ that introduces something else (git@host, user@) is left alone. An @
// followed by a non-semver string at the end is an error, not silently kept.
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
		// user@host or host:path — not a version.
		return s, "", nil
	}
	if _, err := version.Parse(v); err != nil {
		return "", "", fmt.Errorf("resource %q: %q after @ is not a semantic version", res, v)
	}
	return s[:i], v, nil
}

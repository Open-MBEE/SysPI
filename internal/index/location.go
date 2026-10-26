// SPDX-License-Identifier: Apache-2.0

package index

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// ConfigFile is the discovery document an index location may serve.
const ConfigFile = "sysand-index-config.json"

// Location is where an index lives: a plain base URL or local directory
// that index-relative paths are appended to, or a URL template holding
// exactly one {path} or {path_raw} placeholder the relative path is
// substituted into ({path} percent-encodes "/" as %2F for file-access APIs
// such as GitLab's; {path_raw} keeps "/" literal and encodes each segment).
type Location struct {
	raw      string
	dir      string // local directory; empty for HTTP
	prefix   string // text before the placeholder, or the whole base URL
	suffix   string // text after the placeholder
	template bool
	rawPath  bool // {path_raw}
}

// ParseLocation parses an index location: an absolute http(s) URL or URL
// template without userinfo or fragment, or a local directory (optionally
// file://). The rules and messages follow sysand's so one configuration
// serves both clients.
func ParseLocation(s string) (*Location, error) {
	if s == "" {
		return nil, errors.New("index URL is empty")
	}
	if strings.ContainsAny(s, "{}") {
		return parseTemplate(s)
	}
	if lower := strings.ToLower(s); strings.Contains(lower, "%7bpath%7d") || strings.Contains(lower, "%7bpath_raw%7d") {
		return nil, fmt.Errorf("index URL `%s` contains `%%7B`/`%%7D` (percent-encoded braces);\n  did you mean a `{path}` template? Placeholders must be written literally, e.g. `.../repository/files/{path}/raw?ref=main`", redactUserinfo(s))
	}
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		u, err := url.Parse(s)
		if err != nil {
			return nil, fmt.Errorf("invalid index URL `%s`: %v", redactUserinfo(s), err)
		}
		if err := checkShape(u); err != nil {
			return nil, fmt.Errorf("index URL `%s` %v", redactUserinfo(s), err)
		}
		if !strings.HasSuffix(u.Path, "/") {
			u.Path += "/"
		}
		return &Location{raw: s, prefix: u.String()}, nil
	}
	if i := strings.Index(s, "://"); i >= 0 && !strings.HasPrefix(s, "file://") {
		return nil, fmt.Errorf("index URL `%s` has unsupported scheme `%s`; only `http`, `https` and local directories are supported", redactUserinfo(s), s[:i])
	}
	dir := strings.TrimPrefix(s, "file://")
	return &Location{raw: s, dir: dir, prefix: dir}, nil
}

func parseTemplate(s string) (*Location, error) {
	loc := &Location{raw: s, template: true}
	var placeholder string
	for _, p := range []string{"{path}", "{path_raw}"} {
		if n := strings.Count(s, p); n > 0 {
			if placeholder != "" || n > 1 {
				return nil, fmt.Errorf("index URL template `%s` must contain exactly one placeholder", redactUserinfo(s))
			}
			placeholder = p
		}
	}
	rest := s
	if placeholder != "" {
		rest = strings.Replace(s, placeholder, "", 1)
	}
	if i := strings.IndexAny(rest, "{}"); i >= 0 {
		tok := rest[i:]
		if j := strings.Index(tok, "}"); j >= 0 {
			tok = tok[:j+1]
		}
		return nil, fmt.Errorf("index URL template `%s` contains unknown placeholder `%s`;\n  supported placeholders: `{path}` (percent-encoded, `/` becomes `%%2F`) and `{path_raw}` (literal `/` separators)", redactUserinfo(s), tok)
	}
	if placeholder == "" {
		return nil, fmt.Errorf("index URL template `%s` must contain exactly one placeholder", redactUserinfo(s))
	}
	if strings.Contains(s, "#") {
		return nil, fmt.Errorf("index URL template `%s` has a fragment; fragments are never sent to the server", redactUserinfo(s))
	}
	loc.rawPath = placeholder == "{path_raw}"
	i := strings.Index(s, placeholder)
	loc.prefix, loc.suffix = s[:i], s[i+len(placeholder):]
	probe, err := url.Parse(loc.prefix + "probe" + loc.suffix)
	if err != nil {
		return nil, fmt.Errorf("invalid index URL template `%s`: %v", redactUserinfo(s), err)
	}
	if err := checkShape(probe); err != nil {
		return nil, fmt.Errorf("index URL template `%s` %v", redactUserinfo(s), err)
	}
	return loc, nil
}

func checkShape(u *url.URL) error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("has unsupported scheme `%s`; only `http` and `https` are supported", u.Scheme)
	}
	if u.User != nil {
		return errors.New("contains URL userinfo; username and password are not allowed, use SYSAND_CRED_* or netrc")
	}
	if u.Fragment != "" || strings.HasSuffix(u.String(), "#") {
		return errors.New("has a fragment; fragments are never sent to the server")
	}
	if u.Host == "" {
		return errors.New("has no host")
	}
	return nil
}

// redactUserinfo hides any user:password@ in a possibly malformed URL.
func redactUserinfo(raw string) string {
	start := 0
	if i := strings.Index(raw, "://"); i >= 0 {
		start = i + 3
	}
	end := len(raw)
	if i := strings.IndexAny(raw[start:], "/?#"); i >= 0 {
		end = start + i
	}
	if at := strings.LastIndex(raw[start:end], "@"); at >= 0 {
		return raw[:start] + "<redacted>" + raw[start+at:]
	}
	return raw
}

// String is the location as configured.
func (l *Location) String() string { return l.raw }

// Local reports whether the location is a directory on this machine.
func (l *Location) Local() bool { return l.dir != "" }

// URL is the address of the index-relative path rel (slash-separated).
func (l *Location) URL(rel string) string {
	if l.dir != "" {
		return filepath.Join(l.dir, filepath.FromSlash(rel))
	}
	if !l.template {
		return l.prefix + escapeSegments(rel)
	}
	if l.rawPath {
		return l.prefix + escapeSegments(rel) + l.suffix
	}
	return l.prefix + url.PathEscape(rel) + l.suffix
}

func escapeSegments(rel string) string {
	segs := strings.Split(rel, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}

// Endpoints is the outcome of discovery on a location.
type Endpoints struct {
	// IndexRoot is where index.json and the per-project files live.
	IndexRoot *Location
	// APIRoot is the index API advertised by the discovery document, if
	// any; recorded for diagnostics, never called (no publish, no whoami).
	APIRoot string
}

type configDoc struct {
	IndexRoot *string `json:"index_root"`
	APIRoot   *string `json:"api_root"`
}

// Discover reads <location>/sysand-index-config.json. An absent document
// (HTTP 404 or no such file) means the location itself is the flat index
// root; a present one names index_root (a base URL or template) and
// optionally api_root, both absolute http(s) URLs.
func Discover(ctx context.Context, client *http.Client, loc *Location) (*Endpoints, error) {
	data, err := fetchRaw(ctx, client, loc, ConfigFile, 1<<20)
	if errors.Is(err, ErrNotFound) {
		return &Endpoints{IndexRoot: loc}, nil
	}
	if err != nil {
		return nil, err
	}
	at := loc.URL(ConfigFile)
	var doc configDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("discovery document at `%s` is not valid JSON: %v", at, err)
	}
	ep := &Endpoints{IndexRoot: loc}
	if doc.IndexRoot != nil {
		root, err := parseDiscovered(at, "index_root", *doc.IndexRoot, true)
		if err != nil {
			return nil, err
		}
		ep.IndexRoot = root
	}
	if doc.APIRoot != nil {
		api, err := parseDiscovered(at, "api_root", *doc.APIRoot, false)
		if err != nil {
			return nil, err
		}
		ep.APIRoot = api.prefix
	}
	return ep, nil
}

func parseDiscovered(at, field, value string, allowTemplate bool) (*Location, error) {
	if value == "" || (!strings.Contains(value, "://") && !strings.ContainsAny(value, "{}")) {
		return nil, fmt.Errorf("discovery document at `%s` supplied a relative URL `%s` for `%s`;\n  absolute HTTP(S) URLs are required", at, value, field)
	}
	if !allowTemplate && strings.ContainsAny(value, "{}") {
		return nil, fmt.Errorf("discovery document at `%s` supplied a URL template `%s` for `%s`; `api_root` must be a plain URL", at, redactUserinfo(value), field)
	}
	u, err := url.Parse(strings.NewReplacer("{path}", "p", "{path_raw}", "p").Replace(value))
	if err != nil {
		return nil, fmt.Errorf("discovery document at `%s` supplied an invalid URL `%s` for `%s`:\n  %v", at, redactUserinfo(value), field, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("discovery document at `%s` supplied a non-HTTP(S) URL `%s` for `%s`;\n  only `http` and `https` are supported", at, redactUserinfo(value), field)
	}
	if u.User != nil {
		return nil, fmt.Errorf("discovery document at `%s` supplied URL userinfo in `%s` for `%s`;\n  username and password are not allowed", at, redactUserinfo(value), field)
	}
	loc, err := ParseLocation(value)
	if err != nil {
		return nil, fmt.Errorf("discovery document at `%s` supplied an invalid `%s`: %v", at, field, err)
	}
	return loc, nil
}

// fetchRaw reads one index-relative document from a location.
func fetchRaw(ctx context.Context, client *http.Client, loc *Location, rel string, limit int64) ([]byte, error) {
	if loc.dir != "" {
		p := loc.URL(rel)
		data, err := os.ReadFile(p)
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%s: %w", p, ErrNotFound)
		}
		return data, err
	}
	return fetchURL(ctx, client, loc.URL(rel), limit)
}

// SPDX-License-Identifier: Apache-2.0

// Package index reads and emits the static index layout sysand consumes:
// <root>/index.json listing project IRIs and, per project,
// <root>/<publisher>/<name>/versions.json with one entry per version and a
// <version>/ directory holding .project.json, .meta.json and project.kpar.
package index

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Open-MBEE/SysPI/internal/httpauth"
	"github.com/Open-MBEE/SysPI/internal/project"
	"github.com/Open-MBEE/SysPI/internal/version"
)

// VersionEntry is one entry of versions.json.
type VersionEntry struct {
	Version    string          `json:"version"`
	Usage      []project.Usage `json:"usage"`
	KparSize   int64           `json:"kpar_size"`
	KparDigest string          `json:"kpar_digest"`
	Status     string          `json:"status,omitempty"`
}

// Versions is a versions.json document.
type Versions struct {
	Versions []VersionEntry `json:"versions"`
}

// Root is a flat index.json document.
type Root struct {
	Projects []struct {
		IRI string `json:"iri"`
	} `json:"projects"`
}

// fetchURL reads one HTTP document, bounded by limit bytes.
func fetchURL(ctx context.Context, client *http.Client, u string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%s: %w", u, ErrNotFound)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %s%s", u, resp.Status, httpauth.Hint(resp.StatusCode, u, req.URL.Hostname()))
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s: larger than the %d byte limit", u, limit)
	}
	return data, nil
}

// ErrNotFound reports a document the index does not serve.
var ErrNotFound = fmt.Errorf("not found")

// ErrRemoved reports a version the index marks status "removed".
var ErrRemoved = fmt.Errorf("removed upstream")

// Index is an opened index: a configured location after discovery.
type Index struct {
	Location  *Location
	Endpoints *Endpoints
	client    *http.Client
}

// Open parses the location and runs discovery on it.
func Open(ctx context.Context, client *http.Client, location string) (*Index, error) {
	loc, err := ParseLocation(location)
	if err != nil {
		return nil, err
	}
	ep, err := Discover(ctx, client, loc)
	if err != nil {
		return nil, err
	}
	return &Index{Location: loc, Endpoints: ep, client: client}, nil
}

// Fetch reads the index-relative document rel from the index root.
func (x *Index) Fetch(ctx context.Context, rel string, limit int64) ([]byte, error) {
	return fetchRaw(ctx, x.client, x.Endpoints.IndexRoot, rel, limit)
}

// Source is the address the index-relative path is served from, as
// recorded in the lock.
func (x *Index) Source(rel string) string { return x.Endpoints.IndexRoot.URL(rel) }

// ProjectPath is the index-relative directory of pkg:sysand/<publisher>/<name>.
func ProjectPath(publisher, name string) (string, error) {
	for _, seg := range []string{publisher, name} {
		if seg == "" || seg != strings.ToLower(seg) || strings.ContainsAny(seg, "/\\ .") || strings.HasPrefix(seg, "-") {
			return "", fmt.Errorf("pkg:sysand/%s/%s: publisher and name must be lowercase single path segments", publisher, name)
		}
	}
	return publisher + "/" + name, nil
}

// ReadVersions fetches <publisher>/<name>/versions.json and validates it
// as sysand does: every version a semantic version without build
// metadata, listed once, kpar_digest sha256:<64-hex>, entries in
// descending semver precedence.
func (x *Index) ReadVersions(ctx context.Context, publisher, name string) (*Versions, error) {
	dir, err := ProjectPath(publisher, name)
	if err != nil {
		return nil, err
	}
	at := x.Source(dir + "/versions.json")
	data, err := x.Fetch(ctx, dir+"/versions.json", 16<<20)
	if err != nil {
		return nil, err
	}
	var v Versions
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("failed to parse JSON from `%s`: %v", at, err)
	}
	seen := map[string]bool{}
	var prev *version.Version
	for _, e := range v.Versions {
		e := e
		if strings.Contains(e.Version, "+") {
			return nil, fmt.Errorf("versions.json at `%s` has entry with version `%s` carrying build metadata; build metadata is not permitted in index-advertised versions", at, e.Version)
		}
		pv, err := version.Parse(e.Version)
		if err != nil {
			return nil, fmt.Errorf("versions.json at `%s` has entry with non-semver version `%s`", at, e.Version)
		}
		if !strings.HasPrefix(e.KparDigest, "sha256:") || len(e.KparDigest) != 7+64 || strings.Trim(strings.ToLower(e.KparDigest[7:]), "0123456789abcdef") != "" {
			return nil, fmt.Errorf("versions.json at `%s` has entry for version `%s` with malformed `kpar_digest` = `%s` (expected `sha256:<64-hex>`)", at, e.Version, e.KparDigest)
		}
		if e.Usage == nil || e.KparSize <= 0 {
			return nil, fmt.Errorf("versions.json at `%s` has entry for version `%s` lacking `usage` or `kpar_size`", at, e.Version)
		}
		if seen[e.Version] {
			return nil, fmt.Errorf("versions.json at `%s` lists version `%s` more than once", at, e.Version)
		}
		seen[e.Version] = true
		if prev != nil && !pv.Less(*prev) {
			return nil, fmt.Errorf("versions.json at `%s` is not in descending semver precedence: `%s` precedes `%s` in wire order", at, prev, pv)
		}
		prev = &pv
	}
	return &v, nil
}

// Lookup finds one advertised version. A version marked "removed" is
// ErrRemoved (its files are gone); "yanked" is returned (files are still
// served, so a lock may keep it) but Available excludes it.
func (v *Versions) Lookup(ver string) (*VersionEntry, error) {
	for i := range v.Versions {
		e := &v.Versions[i]
		if e.Version != ver {
			continue
		}
		if e.Status == "removed" {
			return nil, fmt.Errorf("version `%s` %w (versions.json marks it `status: \"removed\"`)", ver, ErrRemoved)
		}
		return e, nil
	}
	return nil, fmt.Errorf("versions.json does not list version `%s`", ver)
}

// Available lists the entries that are neither yanked nor removed, newest
// first.
func (v *Versions) Available() []VersionEntry {
	var out []VersionEntry
	for _, e := range v.Versions {
		if e.Status == "" || e.Status == "available" {
			out = append(out, e)
		}
	}
	return out
}

// Emit adds packages (project directories or .kpar files) to the static
// index rooted at root, creating it when absent. Every package needs a
// publisher, as the pkg:sysand layout is keyed by publisher and name. It
// returns the pkg:sysand IRIs written.
func Emit(root string, packages []string, created func(dir string) time.Time) ([]string, error) {
	var iris []string
	for _, pkg := range packages {
		iri, err := emitOne(root, pkg, created)
		if err != nil {
			return nil, err
		}
		iris = append(iris, iri)
	}
	if err := writeRoot(root, iris); err != nil {
		return nil, err
	}
	return iris, nil
}

func emitOne(root, pkg string, created func(dir string) time.Time) (string, error) {
	kpar := pkg
	if st, err := os.Stat(pkg); err != nil {
		return "", err
	} else if st.IsDir() {
		tmp, err := os.MkdirTemp("", "syspi-index-*")
		if err != nil {
			return "", err
		}
		defer os.RemoveAll(tmp)
		kpar = filepath.Join(tmp, "project.kpar")
		if _, err := project.Bundle(pkg, kpar, created(pkg)); err != nil {
			return "", err
		}
	}
	a, err := project.OpenArchive(kpar)
	if err != nil {
		return "", err
	}
	defer a.Close()
	if a.Project.Publisher == "" {
		return "", fmt.Errorf("%s: .project.json has no \"publisher\"; the index is keyed by publisher and name", pkg)
	}
	dir, err := ProjectPath(a.Project.Publisher, a.Project.Name)
	if err != nil {
		return "", fmt.Errorf("%s: %v", pkg, err)
	}
	if strings.Contains(a.Project.Version, "+") {
		return "", fmt.Errorf("%s: version %q carries build metadata, which the index does not allow", pkg, a.Project.Version)
	}
	verDir := filepath.Join(root, filepath.FromSlash(dir), a.Project.Version)
	if err := os.MkdirAll(verDir, 0o755); err != nil {
		return "", err
	}
	data, err := os.ReadFile(kpar)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(verDir, "project.kpar"), data, 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(verDir, project.ProjectFile), a.Project.Marshal(), 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(verDir, project.MetaFile), a.Meta.Marshal(), 0o644); err != nil {
		return "", err
	}
	entry := VersionEntry{Version: a.Project.Version, Usage: a.Project.Usage, KparSize: int64(len(data)), KparDigest: "sha256:" + a.SHA256}
	if entry.Usage == nil {
		entry.Usage = []project.Usage{}
	}
	vpath := filepath.Join(root, filepath.FromSlash(dir), "versions.json")
	var vs Versions
	if cur, err := os.ReadFile(vpath); err == nil {
		if err := json.Unmarshal(cur, &vs); err != nil {
			return "", fmt.Errorf("%s: %v", vpath, err)
		}
	}
	replaced := false
	for i := range vs.Versions {
		if vs.Versions[i].Version == entry.Version {
			vs.Versions[i] = entry
			replaced = true
		}
	}
	if !replaced {
		vs.Versions = append(vs.Versions, entry)
	}
	sort.SliceStable(vs.Versions, func(i, j int) bool {
		vi, _ := version.Parse(vs.Versions[i].Version)
		vj, _ := version.Parse(vs.Versions[j].Version)
		return vj.Less(vi)
	})
	if err := os.WriteFile(vpath, marshal(vs), 0o644); err != nil {
		return "", err
	}
	return "pkg:sysand/" + path.Join(a.Project.Publisher, a.Project.Name), nil
}

func writeRoot(root string, iris []string) error {
	p := filepath.Join(root, "index.json")
	var r Root
	if cur, err := os.ReadFile(p); err == nil {
		if err := json.Unmarshal(cur, &r); err != nil {
			return fmt.Errorf("%s: %v", p, err)
		}
	}
	have := map[string]bool{}
	for _, e := range r.Projects {
		have[e.IRI] = true
	}
	for _, iri := range iris {
		if !have[iri] {
			r.Projects = append(r.Projects, struct {
				IRI string `json:"iri"`
			}{iri})
			have[iri] = true
		}
	}
	sort.Slice(r.Projects, func(i, j int) bool { return r.Projects[i].IRI < r.Projects[j].IRI })
	if r.Projects == nil {
		r.Projects = []struct {
			IRI string `json:"iri"`
		}{}
	}
	return os.WriteFile(p, marshal(r), 0o644)
}

func marshal(v any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	enc.Encode(v)
	return buf.Bytes()
}

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
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

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

// Fetch reads a document from an index rooted at a URL or local directory.
func Fetch(ctx context.Context, client *http.Client, root, rel string, limit int64) ([]byte, error) {
	if strings.HasPrefix(root, "http://") || strings.HasPrefix(root, "https://") {
		u, err := url.Parse(strings.TrimSuffix(root, "/") + "/" + rel)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
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
			return nil, fmt.Errorf("%s: HTTP %s%s", u, resp.Status, AuthHint(resp.StatusCode, u.Hostname()))
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
	root = strings.TrimPrefix(root, "file://")
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("%s: %w", filepath.Join(root, rel), ErrNotFound)
	}
	return data, err
}

// AuthHint is the actionable line for an HTTP 401 or 403 from host: a netrc
// entry ($NETRC, else ~/.netrc or %USERPROFILE%\_netrc) is how credentials
// reach syspi's own downloads.
func AuthHint(status int, host string) string {
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return fmt.Sprintf("\n  hint: the server wants credentials; add `machine %s login <user> password <secret>` to your netrc ($NETRC, else ~/.netrc)", host)
	}
	return ""
}

// ErrNotFound reports a document the index does not serve.
var ErrNotFound = fmt.Errorf("not found")

// ProjectPath is the index-relative directory of pkg:sysand/<publisher>/<name>.
func ProjectPath(publisher, name string) (string, error) {
	for _, seg := range []string{publisher, name} {
		if seg == "" || seg != strings.ToLower(seg) || strings.ContainsAny(seg, "/\\ .") || strings.HasPrefix(seg, "-") {
			return "", fmt.Errorf("pkg:sysand/%s/%s: publisher and name must be lowercase single path segments", publisher, name)
		}
	}
	return publisher + "/" + name, nil
}

// ReadVersions fetches and validates <root>/<publisher>/<name>/versions.json.
func ReadVersions(ctx context.Context, client *http.Client, root, publisher, name string) (*Versions, error) {
	dir, err := ProjectPath(publisher, name)
	if err != nil {
		return nil, err
	}
	data, err := Fetch(ctx, client, root, dir+"/versions.json", 16<<20)
	if err != nil {
		return nil, err
	}
	var v Versions
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("%s/versions.json: %v", dir, err)
	}
	seen := map[string]bool{}
	for i, e := range v.Versions {
		if e.Version == "" || e.KparDigest == "" || e.KparSize <= 0 || e.Usage == nil {
			return nil, fmt.Errorf("%s/versions.json: entry %d lacks version, usage, kpar_size or kpar_digest", dir, i)
		}
		if _, err := version.Parse(e.Version); err != nil || strings.Contains(e.Version, "+") {
			return nil, fmt.Errorf("%s/versions.json: entry %d: version %q is not a semantic version without build metadata", dir, i, e.Version)
		}
		if !strings.HasPrefix(e.KparDigest, "sha256:") || len(e.KparDigest) != 7+64 {
			return nil, fmt.Errorf("%s/versions.json: entry %d: kpar_digest must be sha256:<64 hex>", dir, i)
		}
		if seen[e.Version] {
			return nil, fmt.Errorf("%s/versions.json: version %s listed twice", dir, e.Version)
		}
		seen[e.Version] = true
	}
	return &v, nil
}

// Available lists the entries that are not retired, newest first.
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

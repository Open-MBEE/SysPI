// SPDX-License-Identifier: Apache-2.0

// Package lock reads and writes .project.lock.json: one entry per resolved
// package of the transitive closure, in deterministic bytes.
package lock

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Version is the lock format version this package writes.
const Version = 1

// Kind classifies a locked package.
const (
	KindVCS      = "vcs"      // exported tree of a tagged repository
	KindArchive  = "archive"  // a .kpar (local, remote or from an index)
	KindLocal    = "local"    // an editable local directory, read in place
	KindStandard = "standard" // an OMG standard-library IRI, never installed
)

// Entry is one locked package.
type Entry struct {
	// Resource is the canonical, version-free identity (pkg:<vcs>/<host>/<path>[#<subpath>],
	// an archive URL or path, a standard IRI).
	Resource  string `json:"resource"`
	Kind      string `json:"kind"`
	Publisher string `json:"publisher,omitempty"`
	Name      string `json:"name"`
	Version   string `json:"version"`
	// VCS, Source, Tag and Revision describe where a KindVCS tree came from;
	// Source is also the URL or path of an archive or local directory.
	VCS      string `json:"vcs,omitempty"`
	Source   string `json:"source,omitempty"`
	Tag      string `json:"tag,omitempty"`
	Revision string `json:"revision,omitempty"`
	// SHA256 is the tree digest of an exported tree or the file digest of a .kpar.
	SHA256 string `json:"sha256,omitempty"`
	// Usage lists the canonical resources this package's own .project.json uses.
	Usage []string `json:"usage,omitempty"`
}

// Lock is the lock file.
type Lock struct {
	LockVersion int     `json:"lockVersion"`
	Packages    []Entry `json:"packages"`
}

// New returns an empty lock of the current version.
func New() *Lock { return &Lock{LockVersion: Version, Packages: []Entry{}} }

// Find returns the entry for a canonical resource.
func (l *Lock) Find(resource string) *Entry {
	for i := range l.Packages {
		if l.Packages[i].Resource == resource {
			return &l.Packages[i]
		}
	}
	return nil
}

// Put adds or replaces the entry for e.Resource.
func (l *Lock) Put(e Entry) {
	if e.Usage == nil {
		e.Usage = []string{}
	}
	sort.Strings(e.Usage)
	if cur := l.Find(e.Resource); cur != nil {
		*cur = e
		return
	}
	l.Packages = append(l.Packages, e)
}

// Marshal renders the lock deterministically: packages sorted by resource,
// keys in a fixed order, two-space indentation, trailing newline.
func (l *Lock) Marshal() []byte {
	sort.Slice(l.Packages, func(i, j int) bool { return l.Packages[i].Resource < l.Packages[j].Resource })
	for i := range l.Packages {
		if l.Packages[i].Usage == nil {
			l.Packages[i].Usage = []string{}
		}
		sort.Strings(l.Packages[i].Usage)
	}
	if l.Packages == nil {
		l.Packages = []Entry{}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	enc.Encode(l)
	return buf.Bytes()
}

// Parse decodes a lock body.
func Parse(data []byte) (*Lock, error) {
	var l Lock
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&l); err != nil {
		return nil, fmt.Errorf("lock: %v", err)
	}
	if l.LockVersion != Version {
		return nil, fmt.Errorf("lock: lockVersion %d is not supported (this tool writes %d)", l.LockVersion, Version)
	}
	seen := map[string]bool{}
	for i, e := range l.Packages {
		if e.Resource == "" || e.Name == "" || e.Version == "" || e.Kind == "" {
			return nil, fmt.Errorf("lock: packages[%d] lacks resource, kind, name or version", i)
		}
		if seen[e.Resource] {
			return nil, fmt.Errorf("lock: %s is locked twice", e.Resource)
		}
		seen[e.Resource] = true
	}
	return &l, nil
}

// Read reads <dir>/.project.lock.json; ok is false when there is none.
func Read(dir, file string) (l *Lock, ok bool, err error) {
	data, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}
	l, err = Parse(data)
	return l, err == nil, err
}

// Write writes the lock to <dir>/<file> only when its bytes changed, and
// reports whether it wrote.
func (l *Lock) Write(dir, file string) (bool, error) {
	data := l.Marshal()
	p := filepath.Join(dir, file)
	if cur, err := os.ReadFile(p); err == nil && bytes.Equal(cur, data) {
		return false, nil
	}
	return true, os.WriteFile(p, data, 0o644)
}

// SPDX-License-Identifier: Apache-2.0

package install

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/Open-MBEE/SysPI/internal/lock"
	"github.com/Open-MBEE/SysPI/internal/project"
)

// Listing is one locked package as list reports it.
type Listing struct {
	Resource  string `json:"resource"`
	Kind      string `json:"kind"`
	Publisher string `json:"publisher,omitempty"`
	Name      string `json:"name,omitempty"`
	Version   string `json:"version,omitempty"`
	VCS       string `json:"vcs,omitempty"`
	Tag       string `json:"tag,omitempty"`
	Revision  string `json:"revision,omitempty"`
	// Source is the URL or path an archive came from, or the local directory read in place.
	Source string `json:"source,omitempty"`
	// Path is the installed copy under lib/, relative to the project root;
	// empty for a package that is never installed (standard, local).
	Path string `json:"path,omitempty"`
	// Installed says whether the copy at Path is present; nil without a Path.
	Installed *bool `json:"installed,omitempty"`
}

// List reads the lock of the project at root and describes each locked
// package in resource order. It reads nothing but the lock and lib/.
func List(root string) ([]Listing, error) {
	l, ok, err := lock.Read(root, project.LockFile)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%s: no %s; run syspi lock or syspi install first", root, project.LockFile)
	}
	sort.Slice(l.Packages, func(i, j int) bool { return l.Packages[i].Resource < l.Packages[j].Resource })
	libDir := filepath.Join(root, project.LibDir)
	out := make([]Listing, 0, len(l.Packages))
	for i := range l.Packages {
		e := &l.Packages[i]
		li := Listing{Resource: e.Resource, Kind: e.Kind, Publisher: e.Publisher, Name: e.Name, Version: e.Version, VCS: e.VCS, Tag: e.Tag, Revision: e.Revision}
		if e.Kind != lock.KindVCS {
			li.Source = e.Source
		}
		if p := libPath(libDir, e); p != "" {
			li.Path = rel(root, p)
			_, err := os.Lstat(p)
			if err != nil && !os.IsNotExist(err) {
				return nil, err
			}
			present := err == nil
			li.Installed = &present
		}
		out = append(out, li)
	}
	return out, nil
}

// State is the one word the text listing prints for a package's installed copy.
func (l *Listing) State() string {
	switch {
	case l.Installed == nil:
		return "not installed"
	case *l.Installed:
		return "present"
	}
	return "missing"
}

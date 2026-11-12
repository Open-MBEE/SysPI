// SPDX-License-Identifier: Apache-2.0

package install

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Open-MBEE/SysPI/internal/digest"
	"github.com/Open-MBEE/SysPI/internal/lock"
	"github.com/Open-MBEE/SysPI/internal/origin"
	"github.com/Open-MBEE/SysPI/internal/project"
)

// Discrepancy is one way the project disagrees with its lock: an installed
// copy under lib/ that is not what the lock recorded, or a usage the lock
// does not cover.
type Discrepancy struct {
	// Resource is the lock entry concerned, or the usage of .project.json.
	Resource string `json:"resource"`
	// Path is the installed copy, relative to the project root; empty when
	// the discrepancy is not about a file.
	Path    string `json:"path,omitempty"`
	Problem string `json:"problem"`
}

func (d *Discrepancy) String() string {
	if d.Path != "" {
		return d.Path + ": " + d.Problem
	}
	return d.Resource + ": " + d.Problem
}

// Verify checks the project at root against its lock without contacting a
// repository and without writing anything: every usage of .project.json has
// a lock entry, and every lock entry's installed copy under lib/ (or local
// directory) is what the lock recorded — its .project.json, the checksums
// its .meta.json records, its tree or .kpar SHA-256 and the usages it
// declares. It returns every discrepancy, in resource order, and the number
// of lock entries checked.
func Verify(root string) ([]Discrepancy, int, error) {
	proj, err := project.ReadProject(root)
	if err != nil {
		return nil, 0, err
	}
	l, ok, err := lock.Read(root, project.LockFile)
	if err != nil {
		return nil, 0, err
	}
	if !ok {
		return nil, 0, fmt.Errorf("%s: no %s; run syspi lock or syspi install first", root, project.LockFile)
	}
	sort.Slice(l.Packages, func(i, j int) bool { return l.Packages[i].Resource < l.Packages[j].Resource })
	var out []Discrepancy
	for _, u := range proj.Usage {
		o, err := origin.Parse(u.Resource)
		if err != nil {
			out = append(out, Discrepancy{Resource: u.Resource, Problem: err.Error()})
			continue
		}
		if lockedEntry(l, o) == nil {
			out = append(out, Discrepancy{Resource: u.Resource, Problem: "usage of " + project.ProjectFile + " has no lock entry; run syspi lock"})
		}
	}
	libDir := filepath.Join(root, project.LibDir)
	for i := range l.Packages {
		e := &l.Packages[i]
		switch e.Kind {
		case lock.KindVCS:
			out = append(out, verifyTree(root, libPath(libDir, e), e)...)
		case lock.KindArchive:
			out = append(out, verifyArchive(root, libPath(libDir, e), e)...)
		case lock.KindLocal:
			out = append(out, verifyLocal(e)...)
		}
	}
	return out, len(l.Packages), nil
}

// lockedEntry finds the lock entry a usage resolves to: by repository key
// for a vcs resource, by the resource itself otherwise.
func lockedEntry(l *lock.Lock, o *origin.Origin) *lock.Entry {
	if o.Kind != origin.VCS {
		return l.Find(o.Identity())
	}
	for i := range l.Packages {
		e := &l.Packages[i]
		if e.Kind != lock.KindVCS {
			continue
		}
		if eo, err := origin.Parse(e.Resource); err == nil && eo.Key() == o.Key() {
			return e
		}
	}
	return nil
}

func verifyTree(root, dir string, e *lock.Entry) []Discrepancy {
	p := rel(root, dir)
	at := func(problem string) Discrepancy { return Discrepancy{Resource: e.Resource, Path: p, Problem: problem} }
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return []Discrepancy{at("installed copy missing")}
	}
	var out []Discrepancy
	var skip []string
	if e.MetaGenerated {
		skip = append(skip, project.MetaFile)
	}
	if d, err := digest.TreeExcept(dir, skip...); err != nil {
		out = append(out, at(err.Error()))
	} else if d != e.SHA256 {
		out = append(out, at("tree SHA-256 "+d+" differs from the locked "+e.SHA256))
	}
	meta, hasMeta, err := project.ReadMeta(dir)
	switch {
	case err != nil:
		out = append(out, at(err.Error()))
	case !hasMeta:
		out = append(out, at(project.MetaFile+" missing"))
	default:
		bad, err := project.MismatchedChecksums(dir, meta)
		if err != nil {
			out = append(out, at(err.Error()))
		}
		for _, b := range bad {
			out = append(out, at(b))
		}
	}
	pj, err := project.ReadProject(dir)
	if err != nil {
		return append(out, at(err.Error()))
	}
	return append(out, verifyProject(at, pj, e)...)
}

func verifyArchive(root, file string, e *lock.Entry) []Discrepancy {
	p := rel(root, file)
	at := func(problem string) Discrepancy { return Discrepancy{Resource: e.Resource, Path: p, Problem: problem} }
	if st, err := os.Stat(file); err != nil || st.IsDir() {
		return []Discrepancy{at("installed copy missing")}
	}
	var out []Discrepancy
	if d, err := digest.File(file); err != nil {
		out = append(out, at(err.Error()))
	} else if d != e.SHA256 {
		out = append(out, at("SHA-256 "+d+" differs from the locked "+e.SHA256))
	}
	a, err := project.OpenArchive(file)
	if err != nil {
		return append(out, at(strings.TrimPrefix(err.Error(), file+": ")))
	}
	defer a.Close()
	return append(out, verifyProject(at, a.Project, e)...)
}

func verifyLocal(e *lock.Entry) []Discrepancy {
	at := func(problem string) Discrepancy {
		return Discrepancy{Resource: e.Resource, Path: e.Source, Problem: problem}
	}
	if st, err := os.Stat(e.Source); err != nil || !st.IsDir() {
		return []Discrepancy{at("local directory missing")}
	}
	pj, err := project.ReadProject(e.Source)
	if err != nil {
		return []Discrepancy{at(err.Error())}
	}
	return verifyProject(at, pj, e)
}

// verifyProject compares the .project.json of an installed copy with what
// the lock recorded of it: publisher, name, version and the declared usages.
func verifyProject(at func(string) Discrepancy, p *project.Project, e *lock.Entry) []Discrepancy {
	var out []Discrepancy
	if p.Publisher != e.Publisher || p.Name != e.Name || p.Version != e.Version {
		out = append(out, at(fmt.Sprintf("%s says %s %s, the lock %s %s", project.ProjectFile, installName(p.Publisher, p.Name), p.Version, installName(e.Publisher, e.Name), e.Version)))
	}
	declared := make([]string, 0, len(p.Usage))
	for _, u := range p.Usage {
		declared = append(declared, usageText(u.Resource, u.VersionConstraint))
	}
	locked := make([]string, 0, len(e.Usage))
	for _, u := range e.Usage {
		locked = append(locked, usageText(u.Resource, u.VersionConstraint))
	}
	sort.Strings(declared)
	sort.Strings(locked)
	for _, u := range missingFrom(declared, locked) {
		out = append(out, at("declares usage "+u+", which the lock does not record"))
	}
	for _, u := range missingFrom(locked, declared) {
		out = append(out, at("the lock records usage "+u+", which "+project.ProjectFile+" no longer declares"))
	}
	return out
}

func installName(publisher, name string) string {
	if publisher == "" {
		return name
	}
	return publisher + "/" + name
}

func usageText(resource, constraint string) string {
	if constraint == "" {
		return resource
	}
	return resource + " " + constraint
}

// missingFrom lists the sorted strings of a that b lacks.
func missingFrom(a, b []string) []string {
	have := map[string]bool{}
	for _, s := range b {
		have[s] = true
	}
	var out []string
	for _, s := range a {
		if !have[s] {
			out = append(out, s)
		}
	}
	return out
}

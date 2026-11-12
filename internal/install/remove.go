// SPDX-License-Identifier: Apache-2.0

package install

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Open-MBEE/SysPI/internal/digest"
	"github.com/Open-MBEE/SysPI/internal/lock"
	"github.com/Open-MBEE/SysPI/internal/origin"
	"github.com/Open-MBEE/SysPI/internal/project"
)

// Remove drops the usages naming resources from .project.json, re-resolves
// and re-locks what remains, and removes from lib/ only the installed copies
// no remaining package of the closure needs. A failing run leaves
// .project.json, the lock and lib/ as they were.
func Remove(ctx context.Context, opts Options, resources []string) (*Result, error) {
	root, err := filepath.Abs(opts.Root)
	if err != nil {
		return nil, err
	}
	opts.Root = root
	if opts.Log == nil {
		opts.Log = io.Discard
	}
	proj, err := project.ReadProject(root)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(root, project.ProjectFile))
	if err != nil {
		return nil, err
	}
	var removed []string
	for _, res := range resources {
		o, err := origin.Parse(res)
		if err != nil {
			return nil, err
		}
		i := usageIndex(proj, o.Identity())
		if i < 0 {
			return nil, fmt.Errorf("%s: no usage of %s%s", project.ProjectFile, o.Identity(), lockedBy(root, o))
		}
		removed = append(removed, proj.Usage[i].Resource)
		proj.Usage = append(proj.Usage[:i], proj.Usage[i+1:]...)
	}
	if err := proj.Write(root); err != nil {
		return nil, err
	}
	opts.Prune = true
	res, err := Run(ctx, opts)
	if err != nil {
		os.WriteFile(filepath.Join(root, project.ProjectFile), data, 0o644)
		return nil, err
	}
	res.RemovedUsages = removed
	for _, u := range removed {
		if o, err := origin.Parse(u); err == nil {
			if by := requesters(res.Lock, o); len(by) > 0 {
				fmt.Fprintf(opts.Log, "%s stays locked: used by %s\n", o.Identity(), joinSorted(by))
			}
		}
	}
	return res, nil
}

// usageIndex finds the usage with the canonical identity, as add matches it.
func usageIndex(p *project.Project, id string) int {
	for i, u := range p.Usage {
		if uo, err := origin.Parse(u.Resource); err == nil && uo.Identity() == id {
			return i
		}
	}
	return -1
}

// lockedBy explains a resource that is not a usage but is in the lock
// because other packages use it.
func lockedBy(root string, o *origin.Origin) string {
	l, ok, err := lock.Read(root, project.LockFile)
	if err != nil || !ok {
		return ""
	}
	if by := requesters(l, o); len(by) > 0 {
		return "; it is locked because " + joinSorted(by) + " uses it"
	}
	return ""
}

// requesters names the locked packages whose declared usages name o.
func requesters(l *lock.Lock, o *origin.Origin) []string {
	key := o.Key()
	var by []string
	for _, e := range l.Packages {
		for _, u := range e.Usage {
			if uo, err := origin.Parse(u.Resource); err == nil && uo.Key() == key {
				by = append(by, e.Resource)
				break
			}
		}
	}
	return by
}

func joinSorted(s []string) string {
	sort.Strings(s)
	out := ""
	for i, x := range s {
		if i > 0 {
			out += ", "
		}
		out += x
	}
	return out
}

// prune removes the installed copies the previous lock placed under lib/
// that the fresh lock no longer claims — only when their contents still
// match the entry that put them there; anything else under lib/ is kept.
// It runs after the new lock is written, so a copy it cannot remove is
// only a leftover the next pruning run removes, never an inconsistency.
func (r *resolver) prune() {
	claimed := map[string]bool{}
	var local []string
	for i := range r.fresh.Packages {
		e := &r.fresh.Packages[i]
		if p := libPath(r.libDir, e); p != "" {
			claimed[p] = true
		}
		if e.Kind == lock.KindLocal {
			if abs, err := filepath.Abs(e.Source); err == nil {
				local = append(local, abs)
			}
		}
	}
	for i := range r.old.Packages {
		e := &r.old.Packages[i]
		p := libPath(r.libDir, e)
		if p == "" || claimed[p] {
			continue
		}
		if _, err := os.Lstat(p); err != nil {
			continue
		}
		if !installedMatches(p, e) {
			r.logf("kept %s: its contents differ from the lock entry that installed it", rel(r.opts.Root, p))
			continue
		}
		if by := localWithin(p, local); by != "" {
			r.logf("kept %s: the local directory %s, still locked, is inside it", rel(r.opts.Root, p), by)
			continue
		}
		if dirs, err := digest.MetadataDirs(p); err != nil || len(dirs) > 0 {
			r.logf("kept %s: it holds repository metadata (%s) the lock does not record", rel(r.opts.Root, p), strings.Join(dirs, ", "))
			continue
		}
		if err := os.RemoveAll(p); err != nil {
			r.logf("kept %s (%s %s is no longer used): %v", rel(r.opts.Root, p), e.Name, e.Version, err)
			continue
		}
		r.logf("%s %s is no longer used; %s removed", e.Name, e.Version, rel(r.opts.Root, p))
		r.res.Pruned = append(r.res.Pruned, rel(r.opts.Root, p))
	}
	sort.Strings(r.res.Pruned)
}

// localWithin names the first of the local directories that is p or lies under it.
func localWithin(p string, local []string) string {
	for _, l := range local {
		if r, err := filepath.Rel(p, l); err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			return l
		}
	}
	return ""
}

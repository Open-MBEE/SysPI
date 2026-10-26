// SPDX-License-Identifier: Apache-2.0

// Package install resolves the usages of a project to exact packages,
// records them in .project.lock.json and installs them under lib/ beside
// the project's .project.json. A lock entry wins over a constraint; a
// project whose lock and lib/ already satisfy it is never fetched again.
package install

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/Open-MBEE/SysPI/internal/digest"
	"github.com/Open-MBEE/SysPI/internal/index"
	"github.com/Open-MBEE/SysPI/internal/lock"
	"github.com/Open-MBEE/SysPI/internal/origin"
	"github.com/Open-MBEE/SysPI/internal/project"
	"github.com/Open-MBEE/SysPI/internal/vcs"
	"github.com/Open-MBEE/SysPI/internal/version"
)

// Options configure a run.
type Options struct {
	// Root is the directory holding the consuming .project.json.
	Root string
	// Offline refuses every network and repository access; the lock and
	// lib/ must already satisfy the project.
	Offline bool
	// Index is the sysand static index (URL or directory) pkg:sysand/
	// resources resolve through; empty means none configured.
	Index string
	// Log receives one line per action; nil discards.
	Log io.Writer
	// HTTP fetches remote archives and index files; nil uses http.DefaultClient.
	HTTP *http.Client
	// LockOnly resolves and writes the lock without installing anything.
	LockOnly bool
}

// Result reports what a run did.
type Result struct {
	Lock *lock.Lock
	// Installed, Reused and Skipped name packages by canonical resource.
	Installed, Reused, Skipped []string
	// LockWritten reports whether .project.lock.json changed on disk.
	LockWritten bool
}

type request struct {
	resource   string
	constraint version.Constraint
	requester  string
}

type resolver struct {
	ctx    context.Context
	opts   Options
	proj   *project.Project
	old    *lock.Lock
	fresh  *lock.Lock
	chosen map[string]*lock.Entry // identity -> entry resolved in this run
	queue  []request
	res    *Result
	libDir string
	// stage holds newly fetched packages until every usage resolved; slots
	// maps each lib/ destination to the resource and digest that claimed it.
	stage string
	items []staged
	slots map[string]slot
}

type staged struct{ src, dst string }

type slot struct{ resource, sha256 string }

// Run resolves, locks and installs the project at opts.Root.
func Run(ctx context.Context, opts Options) (*Result, error) {
	if opts.Log == nil {
		opts.Log = io.Discard
	}
	if opts.HTTP == nil {
		opts.HTTP = http.DefaultClient
	}
	root, err := filepath.Abs(opts.Root)
	if err != nil {
		return nil, err
	}
	opts.Root = root
	proj, err := project.ReadProject(root)
	if err != nil {
		return nil, err
	}
	old, _, err := lock.Read(root, project.LockFile)
	if err != nil {
		return nil, err
	}
	if old == nil {
		old = lock.New()
	}
	r := &resolver{ctx: ctx, opts: opts, proj: proj, old: old, fresh: lock.New(), chosen: map[string]*lock.Entry{}, res: &Result{}, libDir: filepath.Join(root, project.LibDir), slots: map[string]slot{}}
	for _, u := range proj.Usage {
		c, err := usageConstraint(u)
		if err != nil {
			return nil, err
		}
		r.queue = append(r.queue, request{resource: u.Resource, constraint: c, requester: project.ProjectFile})
	}
	defer func() {
		if r.stage != "" {
			os.RemoveAll(r.stage)
		}
	}()
	for len(r.queue) > 0 {
		req := r.queue[0]
		r.queue = r.queue[1:]
		if err := r.resolve(req); err != nil {
			return nil, err
		}
	}
	// Everything resolved and verified: publish the staged packages, then the lock.
	for _, it := range r.items {
		if err := os.RemoveAll(it.dst); err != nil {
			return nil, err
		}
		if err := os.Rename(it.src, it.dst); err != nil {
			return nil, err
		}
	}
	r.res.Lock = r.fresh
	r.res.LockWritten, err = r.fresh.Write(root, project.LockFile)
	if err != nil {
		return nil, err
	}
	return r.res, nil
}

// usageConstraint derives the constraint of a usage: versionConstraint when
// given, else an exact @<version> from the resource, else any version.
func usageConstraint(u project.Usage) (version.Constraint, error) {
	if u.VersionConstraint != "" {
		c, err := version.ParseConstraint(u.VersionConstraint)
		if err != nil {
			return c, fmt.Errorf("usage %q: %v", u.Resource, err)
		}
		return c, nil
	}
	o, err := origin.Parse(u.Resource)
	if err != nil {
		return version.Constraint{}, err
	}
	if o.Version != "" {
		v, err := version.Parse(o.Version)
		if err != nil {
			return version.Constraint{}, fmt.Errorf("usage %q: %v", u.Resource, err)
		}
		return version.Exact(v), nil
	}
	return version.Constraint{}, nil
}

// claim reserves a lib/ destination for a resource; two resources with the
// same publisher, name and version may share it only with the same digest.
func (r *resolver) claim(dst, resource, sha256 string) error {
	if cur, ok := r.slots[dst]; ok && cur.resource != resource && cur.sha256 != sha256 {
		return fmt.Errorf("%s and %s both install as %s with different contents (SHA-256 %s vs %s); rename one of the packages", cur.resource, resource, rel(r.opts.Root, dst), cur.sha256, sha256)
	}
	r.slots[dst] = slot{resource, sha256}
	return nil
}

// alreadyStaged reports whether an identical package (claim verified the
// digest) is already staged for dst.
func (r *resolver) alreadyStaged(dst string) bool {
	for _, it := range r.items {
		if it.dst == dst {
			return true
		}
	}
	return false
}

// staging returns the directory new packages are written to before they
// are published into lib/.
func (r *resolver) staging() (string, error) {
	if r.stage != "" {
		return r.stage, nil
	}
	if err := os.MkdirAll(r.libDir, 0o755); err != nil {
		return "", err
	}
	stage, err := os.MkdirTemp(r.libDir, ".syspi-stage-*")
	if err != nil {
		return "", err
	}
	r.stage = stage
	return stage, nil
}

func (r *resolver) logf(format string, args ...any) {
	fmt.Fprintf(r.opts.Log, format+"\n", args...)
}

func (r *resolver) resolve(req request) error {
	o, err := origin.Parse(req.resource)
	if err != nil {
		return fmt.Errorf("%s: usage %q: %v", req.requester, req.resource, err)
	}
	id := o.Identity()
	if e, done := r.chosen[id]; done {
		if e.Kind != lock.KindStandard {
			v, _ := version.Parse(e.Version)
			if !req.constraint.Check(v) {
				return fmt.Errorf("%s: usage %q wants %s but %s is already resolved to %s (required by another usage)", req.requester, req.resource, req.constraint, id, e.Version)
			}
		}
		return nil
	}
	var e *lock.Entry
	switch o.Kind {
	case origin.Standard:
		e = &lock.Entry{Resource: o.Source, Kind: lock.KindStandard, Name: path.Base(o.Source), Source: o.Source}
		r.res.Skipped = append(r.res.Skipped, o.Source)
	case origin.LocalDir:
		e, err = r.resolveLocalDir(o, req)
	case origin.LocalArchive:
		e, err = r.resolveLocalArchive(o, req)
	case origin.RemoteArchive:
		e, err = r.resolveRemoteArchive(o, req)
	case origin.Index:
		e, err = r.resolveIndex(o, req)
	case origin.VCS:
		e, err = r.resolveVCS(o, req)
	default:
		err = fmt.Errorf("unsupported resource kind %s", o.Kind)
	}
	if err != nil {
		return err
	}
	r.chosen[id] = e
	r.fresh.Put(*e)
	return nil
}

// enqueueUsages queues the transitive usages of a resolved package.
func (r *resolver) enqueueUsages(e *lock.Entry, p *project.Project) error {
	for _, u := range p.Usage {
		c, err := usageConstraint(u)
		if err != nil {
			return fmt.Errorf("%s: %v", e.Resource, err)
		}
		if _, err := origin.Parse(u.Resource); err != nil {
			return fmt.Errorf("%s: usage %q: %v", e.Resource, u.Resource, err)
		}
		e.Usage = append(e.Usage, lock.Usage{Resource: u.Resource, VersionConstraint: u.VersionConstraint})
		r.queue = append(r.queue, request{resource: u.Resource, constraint: c, requester: e.Resource})
	}
	return nil
}

func (r *resolver) resolveLocalDir(o *origin.Origin, req request) (*lock.Entry, error) {
	dir := o.Source
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(r.opts.Root, filepath.FromSlash(dir))
	}
	p, err := project.ReadProject(dir)
	if err != nil {
		return nil, fmt.Errorf("%s: usage %q: %v", req.requester, req.resource, err)
	}
	v, _ := version.Parse(p.Version)
	if !req.constraint.Check(v) {
		return nil, fmt.Errorf("%s: usage %q wants %s but the directory holds %s %s", req.requester, req.resource, req.constraint, p.Name, p.Version)
	}
	e := &lock.Entry{Resource: o.Identity(), Kind: lock.KindLocal, Publisher: p.Publisher, Name: p.Name, Version: p.Version, Source: o.Source}
	r.res.Skipped = append(r.res.Skipped, o.Identity())
	return e, r.enqueueUsages(e, p)
}

// archivePath is where an archive package is installed.
func (r *resolver) archivePath(p *project.Project) string {
	return filepath.Join(r.libDir, p.InstallName()+project.ArchiveExt)
}

// installArchive stages a verified archive for lib/ unless an identical
// copy is already there, and queues its usages.
func (r *resolver) installArchive(e *lock.Entry, a *project.Archive, src string) error {
	if r.opts.LockOnly {
		return r.enqueueUsages(e, a.Project)
	}
	dst := r.archivePath(a.Project)
	if err := r.claim(dst, e.Resource, a.SHA256); err != nil {
		return err
	}
	if cur, err := digest.File(dst); (err == nil && cur == a.SHA256) || r.alreadyStaged(dst) {
		r.res.Reused = append(r.res.Reused, e.Resource)
		return r.enqueueUsages(e, a.Project)
	}
	stage, err := r.staging()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	tmp := filepath.Join(stage, filepath.Base(dst))
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	r.items = append(r.items, staged{tmp, dst})
	r.logf("installed %s %s -> %s", a.Project.Name, a.Project.Version, rel(r.opts.Root, dst))
	r.res.Installed = append(r.res.Installed, e.Resource)
	return r.enqueueUsages(e, a.Project)
}

func (r *resolver) resolveLocalArchive(o *origin.Origin, req request) (*lock.Entry, error) {
	if e, ok, err := r.reuseLocked(r.locked(o.Identity(), req.constraint)); ok || err != nil {
		return e, err
	}
	p := o.Source
	if !filepath.IsAbs(p) {
		p = filepath.Join(r.opts.Root, filepath.FromSlash(p))
	}
	a, err := project.OpenArchive(p)
	if err != nil {
		return nil, fmt.Errorf("%s: usage %q: %v", req.requester, req.resource, err)
	}
	defer a.Close()
	v, _ := version.Parse(a.Project.Version)
	if !req.constraint.Check(v) {
		return nil, fmt.Errorf("%s: usage %q wants %s but the archive holds %s %s", req.requester, req.resource, req.constraint, a.Project.Name, a.Project.Version)
	}
	if old := r.old.Find(o.Identity()); old != nil && old.SHA256 != "" && old.SHA256 != a.SHA256 {
		return nil, fmt.Errorf("%s: usage %q: archive SHA-256 %s does not match the lock's %s; remove the lock entry if the archive was meant to change", req.requester, req.resource, a.SHA256, old.SHA256)
	}
	e := &lock.Entry{Resource: o.Identity(), Kind: lock.KindArchive, Publisher: a.Project.Publisher, Name: a.Project.Name, Version: a.Project.Version, Source: o.Source, SHA256: a.SHA256}
	return e, r.installArchive(e, a, p)
}

// locked returns the previous lock entry for an identity when it satisfies
// the constraint, so the lock wins over the constraint.
func (r *resolver) locked(id string, c version.Constraint) *lock.Entry {
	old := r.old.Find(id)
	if old == nil || old.Version == "" {
		return nil
	}
	v, err := version.Parse(old.Version)
	if err != nil || !c.Check(v) {
		return nil
	}
	return old
}

// reuseLocked keeps a locked archive entry when only the lock is being
// written, or when lib/ already holds the archive with the locked digest.
func (r *resolver) reuseLocked(old *lock.Entry) (*lock.Entry, bool, error) {
	if old == nil {
		return nil, false, nil
	}
	stem := (&project.Project{Name: old.Name, Publisher: old.Publisher, Version: old.Version}).InstallName()
	dst := filepath.Join(r.libDir, stem+project.ArchiveExt)
	cur, err := digest.File(dst)
	if r.opts.LockOnly || (err == nil && cur == old.SHA256) {
		if !r.opts.LockOnly {
			if err := r.claim(dst, old.Resource, old.SHA256); err != nil {
				return nil, false, err
			}
			r.res.Reused = append(r.res.Reused, old.Resource)
		}
		e := *old
		e.Usage = nil
		return &e, true, r.requeueLocked(&e, old)
	}
	return nil, false, nil
}

// requeueLocked queues the usages a locked entry recorded — as declared, so
// the constraint and the transport (an SSH address, say) are the package's
// own — and an offline run walks the closure from the lock alone.
func (r *resolver) requeueLocked(e *lock.Entry, old *lock.Entry) error {
	for _, u := range old.Usage {
		c, err := usageConstraint(project.Usage{Resource: u.Resource, VersionConstraint: u.VersionConstraint})
		if err != nil {
			return fmt.Errorf("%s: locked %v", e.Resource, err)
		}
		e.Usage = append(e.Usage, u)
		r.queue = append(r.queue, request{resource: u.Resource, constraint: c, requester: e.Resource})
	}
	return nil
}

func (r *resolver) download(url string, dst string) error {
	if r.opts.Offline {
		return fmt.Errorf("offline: %s is not installed and would have to be downloaded", url)
	}
	req, err := http.NewRequestWithContext(r.ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := r.opts.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %s", url, resp.Status)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(resp.Body, project.MaxArchiveSize+1))
	f.Close()
	if err != nil {
		return err
	}
	if n > project.MaxArchiveSize {
		return fmt.Errorf("%s: larger than the %d byte limit", url, project.MaxArchiveSize)
	}
	return nil
}

func (r *resolver) resolveRemoteArchive(o *origin.Origin, req request) (*lock.Entry, error) {
	id := o.Identity()
	old := r.locked(id, req.constraint)
	if e, ok, err := r.reuseLocked(old); ok || err != nil {
		return e, err
	}
	tmp, err := os.MkdirTemp("", "syspi-dl-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	file := filepath.Join(tmp, "download.kpar")
	if err := r.download(o.Source, file); err != nil {
		return nil, fmt.Errorf("%s: usage %q: %v", req.requester, req.resource, err)
	}
	a, err := project.OpenArchive(file)
	if err != nil {
		return nil, fmt.Errorf("%s: usage %q: %v", req.requester, req.resource, err)
	}
	defer a.Close()
	if old != nil && old.SHA256 != a.SHA256 {
		return nil, fmt.Errorf("%s: usage %q: downloaded archive SHA-256 %s does not match the lock's %s", req.requester, req.resource, a.SHA256, old.SHA256)
	}
	v, _ := version.Parse(a.Project.Version)
	if !req.constraint.Check(v) {
		return nil, fmt.Errorf("%s: usage %q wants %s but the archive holds %s %s", req.requester, req.resource, req.constraint, a.Project.Name, a.Project.Version)
	}
	e := &lock.Entry{Resource: id, Kind: lock.KindArchive, Publisher: a.Project.Publisher, Name: a.Project.Name, Version: a.Project.Version, Source: o.Source, SHA256: a.SHA256}
	return e, r.installArchive(e, a, file)
}

func (r *resolver) resolveIndex(o *origin.Origin, req request) (*lock.Entry, error) {
	id := o.Identity()
	old := r.locked(id, req.constraint)
	if e, ok, err := r.reuseLocked(old); ok || err != nil {
		return e, err
	}
	if r.opts.Index == "" {
		return nil, fmt.Errorf("%s: usage %q: no sysand index configured (set SYSPI_INDEX or pass -index)", req.requester, req.resource)
	}
	if r.opts.Offline {
		return nil, fmt.Errorf("%s: usage %q: offline and not installed", req.requester, req.resource)
	}
	dir, err := index.ProjectPath(o.Publisher, o.Name)
	if err != nil {
		return nil, fmt.Errorf("%s: usage %q: %v", req.requester, req.resource, err)
	}
	var want *index.VersionEntry
	if old != nil {
		want = &index.VersionEntry{Version: old.Version, KparDigest: "sha256:" + old.SHA256}
	} else {
		vs, err := index.ReadVersions(r.ctx, r.opts.HTTP, r.opts.Index, o.Publisher, o.Name)
		if err != nil {
			if errors.Is(err, index.ErrNotFound) {
				return nil, fmt.Errorf("%s: usage %q: %s is not in the index %s", req.requester, req.resource, id, r.opts.Index)
			}
			return nil, fmt.Errorf("%s: usage %q: %v", req.requester, req.resource, err)
		}
		var avail []string
		for _, e := range vs.Available() {
			v, _ := version.Parse(e.Version)
			avail = append(avail, e.Version)
			if want == nil && req.constraint.Check(v) {
				want = &e
			}
		}
		if want == nil {
			return nil, fmt.Errorf("%s: usage %q: no version satisfies %s; available: %s", req.requester, req.resource, req.constraint, strings.Join(avail, ", "))
		}
	}
	tmp, err := os.MkdirTemp("", "syspi-idx-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	file := filepath.Join(tmp, "project.kpar")
	data, err := index.Fetch(r.ctx, r.opts.HTTP, r.opts.Index, dir+"/"+want.Version+"/project.kpar", project.MaxArchiveSize)
	if err != nil {
		return nil, fmt.Errorf("%s: usage %q: %v", req.requester, req.resource, err)
	}
	if err := os.WriteFile(file, data, 0o644); err != nil {
		return nil, err
	}
	a, err := project.OpenArchive(file)
	if err != nil {
		return nil, fmt.Errorf("%s: usage %q: %v", req.requester, req.resource, err)
	}
	defer a.Close()
	if "sha256:"+a.SHA256 != want.KparDigest {
		return nil, fmt.Errorf("%s: usage %q: %s %s: archive SHA-256 %s does not match the recorded %s", req.requester, req.resource, id, want.Version, a.SHA256, strings.TrimPrefix(want.KparDigest, "sha256:"))
	}
	if a.Project.Version != want.Version {
		return nil, fmt.Errorf("%s: usage %q: index lists %s but the archive's .project.json says %s", req.requester, req.resource, want.Version, a.Project.Version)
	}
	if !strings.EqualFold(a.Project.Publisher, o.Publisher) || !strings.EqualFold(a.Project.Name, o.Name) {
		return nil, fmt.Errorf("%s: usage %q: the index served %s/%s, not %s/%s", req.requester, req.resource, a.Project.Publisher, a.Project.Name, o.Publisher, o.Name)
	}
	src := strings.TrimSuffix(r.opts.Index, "/") + "/" + dir + "/" + want.Version + "/project.kpar"
	e := &lock.Entry{Resource: id, Kind: lock.KindArchive, Publisher: a.Project.Publisher, Name: a.Project.Name, Version: a.Project.Version, Source: src, SHA256: a.SHA256}
	return e, r.installArchive(e, a, file)
}

// treePath is where a VCS package is installed.
func treePath(libDir string, p *project.Project) string {
	return filepath.Join(libDir, p.InstallName())
}

// installedTreeMatches reports whether dir holds a tree with the locked
// digest, ignoring a .meta.json this tool generated.
func installedTreeMatches(dir string, old *lock.Entry) bool {
	if _, err := os.Stat(filepath.Join(dir, project.ProjectFile)); err != nil {
		return false
	}
	var skip []string
	if old.MetaGenerated {
		skip = append(skip, project.MetaFile)
	}
	d, err := digest.TreeExcept(dir, skip...)
	return err == nil && d == old.SHA256
}

// sysandInstalled looks in .sysand/lib, which a sysand user already has
// (read, never written), for a tree of the locked package whose digest is
// the locked one; a copy with another digest is reported and not trusted.
func (r *resolver) sysandInstalled(old *lock.Entry) string {
	dir := filepath.Join(r.opts.Root, filepath.FromSlash(project.SysandLibDir))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		p := filepath.Join(dir, ent.Name())
		pr, err := project.ReadProject(p)
		if err != nil || pr.Name != old.Name || pr.Version != old.Version || (old.Publisher != "" && pr.Publisher != "" && pr.Publisher != old.Publisher) {
			continue
		}
		if installedTreeMatches(p, old) {
			return p
		}
		if d, err := digest.TreeExcept(p, project.MetaFile); err == nil && d == old.SHA256 {
			return p
		}
		r.logf("%s %s at %s does not have the locked tree SHA-256 %s; installing under %s instead", old.Name, old.Version, rel(r.opts.Root, p), old.SHA256, project.LibDir)
	}
	return ""
}

func (r *resolver) resolveVCS(o *origin.Origin, req request) (*lock.Entry, error) {
	id := o.Identity()
	old := r.locked(id, req.constraint)
	if old != nil && !r.opts.LockOnly {
		stem := (&project.Project{Name: old.Name, Publisher: old.Publisher, Version: old.Version}).InstallName()
		dst := filepath.Join(r.libDir, stem)
		if installedTreeMatches(dst, old) {
			if err := r.claim(dst, id, old.SHA256); err != nil {
				return nil, err
			}
			e := *old
			e.Usage = nil
			r.res.Reused = append(r.res.Reused, id)
			return &e, r.requeueLocked(&e, old)
		}
		if p := r.sysandInstalled(old); p != "" {
			r.logf("%s %s already installed by sysand at %s", old.Name, old.Version, rel(r.opts.Root, p))
			e := *old
			e.Usage = nil
			r.res.Reused = append(r.res.Reused, id)
			return &e, r.requeueLocked(&e, old)
		}
	}
	if old != nil && r.opts.LockOnly {
		e := *old
		e.Usage = nil
		return &e, r.requeueLocked(&e, old)
	}
	if r.opts.Offline {
		return nil, fmt.Errorf("%s: usage %q: offline and %s is not installed under %s", req.requester, req.resource, id, project.LibDir)
	}
	src, err := vcs.Open(o.VCS, o.Source)
	if err != nil {
		return nil, fmt.Errorf("%s: usage %q: %v", req.requester, req.resource, err)
	}
	var sel vcs.Version
	if old != nil {
		v, _ := version.Parse(old.Version)
		sel = vcs.Version{Tag: old.Tag, Version: v}
	} else {
		vs, err := src.ListVersions(r.ctx)
		if err != nil {
			return nil, fmt.Errorf("%s: usage %q: %v", req.requester, req.resource, err)
		}
		if sel, err = vcs.Select(vs, req.constraint); err != nil {
			return nil, fmt.Errorf("%s: usage %q: %v", req.requester, req.resource, err)
		}
	}
	revision, err := src.Revision(r.ctx, sel)
	if err != nil {
		return nil, fmt.Errorf("%s: usage %q: %v", req.requester, req.resource, err)
	}
	if old != nil && old.Revision != "" && old.Revision != revision && o.VCS != "svn" {
		return nil, fmt.Errorf("%s: usage %q: tag %s of %s now points at %s %s, the lock recorded %s", req.requester, req.resource, sel.Tag, o.Source, o.VCS, revision, old.Revision)
	}
	scratchParent := ""
	if !r.opts.LockOnly {
		if scratchParent, err = r.staging(); err != nil {
			return nil, err
		}
	}
	scratch, err := os.MkdirTemp(scratchParent, ".syspi-fetch-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(scratch)
	exported := filepath.Join(scratch, "export")
	if err := src.Export(r.ctx, sel, exported); err != nil {
		return nil, fmt.Errorf("%s: usage %q: %v", req.requester, req.resource, err)
	}
	tree := exported
	if o.Subpath != "" {
		tree = filepath.Join(exported, filepath.FromSlash(o.Subpath))
		if st, err := os.Stat(tree); err != nil || !st.IsDir() {
			return nil, fmt.Errorf("%s: usage %q: tag %s of %s has no directory %s", req.requester, req.resource, sel.Tag, o.Source, o.Subpath)
		}
	}
	// Verify before reading: checksums first when the tree ships metadata.
	meta, hasMeta, err := project.ReadMeta(tree)
	if err != nil {
		return nil, fmt.Errorf("%s: usage %q: tag %s: %v", req.requester, req.resource, sel.Tag, err)
	}
	if hasMeta {
		unrecorded, err := project.VerifyChecksums(tree, meta)
		if err != nil {
			return nil, fmt.Errorf("%s: usage %q: tag %s: %v", req.requester, req.resource, sel.Tag, err)
		}
		if len(unrecorded) > 0 {
			r.logf("%s: tag %s: %s records no checksum for %d of %d files; their integrity rests on the tree hash", id, sel.Tag, project.MetaFile, len(unrecorded), len(unrecorded)+len(meta.Checksum))
		}
	}
	p, err := project.ReadProject(tree)
	if err != nil {
		where := "the root of tag " + sel.Tag
		if o.Subpath != "" {
			where = o.Subpath + " at tag " + sel.Tag
		}
		return nil, fmt.Errorf("%s: usage %q: %s of %s is not a package: %v (a package is a repository whose tagged tree has %s)", req.requester, req.resource, where, o.Source, err, project.ProjectFile)
	}
	// Exact text, so build metadata must agree too (Version.Equal ignores it).
	if p.Version != strings.TrimPrefix(sel.Tag, "v") {
		return nil, fmt.Errorf("%s: usage %q: tag %s of %s carries version %s but its %s says \"version\": %q; the tag must equal the version", req.requester, req.resource, sel.Tag, o.Source, sel.Version, project.ProjectFile, p.Version)
	}
	sum, err := digest.Tree(tree)
	if err != nil {
		return nil, fmt.Errorf("%s: usage %q: tag %s: %v", req.requester, req.resource, sel.Tag, err)
	}
	if old != nil && old.SHA256 != "" && old.SHA256 != sum {
		return nil, fmt.Errorf("%s: usage %q: tag %s of %s exports tree SHA-256 %s, the lock recorded %s", req.requester, req.resource, sel.Tag, o.Source, sum, old.SHA256)
	}
	e := &lock.Entry{Resource: id, Kind: lock.KindVCS, Publisher: p.Publisher, Name: p.Name, Version: p.Version, VCS: o.VCS, Source: o.Source, Tag: sel.Tag, Revision: revision, SHA256: sum, MetaGenerated: !hasMeta}
	if r.opts.LockOnly {
		return e, r.enqueueUsages(e, p)
	}
	if !hasMeta {
		m, err := project.GenerateMeta(tree, project.CreatedTime())
		if err != nil {
			return nil, err
		}
		if err := m.Write(tree); err != nil {
			return nil, err
		}
		r.logf("%s %s: tag %s has no %s; generated one for the installed copy", p.Name, p.Version, sel.Tag, project.MetaFile)
	}
	dst := treePath(r.libDir, p)
	if err := r.claim(dst, id, sum); err != nil {
		return nil, err
	}
	if r.alreadyStaged(dst) {
		r.res.Reused = append(r.res.Reused, id)
		return e, r.enqueueUsages(e, p)
	}
	stagedTree := filepath.Join(r.stage, p.InstallName())
	if err := os.RemoveAll(stagedTree); err != nil {
		return nil, err
	}
	if err := os.Rename(tree, stagedTree); err != nil {
		return nil, err
	}
	r.items = append(r.items, staged{stagedTree, dst})
	r.logf("installed %s %s (%s %s %s) -> %s", p.Name, p.Version, o.VCS, sel.Tag, revision, rel(r.opts.Root, dst))
	r.res.Installed = append(r.res.Installed, id)
	return e, r.enqueueUsages(e, p)
}

func rel(root, p string) string {
	if r, err := filepath.Rel(root, p); err == nil && !strings.HasPrefix(r, "..") {
		return filepath.ToSlash(r)
	}
	return p
}

// SPDX-License-Identifier: Apache-2.0

// Package install resolves the usages of a project to exact packages,
// records them in .project.lock.json and installs them under lib/ beside
// the project's .project.json. A lock entry wins over a constraint; a
// project whose lock and lib/ already satisfy it is never fetched again.
package install

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Open-MBEE/SysPI/internal/digest"
	"github.com/Open-MBEE/SysPI/internal/lock"
	"github.com/Open-MBEE/SysPI/internal/netrc"
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
	// Log receives one line per action; nil discards.
	Log io.Writer
	// HTTP fetches remote archives and index files; nil uses http.DefaultClient.
	HTTP *http.Client
	// LockOnly resolves and writes the lock without installing anything.
	LockOnly bool
	// Prune removes from lib/ the installed copies the previous lock placed
	// there that the new lock no longer claims (see Remove).
	Prune bool
	// Update names, by repository key, the usages re-resolved to the highest
	// version their constraint allows, ignoring the lock; UpdateAll does so
	// for every repository of the closure (see Update and Outdated).
	Update    []string
	UpdateAll bool
	// DryRun resolves as LockOnly does but writes nothing: the Result
	// carries the lock the run would have written.
	DryRun bool
}

// Result reports what a run did.
type Result struct {
	Lock *lock.Lock
	// Installed, Reused and Skipped name packages by canonical resource.
	Installed, Reused, Skipped []string
	// LockWritten reports whether .project.lock.json changed on disk.
	LockWritten bool
	// RemovedUsages are the usages Remove dropped from .project.json, as written there.
	RemovedUsages []string
	// Pruned lists the lib/ paths (relative to Root) a pruning run removed.
	Pruned []string
	// Changes lists, in resource order, every lock entry the run moved,
	// added or dropped against the previous lock.
	Changes []Change
}

// Change is one lock entry that differs between the previous and the new lock.
type Change struct {
	Resource string `json:"resource"`
	Name     string `json:"name,omitempty"`
	// State is "moved" (both locks have the entry, at different versions),
	// "added" (only the new lock has it) or "dropped" (only the old one).
	State string `json:"state"`
	// From and To are the versions before and after, as the locks record
	// them (a standard library has none).
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
}

type request struct {
	resource   string
	constraint version.Constraint
	requester  string
	origin     *origin.Origin
	identity   string
}

type resolver struct {
	ctx    context.Context
	opts   Options
	old    *lock.Lock
	fresh  *lock.Lock
	chosen map[string]*lock.Entry // identity -> entry resolved in this run
	// sums holds, per archive identity, the digests of its bytes known in
	// this run (the lock's SHA-256, the checksums verified on download or
	// against the installed copy), so a second usage is held to them too.
	sums   map[string]map[string]string
	queue  []request
	res    *Result
	libDir string
	// stage holds newly fetched packages until every usage resolved; slots
	// maps each lib/ destination to the resource and digest that claimed it.
	stage          string
	items          []staged
	slots          map[string]slot
	claimed        []string
	http           *http.Client
	log            bytes.Buffer
	tags           map[repository][]vcs.Version
	lockCandidates map[repositoryTag]verifiedCandidate
}

// httpClient builds the credential-carrying client on first use, so an
// offline run never reads the netrc.
func (r *resolver) httpClient() (*http.Client, error) {
	if r.http != nil {
		return r.http, nil
	}
	if r.opts.HTTP != nil {
		r.http = r.opts.HTTP
		return r.http, nil
	}
	c, err := netrc.Client()
	if err != nil {
		return nil, fmt.Errorf("reading %s: %v", netrc.Path(), err)
	}
	r.http = c
	return c, nil
}

type staged struct{ src, dst string }

type slot struct{ resource, sha256 string }

// Run resolves, locks and installs the project at opts.Root.
func Run(ctx context.Context, opts Options) (*Result, error) {
	if opts.Log == nil {
		opts.Log = io.Discard
	}
	root, err := filepath.Abs(opts.Root)
	if err != nil {
		return nil, err
	}
	opts.Root = root
	if opts.DryRun {
		opts.LockOnly = true
	}
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
	if err := keyResources(old); err != nil {
		return nil, err
	}
	r := &resolver{ctx: ctx, opts: opts, old: old, fresh: lock.New(), chosen: map[string]*lock.Entry{}, sums: map[string]map[string]string{}, res: &Result{}, libDir: filepath.Join(root, project.LibDir), slots: map[string]slot{}, tags: map[repository][]vcs.Version{}}
	for _, u := range proj.Usage {
		req, err := parseRequest(u, project.ProjectFile)
		if err != nil {
			return nil, err
		}
		r.queue = append(r.queue, req)
	}
	defer func() {
		if r.stage != "" {
			os.RemoveAll(r.stage)
		}
	}()
	if err := r.solve(); err != nil {
		return nil, err
	}
	r.res.Changes = changes(r.old, r.fresh)
	if opts.DryRun {
		r.res.Lock = r.fresh
		r.log.WriteTo(opts.Log)
		return r.res, nil
	}
	// Everything resolved and verified: publish the staged packages, then
	// the lock, restoring every replaced package if any step fails.
	backups, err := r.publish()
	if err != nil {
		return nil, err
	}
	r.res.Lock = r.fresh
	r.res.LockWritten, err = r.fresh.Write(root, project.LockFile)
	if err != nil {
		r.rollback(backups)
		return nil, err
	}
	for _, b := range backups {
		os.RemoveAll(b.src)
	}
	if opts.Prune {
		r.prune()
	}
	r.log.WriteTo(opts.Log)
	return r.res, nil
}

// publish moves each staged package into lib/, parking the package it
// replaces beside the stage; the returned backups map each parked copy
// (src) back to its destination (dst).
func (r *resolver) publish() (backups []staged, err error) {
	if len(r.items) > 0 {
		if err := os.MkdirAll(r.libDir, 0o755); err != nil {
			return nil, err
		}
	}
	defer func() {
		if err != nil {
			r.rollback(backups)
		}
	}()
	for i, it := range r.items {
		if _, statErr := os.Lstat(it.dst); statErr == nil {
			park := filepath.Join(r.stage, fmt.Sprintf("old-%d", i))
			if err = os.Rename(it.dst, park); err != nil {
				return backups, err
			}
			backups = append(backups, staged{src: park, dst: it.dst})
		}
		if err = os.Rename(it.src, it.dst); err != nil {
			return backups, err
		}
	}
	return backups, nil
}

// rollback undoes publish: removes what it placed and restores what it
// parked, so lib/ matches the lock that is still on disk.
func (r *resolver) rollback(backups []staged) {
	for _, it := range r.items {
		if _, err := os.Lstat(it.src); err != nil { // moved into place
			os.RemoveAll(it.dst)
		}
	}
	for _, b := range backups {
		os.Rename(b.src, b.dst)
	}
}

// parseRequest resolves a usage to its origin, identity and constraint once.
func parseRequest(u project.Usage, requester string) (request, error) {
	o, err := origin.Parse(u.Resource)
	if err != nil {
		return request{}, fmt.Errorf("%s: usage %q: %w", requester, u.Resource, err)
	}
	c, err := usageConstraint(u, o)
	if err != nil {
		return request{}, fmt.Errorf("%s: %w", requester, err)
	}
	return request{resource: u.Resource, constraint: c, requester: requester, origin: o, identity: o.Identity()}, nil
}

// usageConstraint derives the constraint of a usage: versionConstraint when
// given, else an exact @<version> from the resource, else any version.
func usageConstraint(u project.Usage, o *origin.Origin) (version.Constraint, error) {
	if u.VersionConstraint != "" {
		c, err := version.ParseConstraint(u.VersionConstraint)
		if err != nil {
			return c, fmt.Errorf("usage %q: %v", u.Resource, err)
		}
		return c, nil
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
	if cur, ok := r.slots[dst]; ok {
		if cur.resource != resource && cur.sha256 != sha256 {
			return conflictf("%s and %s both install as %s with different contents (SHA-256 %s vs %s); rename one of the packages", cur.resource, resource, rel(r.opts.Root, dst), cur.sha256, sha256)
		}
		return nil
	}
	r.slots[dst] = slot{resource, sha256}
	r.claimed = append(r.claimed, dst)
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

// staging keeps new packages on the destination filesystem, even before lib/ exists.
func (r *resolver) staging() (string, error) {
	if r.stage != "" {
		return r.stage, nil
	}
	parent := r.libDir
	if _, err := os.Stat(parent); os.IsNotExist(err) {
		parent = r.opts.Root
	} else if err != nil {
		return "", err
	}
	stage, err := os.MkdirTemp(parent, ".syspi-stage-*")
	if err != nil {
		return "", err
	}
	r.stage = stage
	return stage, nil
}

func (r *resolver) logf(format string, args ...any) {
	fmt.Fprintf(&r.log, format+"\n", args...)
}

func (r *resolver) resolveFixed(o *origin.Origin, req request) error {
	id := o.Identity()
	var e *lock.Entry
	var err error
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
	case origin.VCS:
		e, err = r.resolveVCS(o, req, r.lockedRepository(o, req.constraint), nil, vcs.Version{})
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
		req, err := parseRequest(u, e.Resource)
		if err != nil {
			return err
		}
		e.Usage = append(e.Usage, lock.Usage{Resource: u.Resource, VersionConstraint: u.VersionConstraint})
		r.queue = append(r.queue, req)
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
		return nil, conflictf("%s: usage %q wants %s but the directory holds %s %s", req.requester, req.resource, req.constraint, p.Name, p.Version)
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
	dst := r.archivePath(a.Project)
	if err := r.claim(dst, e.Resource, a.SHA256); err != nil {
		return err
	}
	if r.opts.LockOnly {
		return r.enqueueUsages(e, a.Project)
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
		return nil, conflictf("%s: usage %q wants %s but the archive holds %s %s", req.requester, req.resource, req.constraint, a.Project.Name, a.Project.Version)
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

// lockedRepository is locked by the repository key, but only for an entry
// whose recorded source is the requested location: repo and repo.git differ.
func (r *resolver) lockedRepository(o *origin.Origin, c version.Constraint) *lock.Entry {
	old := r.locked(o.Key(), c)
	if old != nil && old.Source != "" && locationKey(old.Source) != locationKey(o.Source) {
		return nil
	}
	return old
}

// locationKey normalises a repository source for comparison: a <vcs>+ scheme
// prefix and host case do not matter, a file URL is its path; .git and a user name do.
func locationKey(src string) string {
	scheme, rest, ok := strings.Cut(src, "://")
	if !ok {
		return src
	}
	if _, t, ok := strings.Cut(scheme, "+"); ok {
		scheme = t
	}
	scheme = strings.ToLower(scheme)
	if scheme == "file" {
		return filepath.FromSlash(rest)
	}
	authority, p, _ := strings.Cut(rest, "/")
	at := strings.LastIndexByte(authority, '@') + 1
	return scheme + "://" + authority[:at] + strings.ToLower(authority[at:]) + "/" + p
}

// reuseLocked keeps a locked archive entry when only the lock is being
// written, or when lib/ already holds the archive with the locked digest.
func (r *resolver) reuseLocked(old *lock.Entry) (*lock.Entry, bool, error) {
	if old == nil {
		return nil, false, nil
	}
	dst := r.lockedArchivePath(old)
	cur, err := digest.File(dst)
	if r.opts.LockOnly || (err == nil && cur == old.SHA256) {
		if err := r.claim(dst, old.Resource, old.SHA256); err != nil {
			return nil, false, err
		}
		if !r.opts.LockOnly {
			r.res.Reused = append(r.res.Reused, old.Resource)
		}
		e := *old
		e.Usage = nil
		return &e, true, r.requeueLocked(&e, old)
	}
	return nil, false, nil
}

// verifyDeclared holds the archive an entry records to the checksums a
// hosted usage declares: against the digests known this run, else against
// the installed or staged copy. It reports whether every item was checked.
func (r *resolver) verifyDeclared(o *origin.Origin, e *lock.Entry) (bool, error) {
	if len(o.Checksums) == 0 {
		return true, nil
	}
	known := r.known(o.Identity())
	known["sha256"] = e.SHA256
	if err := digest.Compare(o.Checksums, known); err != nil {
		return false, err
	}
	var unchecked []digest.Checksum
	for _, c := range o.Checksums {
		if _, ok := known[c.Algorithm]; !ok {
			unchecked = append(unchecked, c)
		}
	}
	if len(unchecked) == 0 {
		return true, nil
	}
	file := r.archiveFile(e)
	if file == "" {
		return false, nil
	}
	// A copy that is not the locked archive (damaged, say) is no witness; the
	// download replaces it and is checked instead.
	got, err := digest.Sums(file, append(unchecked, digest.Checksum{Algorithm: "sha256", Value: e.SHA256}))
	if err != nil || got["sha256"] != e.SHA256 {
		return false, err
	}
	for algo, v := range got {
		known[algo] = v
	}
	return true, digest.Compare(unchecked, got)
}

// known is the digest map of an archive identity, created on first use.
func (r *resolver) known(id string) map[string]string {
	if r.sums[id] == nil {
		r.sums[id] = map[string]string{}
	}
	return r.sums[id]
}

// unverified names the declared algorithms no digest is known for.
func unverified(o *origin.Origin, known map[string]string) string {
	var out []string
	for _, c := range o.Checksums {
		if _, ok := known[c.Algorithm]; !ok {
			out = append(out, c.Algorithm)
		}
	}
	return strings.Join(out, ", ")
}

// archiveFile is the staged or installed copy of a locked archive, "" when
// there is none to read.
func (r *resolver) archiveFile(e *lock.Entry) string {
	dst := r.lockedArchivePath(e)
	for _, it := range r.items {
		if it.dst == dst {
			return it.src
		}
	}
	if _, err := os.Stat(dst); err == nil {
		return dst
	}
	return ""
}

// lockedArchivePath is where a locked archive entry is installed under lib/.
func (r *resolver) lockedArchivePath(old *lock.Entry) string {
	stem := (&project.Project{Name: old.Name, Publisher: old.Publisher, Version: old.Version}).InstallName()
	return filepath.Join(r.libDir, stem+project.ArchiveExt)
}

// requeueLocked queues the usages a locked entry recorded — as declared, so
// the constraint and the transport (an SSH address, say) are the package's
// own — and an offline run walks the closure from the lock alone.
func (r *resolver) requeueLocked(e *lock.Entry, old *lock.Entry) error {
	for _, u := range old.Usage {
		req, err := parseRequest(project.Usage{Resource: u.Resource, VersionConstraint: u.VersionConstraint}, e.Resource)
		if err != nil {
			return fmt.Errorf("%s: locked %w", e.Resource, errors.Unwrap(err))
		}
		e.Usage = append(e.Usage, u)
		r.queue = append(r.queue, req)
	}
	return nil
}

// keyResources rewrites each repository resource to its key, so an entry and a
// usage match however the .git suffix was spelled. Any other spelling is not an
// identity this tool writes, and the lock is refused rather than guessed at.
func keyResources(l *lock.Lock) error {
	for i, e := range l.Packages {
		o, err := origin.Parse(e.Resource)
		if err != nil || o.Kind != origin.VCS {
			continue
		}
		if id := o.Identity(); id != e.Resource {
			return fmt.Errorf("%s: packages[%d] resource %s is not canonical (this tool writes %s); delete the lock and run syspi lock", project.LockFile, i, e.Resource, id)
		}
		l.Packages[i].Resource = o.Key()
	}
	return nil
}

func (r *resolver) download(url string, dst string) error {
	shown := netrc.RedactURL(url)
	if r.opts.Offline {
		return fmt.Errorf("offline: %s is not installed and would have to be downloaded", shown)
	}
	req, err := http.NewRequestWithContext(r.ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	client, err := r.httpClient()
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %s%s", shown, resp.Status, netrc.Hint(resp.StatusCode, req.URL.Hostname()))
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
		return fmt.Errorf("%s: larger than the %d byte limit", shown, project.MaxArchiveSize)
	}
	return nil
}

func (r *resolver) resolveRemoteArchive(o *origin.Origin, req request) (*lock.Entry, error) {
	id := o.Identity()
	old := r.locked(id, req.constraint)
	// A locked entry is reused only when every declared checksum was checked
	// against it or the installed copy; otherwise the download checks them.
	if old != nil {
		checked, err := r.verifyDeclared(o, old)
		if err != nil {
			return nil, fmt.Errorf("%s: usage %q: %v", req.requester, req.resource, err)
		}
		if checked {
			if e, ok, err := r.reuseLocked(old); ok || err != nil {
				return e, err
			}
		}
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
	// The declared checksums are checked before the archive is opened, so no
	// member of a tampered download is read.
	if err := digest.Verify(file, o.Checksums); err != nil {
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
	if o.Hosted() && a.Project.Name != o.Name {
		return nil, fmt.Errorf("%s: usage %q: the purl names %q but the archive's %s names %q", req.requester, req.resource, o.Name, project.ProjectFile, a.Project.Name)
	}
	known := r.known(id)
	known["sha256"] = a.SHA256
	for _, c := range o.Checksums {
		known[c.Algorithm] = c.Value
	}
	v, _ := version.Parse(a.Project.Version)
	if !req.constraint.Check(v) {
		return nil, conflictf("%s: usage %q wants %s but the archive holds %s %s", req.requester, req.resource, req.constraint, a.Project.Name, a.Project.Version)
	}
	e := &lock.Entry{Resource: id, Kind: lock.KindArchive, Publisher: a.Project.Publisher, Name: a.Project.Name, Version: a.Project.Version, Source: o.Source, SHA256: a.SHA256}
	return e, r.installArchive(e, a, file)
}

// treePath is where a VCS package is installed.
func treePath(libDir string, p *project.Project) string {
	return filepath.Join(libDir, p.InstallName())
}

// installedTreeMatches reports whether dir holds a tree with the locked
// digest, ignoring a .meta.json this tool generated.
// updating reports whether the lock is ignored for this repository: the
// run updates everything, or named this usage's repository.
func (r *resolver) updating(o *origin.Origin) bool {
	if r.opts.UpdateAll {
		return true
	}
	for _, k := range r.opts.Update {
		if k == o.Key() {
			return true
		}
	}
	return false
}

// changes diffs two locks by repository key (the old lock's resources are
// already keyed): every entry moved, added or dropped.
func changes(old, fresh *lock.Lock) []Change {
	var out []Change
	seen := map[string]bool{}
	for i := range fresh.Packages {
		e := &fresh.Packages[i]
		k := repositoryKey(e.Resource)
		seen[k] = true
		c := Change{Resource: e.Resource, Name: e.Name, State: "added", To: e.Version}
		if o := old.Find(k); o != nil {
			if o.Version == e.Version {
				continue
			}
			c.State, c.From = "moved", o.Version
		}
		out = append(out, c)
	}
	for i := range old.Packages {
		e := &old.Packages[i]
		if !seen[repositoryKey(e.Resource)] {
			out = append(out, Change{Resource: e.Resource, Name: e.Name, State: "dropped", From: e.Version})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Resource < out[j].Resource })
	return out
}

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

type repositoryTag struct{ identity, source, tag string }

type verifiedCandidate struct {
	entry   lock.Entry
	project *project.Project
	warning string
}

func (r *resolver) resolveVCS(o *origin.Origin, req request, old *lock.Entry, src vcs.Source, sel vcs.Version) (*lock.Entry, error) {
	id := o.Identity()
	if old != nil && r.opts.Offline && r.updating(o) {
		return nil, fmt.Errorf("%s: usage %q: offline; looking for a newer version of %s needs the repository", req.requester, req.resource, id)
	}
	if old != nil && !r.opts.LockOnly {
		stem := (&project.Project{Name: old.Name, Publisher: old.Publisher, Version: old.Version}).InstallName()
		dst := filepath.Join(r.libDir, stem)
		if installedTreeMatches(dst, old) {
			if err := r.claim(dst, id, old.SHA256); err != nil {
				return nil, err
			}
			e := *old
			e.Resource, e.Source, e.Usage = id, o.Source, nil
			r.res.Reused = append(r.res.Reused, id)
			return &e, r.requeueLocked(&e, old)
		}
	}
	if old != nil && r.opts.LockOnly {
		if err := r.claim(treePath(r.libDir, &project.Project{Name: old.Name, Publisher: old.Publisher, Version: old.Version}), id, old.SHA256); err != nil {
			return nil, err
		}
		e := *old
		e.Resource, e.Source, e.Usage = id, o.Source, nil
		return &e, r.requeueLocked(&e, old)
	}
	if r.opts.Offline {
		return nil, fmt.Errorf("%s: usage %q: offline and %s is not installed under %s", req.requester, req.resource, id, project.LibDir)
	}
	if old != nil {
		v, _ := version.Parse(old.Version)
		sel = vcs.Version{Tag: old.Tag, Version: v}
	}
	key := repositoryTag{id, o.Source, sel.Tag}
	if cached, ok := r.lockCandidates[key]; ok {
		if cached.warning != "" {
			r.logf("%s", cached.warning)
		}
		if err := r.claim(treePath(r.libDir, cached.project), id, cached.entry.SHA256); err != nil {
			return nil, err
		}
		return &cached.entry, r.enqueueUsages(&cached.entry, cached.project)
	}
	if src == nil {
		var err error
		src, err = vcs.Open(o.VCS, o.Source)
		if err != nil {
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
	var warning string
	if hasMeta {
		unrecorded, err := project.VerifyChecksums(tree, meta)
		if err != nil {
			return nil, fmt.Errorf("%s: usage %q: tag %s: %v", req.requester, req.resource, sel.Tag, err)
		}
		if len(unrecorded) > 0 {
			warning = fmt.Sprintf("%s: tag %s: %s records no checksum for %d of %d files; their integrity rests on the tree hash", id, sel.Tag, project.MetaFile, len(unrecorded), len(unrecorded)+len(meta.Checksum))
			r.logf("%s", warning)
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
	dst := treePath(r.libDir, p)
	if err := r.claim(dst, id, sum); err != nil {
		return nil, err
	}
	if r.opts.LockOnly {
		if r.lockCandidates == nil {
			r.lockCandidates = make(map[repositoryTag]verifiedCandidate)
		}
		r.lockCandidates[key] = verifiedCandidate{entry: *e, project: p, warning: warning}
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

// SPDX-License-Identifier: Apache-2.0

package install

import (
	"errors"
	"fmt"
	"maps"
	"os"

	"github.com/Open-MBEE/SysPI/internal/lock"
	"github.com/Open-MBEE/SysPI/internal/origin"
	"github.com/Open-MBEE/SysPI/internal/vcs"
	"github.com/Open-MBEE/SysPI/internal/version"
)

type conflictError struct{ error }

func conflictf(format string, args ...any) error {
	return conflictError{fmt.Errorf(format, args...)}
}

type repository struct{ vcs, source string }

func (r *resolver) solve() error {
	for len(r.queue) > 0 {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		req := r.queue[0]
		r.queue = r.queue[1:]
		o, err := origin.Parse(req.resource)
		if err != nil {
			return fmt.Errorf("%s: usage %q: %v", req.requester, req.resource, err)
		}
		id := o.Identity()
		if entry := r.chosen[id]; entry != nil {
			if entry.Kind != lock.KindStandard {
				v, _ := version.Parse(entry.Version)
				if !req.constraint.Check(v) {
					return conflictf("%s: usage %q wants %s but %s is already resolved to %s (required by another usage)", req.requester, req.resource, req.constraint, id, entry.Version)
				}
			}
			if entry.Kind == lock.KindArchive {
				checked, err := r.verifyDeclared(o, entry)
				if err == nil && !checked {
					err = fmt.Errorf("the declared %s checksum cannot be verified: the archive is not installed", unverified(o, r.sums[id]))
				}
				if err != nil {
					return fmt.Errorf("%s: usage %q: %v", req.requester, req.resource, err)
				}
			}
			continue
		}
		if o.Kind == origin.VCS && !r.opts.Offline {
			return r.solveVCS(o, req)
		}
		if err := r.resolveFixed(o, req); err != nil {
			return err
		}
	}
	return nil
}

func (r *resolver) solveVCS(o *origin.Origin, req request) error {
	save := r.saveChoice()
	try := func(old *lock.Entry, source vcs.Source, candidate vcs.Version) error {
		entry, err := r.resolveVCS(o, req, old, source, candidate)
		if err == nil {
			r.chosen[o.Identity()] = entry
			r.fresh.Put(*entry)
			err = r.solve()
		}
		var conflict conflictError
		if errors.As(err, &conflict) {
			if restoreErr := r.restoreChoice(save); restoreErr != nil {
				return restoreErr
			}
		}
		return err
	}

	var firstConflict conflictError
	locked := r.lockedRepository(o, req.constraint)
	// An update moves only what it names: the named usage takes its highest version, the rest keep their lock entry.
	updateRun := r.opts.UpdateAll || len(r.opts.Update) > 0
	updating := r.updating(o)
	if locked != nil && !updating {
		err := try(locked, nil, vcs.Version{})
		if updateRun || !errors.As(err, &firstConflict) {
			return err
		}
	}

	source, err := vcs.Open(o.VCS, o.Source)
	if err != nil {
		return fmt.Errorf("%s: usage %q: %w", req.requester, req.resource, err)
	}
	key := repository{o.VCS, o.Source}
	tags, listed := r.tags[key]
	if !listed {
		tags, err = source.ListVersions(r.ctx)
		if err != nil {
			return fmt.Errorf("%s: usage %q: %w", req.requester, req.resource, err)
		}
		r.tags[key] = tags
	}
	for i := len(tags) - 1; i >= 0; i-- {
		candidate := tags[i]
		if !req.constraint.Check(candidate.Version) {
			continue
		}
		atLock := locked != nil && candidate.Version.String() == locked.Version
		if updating && atLock {
			return try(locked, nil, vcs.Version{})
		}
		if updating {
			return try(nil, source, candidate)
		}
		if atLock {
			continue
		}
		err := try(nil, source, candidate)
		var conflict conflictError
		if !errors.As(err, &conflict) {
			return err
		}
		if firstConflict.error == nil {
			firstConflict = conflict
		}
	}
	if firstConflict.error != nil {
		return firstConflict
	}
	_, err = vcs.Select(tags, req.constraint)
	return conflictf("%s: usage %q: %v", req.requester, req.resource, err)
}

type choice struct {
	queue                            []request
	packages, items, claims, logSize int
	installed, reused, skipped       int
	sums                             map[string]map[string]string
}

func (r *resolver) saveChoice() choice {
	// Keep later appends from overwriting the queue saved by this choice.
	r.queue = r.queue[:len(r.queue):len(r.queue)]
	// A rejected candidate's downloads are no witness for the archives fetched after it.
	sums := make(map[string]map[string]string, len(r.sums))
	for id, known := range r.sums {
		sums[id] = maps.Clone(known)
	}
	return choice{
		queue: r.queue, claims: len(r.claimed), sums: sums,
		packages: len(r.fresh.Packages), items: len(r.items), logSize: r.log.Len(),
		installed: len(r.res.Installed), reused: len(r.res.Reused), skipped: len(r.res.Skipped),
	}
}

func (r *resolver) restoreChoice(save choice) error {
	for _, item := range r.items[save.items:] {
		if err := os.RemoveAll(item.src); err != nil {
			return fmt.Errorf("discarding rejected package for %s: %w", rel(r.opts.Root, item.dst), err)
		}
	}
	for _, entry := range r.fresh.Packages[save.packages:] {
		delete(r.chosen, entry.Resource)
	}
	r.fresh.Packages = r.fresh.Packages[:save.packages]
	r.items = r.items[:save.items]
	for _, dst := range r.claimed[save.claims:] {
		delete(r.slots, dst)
	}
	r.claimed = r.claimed[:save.claims]
	r.queue = save.queue
	r.sums = make(map[string]map[string]string, len(save.sums))
	for id, known := range save.sums {
		r.sums[id] = maps.Clone(known)
	}
	r.res.Installed = r.res.Installed[:save.installed]
	r.res.Reused = r.res.Reused[:save.reused]
	r.res.Skipped = r.res.Skipped[:save.skipped]
	r.log.Truncate(save.logSize)
	return nil
}

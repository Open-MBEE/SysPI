// SPDX-License-Identifier: Apache-2.0

package install

import (
	"context"
	"fmt"

	"github.com/Open-MBEE/SysPI/internal/origin"
	"github.com/Open-MBEE/SysPI/internal/project"
)

// Update re-resolves the named usages of .project.json — every one when
// resources is empty — to the highest version satisfying their constraints,
// ignoring the lock, then installs and re-locks as Run does and prunes the
// copies the moved entries leave behind. It is the one way a lock entry
// moves. Result.Changes lists each entry moved, added or dropped.
func Update(ctx context.Context, opts Options, resources []string) (*Result, error) {
	if err := selectUpdates(&opts, resources); err != nil {
		return nil, err
	}
	opts.Prune = true
	return Run(ctx, opts)
}

// Outdated resolves as Update would and reports what it would change,
// writing nothing: Result.Changes lists the entries that would move.
func Outdated(ctx context.Context, opts Options, resources []string) (*Result, error) {
	if err := selectUpdates(&opts, resources); err != nil {
		return nil, err
	}
	opts.DryRun = true
	return Run(ctx, opts)
}

// selectUpdates turns the named resources into the repository keys the
// resolver ignores the lock for; each must be a usage of .project.json.
func selectUpdates(opts *Options, resources []string) error {
	if len(resources) == 0 {
		opts.UpdateAll = true
		return nil
	}
	proj, err := project.ReadProject(opts.Root)
	if err != nil {
		return err
	}
	for _, res := range resources {
		o, err := origin.Parse(res)
		if err != nil {
			return err
		}
		if usageIndex(proj, o.Identity()) < 0 {
			return fmt.Errorf("%s: no usage of %s", project.ProjectFile, o.Identity())
		}
		if o.Kind != origin.VCS {
			return fmt.Errorf("%s: %s is not a repository; it has no versions to choose from, and its usage names what it is", project.ProjectFile, o.Identity())
		}
		opts.Update = append(opts.Update, o.Key())
	}
	return nil
}

// Describe is the one-line report of a change: "name old -> new", "name new (new)"
// or "name old (dropped)".
func (c *Change) Describe() string {
	name := c.Name
	if name == "" {
		name = c.Resource
	}
	switch {
	case c.From == "":
		return fmt.Sprintf("%s %s (new)", name, c.To)
	case c.To == "":
		return fmt.Sprintf("%s %s (dropped)", name, c.From)
	}
	return fmt.Sprintf("%s %s -> %s", name, c.From, c.To)
}

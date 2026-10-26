// SPDX-License-Identifier: Apache-2.0

// Package vcs lists and exports tagged versions of a package from a
// version-control repository. One Source implementation exists per system
// (git, svn, hg); each shells out to the user's own executable so their
// credential helpers, SSH and proxy configuration apply. Nothing above the
// Source interface knows which system it is talking to.
package vcs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/Open-MBEE/SysPI/internal/version"
)

// Version is one tagged version a repository offers.
type Version struct {
	// Tag is the tag (git, hg) or tags/ directory (svn) name as it is in the repository.
	Tag string
	// Version is the semantic version the tag name carries.
	Version version.Version
}

// Source is a package repository seen through one version-control system.
type Source interface {
	// VCS is the system name: git, svn or hg.
	VCS() string
	// ListVersions returns every tag whose name is v<semver> or <semver>,
	// ascending by version, preferring the v-prefixed tag when both exist.
	ListVersions(ctx context.Context) ([]Version, error)
	// Export writes the tree at the given version into dst, which must not
	// exist yet. No repository metadata (.git, .hg, .svn) is written.
	Export(ctx context.Context, v Version, dst string) error
	// Revision is the repository's own identity of the version: a git commit
	// SHA, an hg changeset node id, or an svn last-changed revision number.
	Revision(ctx context.Context, v Version) (string, error)
}

// Open returns the Source for a system and repository URL. The repository is
// not contacted. url is what the executable is given: a clone URL, an
// scp-style address or a local path.
func Open(vcsName, url string) (Source, error) {
	switch vcsName {
	case "git":
		return &gitSource{url: url}, nil
	case "svn":
		return &svnSource{url: url}, nil
	case "hg":
		return &hgSource{url: url}, nil
	}
	return nil, fmt.Errorf("version-control system %q is not supported; supported: git, svn, hg", vcsName)
}

// ErrNoExecutable is wrapped by errors reporting a missing git/svn/hg binary.
var ErrNoExecutable = errors.New("executable not found")

// run executes the named executable with args and returns its stdout. A
// missing executable is reported as a clear ErrNoExecutable; any other
// failure carries the command's stderr.
func run(ctx context.Context, dir, name string, args ...string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%w: %q is required to resolve this package but is not on PATH", ErrNoExecutable, name)
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0", // never hang on a password prompt
		"LC_ALL=C",              // stable messages and listings
		"HGPLAIN=1",             // mercurial: no user aliases, stable output
	)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	cmd.Stdin = nil
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), msg)
	}
	return string(out), nil
}

// Available reports whether the executable for a system is on PATH.
func Available(vcsName string) bool {
	_, err := exec.LookPath(vcsName)
	return err == nil
}

// versionsFromNames turns tag or directory names into Versions, keeping only
// names that carry a semantic version, sorted ascending, one per version with
// the v-prefixed name preferred.
func versionsFromNames(names []string) []Version {
	byVersion := map[string]Version{}
	prefixed := map[string]bool{}
	for _, n := range names {
		v, pref, err := version.ParseTag(n)
		if err != nil {
			continue
		}
		key := v.String()
		if _, seen := byVersion[key]; seen && (prefixed[key] || !pref) {
			continue
		}
		byVersion[key] = Version{Tag: n, Version: v}
		prefixed[key] = pref
	}
	out := make([]Version, 0, len(byVersion))
	for _, v := range byVersion {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version.Less(out[j].Version) })
	return out
}

// Select picks the highest listed version satisfying c. The error names the
// constraint and the versions that were available.
func Select(vs []Version, c version.Constraint) (Version, error) {
	var best *Version
	for i := range vs {
		if c.Check(vs[i].Version) && (best == nil || best.Version.Less(vs[i].Version)) {
			best = &vs[i]
		}
	}
	if best == nil {
		avail := make([]string, 0, len(vs))
		for _, v := range vs {
			avail = append(avail, v.Tag)
		}
		if len(avail) == 0 {
			return Version{}, fmt.Errorf("no version satisfies %q: the repository has no v<semver> or <semver> tags", c)
		}
		return Version{}, fmt.Errorf("no version satisfies %q; available: %s", c, strings.Join(avail, ", "))
	}
	return *best, nil
}

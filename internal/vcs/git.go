// SPDX-License-Identifier: Apache-2.0

package vcs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type gitSource struct{ url string }

func (g *gitSource) VCS() string { return "git" }

var gitTagLine = regexp.MustCompile(`^([0-9a-f]{40,64})\trefs/tags/(.+)$`)

// lsRemoteTags returns tag name → commit, using the peeled (^{}) object for
// annotated tags so the revision is always the commit, never the tag object.
func (g *gitSource) lsRemoteTags(ctx context.Context) (map[string]string, error) {
	out, err := run(ctx, "", "git", "ls-remote", "--tags", "--", g.url)
	if err != nil {
		return nil, err
	}
	tags := map[string]string{}
	peeled := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		m := gitTagLine.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if m == nil {
			continue
		}
		if name, ok := strings.CutSuffix(m[2], "^{}"); ok {
			peeled[name] = m[1]
			continue
		}
		tags[m[2]] = m[1]
	}
	for name, sha := range peeled {
		tags[name] = sha
	}
	return tags, nil
}

func (g *gitSource) ListVersions(ctx context.Context) ([]Version, error) {
	tags, err := g.lsRemoteTags(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(tags))
	for n := range tags {
		names = append(names, n)
	}
	return versionsFromNames(names), nil
}

func (g *gitSource) Revision(ctx context.Context, v Version) (string, error) {
	tags, err := g.lsRemoteTags(ctx)
	if err != nil {
		return "", err
	}
	sha, ok := tags[v.Tag]
	if !ok {
		return "", fmt.Errorf("tag %q no longer exists in %s", v.Tag, g.url)
	}
	return sha, nil
}

// Export clones the tag at depth 1 into a scratch directory beside dst, then
// moves the checked-out tree (without .git) to dst.
func (g *gitSource) Export(ctx context.Context, v Version, dst string) error {
	scratch, err := os.MkdirTemp(filepath.Dir(dst), ".syspi-clone-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)
	clone := filepath.Join(scratch, "repo")
	if _, err := run(ctx, "", "git", "-c", "advice.detachedHead=false", "-c", "core.autocrlf=false", "clone", "--quiet", "--depth", "1", "--branch", v.Tag, "--", g.url, clone); err != nil {
		if !strings.Contains(err.Error(), "shallow") {
			return err
		}
		// A dumb HTTP remote cannot serve a shallow clone; take the full history.
		os.RemoveAll(clone)
		if _, err := run(ctx, "", "git", "-c", "advice.detachedHead=false", "-c", "core.autocrlf=false", "clone", "--quiet", "--branch", v.Tag, "--", g.url, clone); err != nil {
			return err
		}
	}
	if err := os.RemoveAll(filepath.Join(clone, ".git")); err != nil {
		return err
	}
	return os.Rename(clone, dst)
}

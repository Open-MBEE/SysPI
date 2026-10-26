// SPDX-License-Identifier: Apache-2.0

package vcs

import (
	"context"
	"fmt"
	"strings"
)

// svnSource is a Subversion repository (or project root within one) whose
// versions are the directories under <url>/tags/, named v<semver> or <semver>
// by the usual tags/ convention.
type svnSource struct{ url string }

func (s *svnSource) VCS() string { return "svn" }

func (s *svnSource) tagURL(v Version) string {
	return strings.TrimSuffix(s.url, "/") + "/tags/" + v.Tag
}

func (s *svnSource) ListVersions(ctx context.Context) ([]Version, error) {
	out, err := run(ctx, "", "svn", "ls", "--non-interactive", "--", strings.TrimSuffix(s.url, "/")+"/tags")
	if err != nil {
		return nil, fmt.Errorf("listing %s/tags (svn packages are tagged by copying to tags/<version>): %w", strings.TrimSuffix(s.url, "/"), err)
	}
	var names []string
	for _, line := range strings.Split(out, "\n") {
		if n, ok := strings.CutSuffix(strings.TrimSpace(line), "/"); ok && n != "" {
			names = append(names, n)
		}
	}
	return versionsFromNames(names), nil
}

// Revision is the last-changed revision of the tag directory. It is local to
// the repository, not content-addressed; the lock's integrity for svn rests
// on the tree hash and this is the reproducibility hint.
func (s *svnSource) Revision(ctx context.Context, v Version) (string, error) {
	out, err := run(ctx, "", "svn", "info", "--non-interactive", "--show-item", "last-changed-revision", "--", s.tagURL(v))
	if err != nil {
		return "", err
	}
	rev := strings.TrimSpace(out)
	if rev == "" {
		return "", fmt.Errorf("svn info for %s returned no last-changed revision", s.tagURL(v))
	}
	return rev, nil
}

// Export writes the tag directory with `svn export`, pinned at its
// last-changed revision so a later copy cannot change what is exported.
func (s *svnSource) Export(ctx context.Context, v Version, dst string) error {
	rev, err := s.Revision(ctx, v)
	if err != nil {
		return err
	}
	_, err = run(ctx, "", "svn", "export", "--non-interactive", "--quiet", "--ignore-keywords", "--", s.tagURL(v)+"@"+rev, dst)
	return err
}

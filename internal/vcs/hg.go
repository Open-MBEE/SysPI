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

type hgSource struct{ url string }

func (h *hgSource) VCS() string { return "hg" }

var hgNode = regexp.MustCompile(`^[0-9a-f]{40}$`)

// ListVersions clones the repository without a working copy and reads
// `hg tags`; Mercurial has no remote tag listing.
func (h *hgSource) ListVersions(ctx context.Context) ([]Version, error) {
	scratch, err := os.MkdirTemp("", "syspi-hg-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(scratch)
	clone := filepath.Join(scratch, "repo")
	if _, err := run(ctx, "", "hg", "clone", "--quiet", "--noupdate", "--", h.url, clone); err != nil {
		return nil, err
	}
	out, err := run(ctx, clone, "hg", "tags", "--quiet")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(out, "\n") {
		if n := strings.TrimSpace(line); n != "" && n != "tip" {
			names = append(names, n)
		}
	}
	return versionsFromNames(names), nil
}

// Revision is the 40-hex changeset node id the tag points at.
func (h *hgSource) Revision(ctx context.Context, v Version) (string, error) {
	out, err := run(ctx, "", "hg", "identify", "--id", "--debug", "-r", v.Tag, "--", h.url)
	if err != nil {
		return "", err
	}
	id := strings.TrimSuffix(strings.TrimSpace(out), "+")
	if !hgNode.MatchString(id) {
		return "", fmt.Errorf("hg identify for tag %q of %s returned %q, not a changeset id", v.Tag, h.url, id)
	}
	return id, nil
}

// Export clones at the tag's changeset and writes the tree with `hg archive`.
// The changeset is resolved first: a tag is recorded in .hgtags by a *later*
// changeset, so a clone limited to the tagged changeset does not know the tag
// by name.
func (h *hgSource) Export(ctx context.Context, v Version, dst string) error {
	node, err := h.Revision(ctx, v)
	if err != nil {
		return err
	}
	scratch, err := os.MkdirTemp(filepath.Dir(dst), ".syspi-hg-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)
	clone := filepath.Join(scratch, "repo")
	if _, err := run(ctx, "", "hg", "clone", "--quiet", "--noupdate", "-r", node, "--", h.url, clone); err != nil {
		return err
	}
	// --no-decode keeps bytes as committed. The .hg_archival.txt stamp and the
	// .hgtags bookkeeping file are Mercurial's own, not the package's, and are
	// excluded so the tree hash matches the same package served from git or svn.
	if _, err := run(ctx, clone, "hg", "archive", "--quiet", "--type", "files", "-r", node, "--no-decode", "--exclude", ".hg_archival.txt", "--exclude", ".hgtags", dst); err != nil {
		return err
	}
	return nil
}

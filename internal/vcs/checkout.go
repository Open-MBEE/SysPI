// SPDX-License-Identifier: Apache-2.0

package vcs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// CheckedOutTag returns the version tag the working copy at dir has checked
// out ("" when none or unknown): a git tag pointing exactly at HEAD, or the
// hg tag of the working revision. Only tags carrying a version count.
func CheckedOutTag(ctx context.Context, dir string) string {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil && Available("git") {
		out, err := run(ctx, dir, "git", "tag", "--points-at", "HEAD")
		if err == nil {
			if vs := versionsFromNames(strings.Fields(out)); len(vs) > 0 {
				return vs[len(vs)-1].Tag
			}
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".hg")); err == nil && Available("hg") {
		out, err := run(ctx, dir, "hg", "log", "-r", ".", "--template", "{tags}")
		if err == nil {
			if vs := versionsFromNames(strings.Fields(out)); len(vs) > 0 {
				return vs[len(vs)-1].Tag
			}
		}
	}
	return ""
}

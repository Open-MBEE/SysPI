// SPDX-License-Identifier: Apache-2.0

package vcs

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
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

// CheckedOutTime returns the commit time of the working copy at dir (git or
// hg) and true, or false when dir is not a checkout. It gives bundle a
// reproducible "created" time when SOURCE_DATE_EPOCH is unset.
func CheckedOutTime(ctx context.Context, dir string) (time.Time, bool) {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil && Available("git") {
		if out, err := run(ctx, dir, "git", "log", "-1", "--format=%ct"); err == nil {
			if n, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64); err == nil {
				return time.Unix(n, 0).UTC(), true
			}
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".hg")); err == nil && Available("hg") {
		if out, err := run(ctx, dir, "hg", "log", "-r", ".", "--template", "{date(date, '%s')}"); err == nil {
			if n, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64); err == nil {
				return time.Unix(n, 0).UTC(), true
			}
		}
	}
	return time.Time{}, false
}

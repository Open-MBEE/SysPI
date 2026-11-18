// SPDX-License-Identifier: Apache-2.0

package install

import (
	"path/filepath"
	"strings"

	"github.com/Open-MBEE/SysPI/internal/digest"
	"github.com/Open-MBEE/SysPI/internal/lock"
	"github.com/Open-MBEE/SysPI/internal/project"
)

// libPath is where under libDir the lock entry's installed copy lives: the
// exported tree of a vcs entry, the .kpar of an archive entry; "" for a
// package that is never installed (a local directory, a standard library)
// or whose name would not be a direct child of libDir.
func libPath(libDir string, e *lock.Entry) string {
	stem := (&project.Project{Name: e.Name, Publisher: e.Publisher, Version: e.Version}).InstallName()
	if stem == "." || stem == ".." || strings.ContainsAny(stem, `/\`) {
		return ""
	}
	var p string
	switch e.Kind {
	case lock.KindVCS:
		p = filepath.Join(libDir, stem)
	case lock.KindArchive:
		p = filepath.Join(libDir, stem+project.ArchiveExt)
	default:
		return ""
	}
	if filepath.Dir(p) != filepath.Clean(libDir) {
		return ""
	}
	return p
}

// installedMatches reports whether the copy at p is the one the lock entry
// describes: the locked tree digest, or the locked .kpar digest.
func installedMatches(p string, e *lock.Entry) bool {
	if e.Kind == lock.KindArchive {
		cur, err := digest.File(p)
		return err == nil && cur == e.SHA256
	}
	return installedTreeMatches(p, e)
}

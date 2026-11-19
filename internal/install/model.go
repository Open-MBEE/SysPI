// SPDX-License-Identifier: Apache-2.0

package install

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Open-MBEE/SysPI/internal/lock"
	"github.com/Open-MBEE/SysPI/internal/origin"
	"github.com/Open-MBEE/SysPI/internal/project"
)

// DependencyModels exports verified textual sources from the project's lock closure.
func DependencyModels(root, scratch string) ([]string, error) {
	p, err := project.ReadProject(root)
	if err != nil {
		return nil, err
	}
	if len(p.Usage) == 0 {
		return nil, nil
	}
	l, ok, err := lock.Read(root, project.LockFile)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%s: no %s; run syspi install", root, project.LockFile)
	}
	if discrepancies := verifyLock(root, p, l); len(discrepancies) > 0 {
		return nil, fmt.Errorf("dependency source export requires a verified installation: %s", discrepancies[0])
	}

	queue := make([]string, 0, len(p.Usage))
	for _, u := range p.Usage {
		queue = append(queue, u.Resource)
	}
	seen := make(map[string]bool)
	staged := make(map[string]bool)
	var sources []string
	for i := 0; i < len(queue); i++ {
		o, err := origin.Parse(queue[i])
		if err != nil {
			return nil, err
		}
		e := lockedEntry(l, o)
		if e == nil {
			return nil, fmt.Errorf("%s: missing lock entry for %s", root, queue[i])
		}
		if seen[e.Resource] {
			continue
		}
		seen[e.Resource] = true
		for _, u := range e.Usage {
			queue = append(queue, u.Resource)
		}

		if e.Kind == lock.KindStandard {
			continue
		}
		dir := libPath(filepath.Join(root, project.LibDir), e)
		if e.Kind == lock.KindLocal {
			dir = e.Source
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(root, filepath.FromSlash(dir))
			}
		}
		dir = filepath.Clean(dir)
		if staged[dir] {
			continue
		}
		staged[dir] = true
		destination := filepath.Join(scratch, fmt.Sprintf("dependency-%d", i))
		var files []string
		if e.Kind == lock.KindArchive {
			files, err = extractLockedText(dir, destination, e.SHA256)
		} else {
			files, err = copyTextModels(dir, destination)
		}
		if err != nil {
			return nil, err
		}
		sources = append(sources, files...)
	}
	return sources, nil
}

func extractLockedText(file, dst, sha256 string) ([]string, error) {
	a, err := project.OpenArchive(file)
	if err != nil {
		return nil, err
	}
	defer a.Close()
	if a.SHA256 != sha256 {
		return nil, fmt.Errorf("%s: archive no longer matches the lock", file)
	}
	return a.ExtractTextModels(dst)
}

func copyTextModels(src, dst string) ([]string, error) {
	members, err := project.Members(src)
	if err != nil {
		return nil, err
	}
	files := project.TextModelMembers(members)
	for i, member := range files {
		name := filepath.FromSlash(member)
		data, err := os.ReadFile(filepath.Join(src, name))
		if err != nil {
			return nil, err
		}
		target := filepath.Join(dst, name)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(target, data, 0o600); err != nil {
			return nil, err
		}
		files[i] = target
	}
	return files, nil
}

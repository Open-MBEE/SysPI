// SPDX-License-Identifier: Apache-2.0

package install

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Open-MBEE/SysPI/internal/lock"
	"github.com/Open-MBEE/SysPI/internal/project"
)

func describeAll(cs []Change) string {
	var s []string
	for i := range cs {
		s = append(s, cs[i].Describe())
	}
	return strings.Join(s, "; ")
}

func lockedVersions(t *testing.T, dir string) string {
	t.Helper()
	l, _, err := lock.Read(dir, project.LockFile)
	if err != nil {
		t.Fatal(err)
	}
	var s []string
	for i := range l.Packages {
		s = append(s, l.Packages[i].Name+" "+l.Packages[i].Version)
	}
	sort.Strings(s)
	return strings.Join(s, ",")
}

func TestUpdateAndOutdated(t *testing.T) {
	widgets, base := fixture(t)
	wres := "git+file://" + filepath.ToSlash(widgets)
	bres := "git+file://" + filepath.ToSlash(base)
	dir := consumer(t, project.Usage{Resource: wres + "@1.0.0"}, project.Usage{Resource: bres + "@2.0.0"})
	run(t, dir, Options{})
	if got := lockedVersions(t, dir); got != "base 2.0.0,widgets 1.0.0" {
		t.Fatalf("locked: %s", got)
	}
	// Loosen the constraints: the lock still wins for every command but update.
	p := &project.Project{Name: "app", Publisher: "test", Version: "0.1.0", Usage: []project.Usage{
		{Resource: wres, VersionConstraint: ">=1.0.0 <1.2.0"},
		{Resource: bres, VersionConstraint: "^2.0.0"},
	}}
	if err := p.Write(dir); err != nil {
		t.Fatal(err)
	}
	res, _ := run(t, dir, Options{})
	if got := lockedVersions(t, dir); got != "base 2.0.0,widgets 1.0.0" || len(res.Changes) != 0 {
		t.Fatalf("install moved the lock: %s %v", got, res.Changes)
	}
	lockBefore, _ := os.ReadFile(filepath.Join(dir, project.LockFile))

	// Naming widgets alone cannot move it: widgets 1.1.0 needs base ^2.1.0
	// and the lock entry for base, not named, wins — the conflict is reported.
	if _, err := Outdated(context.Background(), Options{Root: dir}, []string{wres}); err == nil || !strings.Contains(err.Error(), "already resolved to 2.0.0") {
		t.Fatalf("outdated widgets alone: %v", err)
	}
	// outdated: what would move, nothing written.
	for _, named := range [][]string{nil, {wres, bres}} {
		res, err := Outdated(context.Background(), Options{Root: dir}, named)
		if err != nil {
			t.Fatal(err)
		}
		if got := describeAll(res.Changes); got != "base 2.0.0 -> 2.1.0; widgets 1.0.0 -> 1.1.0" {
			t.Fatalf("outdated %v: %s", named, got)
		}
		if lockNow, _ := os.ReadFile(filepath.Join(dir, project.LockFile)); string(lockNow) != string(lockBefore) {
			t.Fatal("outdated wrote the lock")
		}
		if got := libNames(t, dir); got != "acme-widgets_1.0.0,base_2.0.0" {
			t.Fatalf("outdated touched lib/: %s", got)
		}
	}
	res, err := Outdated(context.Background(), Options{Root: dir}, []string{bres})
	if err != nil || describeAll(res.Changes) != "base 2.0.0 -> 2.1.0" {
		t.Fatalf("outdated base: %v %s", err, describeAll(res.Changes))
	}

	// update of one usage moves that entry, installs it and prunes the old copy.
	res, err = Update(context.Background(), Options{Root: dir}, []string{bres})
	if err != nil {
		t.Fatal(err)
	}
	if got := describeAll(res.Changes); got != "base 2.0.0 -> 2.1.0" || !res.LockWritten || strings.Join(res.Pruned, ",") != "lib/base_2.0.0" {
		t.Fatalf("update base: %s written=%v pruned=%v", got, res.LockWritten, res.Pruned)
	}
	if got := lockedVersions(t, dir); got != "base 2.1.0,widgets 1.0.0" {
		t.Fatalf("locked: %s", got)
	}
	if got := libNames(t, dir); got != "acme-widgets_1.0.0,base_2.1.0" {
		t.Fatalf("lib/: %s", got)
	}
	// update of everything.
	res, err = Update(context.Background(), Options{Root: dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := describeAll(res.Changes); got != "widgets 1.0.0 -> 1.1.0" || strings.Join(res.Pruned, ",") != "lib/acme-widgets_1.0.0" {
		t.Fatalf("update all: %s pruned=%v", got, res.Pruned)
	}
	if got := libNames(t, dir); got != "acme-widgets_1.1.0,base_2.1.0" {
		t.Fatalf("lib/: %s", got)
	}
	res, err = Outdated(context.Background(), Options{Root: dir}, nil)
	if err != nil || len(res.Changes) != 0 {
		t.Fatalf("still outdated: %v %s", err, describeAll(res.Changes))
	}
	// Nothing to move: update changes nothing and reinstalls nothing.
	res, err = Update(context.Background(), Options{Root: dir}, nil)
	if err != nil || len(res.Changes) != 0 || len(res.Installed) != 0 || res.LockWritten {
		t.Fatalf("update at the top: %v %+v", err, res)
	}

	// Refusals.
	if _, err := Update(context.Background(), Options{Root: dir, Offline: true}, nil); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("offline update: %v", err)
	}
	if _, err := Outdated(context.Background(), Options{Root: dir}, []string{"git+file:///no/such/repo"}); err == nil || !strings.Contains(err.Error(), "no usage of") {
		t.Fatalf("unknown usage: %v", err)
	}
	std := "https://www.omg.org/spec/SysML/20250201/Systems-Library.kpar"
	p.Usage = append(p.Usage, project.Usage{Resource: std})
	p.Write(dir)
	if _, err := Outdated(context.Background(), Options{Root: dir}, []string{std}); err == nil || !strings.Contains(err.Error(), "is not a repository") {
		t.Fatalf("standard library named: %v", err)
	}
	// A new usage shows as new, a dropped one as dropped, in resource order.
	res, err = Outdated(context.Background(), Options{Root: dir}, nil)
	if err != nil || describeAll(res.Changes) != "Systems-Library.kpar  (new)" {
		t.Fatalf("new usage: %v %q", err, describeAll(res.Changes))
	}
}

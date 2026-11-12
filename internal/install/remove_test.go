// SPDX-License-Identifier: Apache-2.0

package install

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Open-MBEE/SysPI/internal/lock"
	"github.com/Open-MBEE/SysPI/internal/project"
)

func remove(t *testing.T, dir string, opts Options, resources ...string) (*Result, string) {
	t.Helper()
	var log bytes.Buffer
	opts.Root = dir
	opts.Log = &log
	res, err := Remove(context.Background(), opts, resources)
	if err != nil {
		t.Fatalf("Remove: %v\n%s", err, log.String())
	}
	return res, log.String()
}

func libNames(t *testing.T, dir string) string {
	t.Helper()
	entries, _ := os.ReadDir(filepath.Join(dir, project.LibDir))
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return strings.Join(names, ",")
}

func TestRemove(t *testing.T) {
	widgets, base := fixture(t)
	wres := "git+file://" + filepath.ToSlash(widgets)
	bres := "git+file://" + filepath.ToSlash(base)
	dir := consumer(t, project.Usage{Resource: wres, VersionConstraint: "~1.1.0"}, project.Usage{Resource: bres, VersionConstraint: "^2.1.0"})
	run(t, dir, Options{})
	// A directory the user put under lib/ is never touched.
	write(t, dir, "lib/mine_1.0.0/.project.json", `{"name":"mine","version":"1.0.0"}`+"\n")
	if got := libNames(t, dir); got != "acme-widgets_1.1.0,base_2.1.0,mine_1.0.0" {
		t.Fatalf("lib/ before: %s", got)
	}

	// base is still used by widgets: the usage goes, the package stays locked and installed.
	res, log := remove(t, dir, Options{Offline: true}, bres)
	if len(res.RemovedUsages) != 1 || res.RemovedUsages[0] != bres || len(res.Pruned) != 0 {
		t.Fatalf("remove base: %+v\n%s", res, log)
	}
	if !strings.Contains(log, "stays locked: used by "+ident(t, wres)) {
		t.Fatalf("log lacks the requester:\n%s", log)
	}
	p, _ := project.ReadProject(dir)
	if len(p.Usage) != 1 || p.Usage[0].Resource != wres {
		t.Fatalf("usages after: %+v", p.Usage)
	}
	lk, _, _ := lock.Read(dir, project.LockFile)
	if lk.Find(ident(t, bres)) == nil || len(lk.Packages) != 2 {
		t.Fatalf("lock after removing base:\n%s", lk.Marshal())
	}
	// Removing it again: it is no usage, but the message says who locks it.
	_, err := Remove(context.Background(), Options{Root: dir, Offline: true}, []string{bres})
	if err == nil || !strings.Contains(err.Error(), "no usage of "+ident(t, bres)) || !strings.Contains(err.Error(), "locked because "+ident(t, wres)+" uses it") {
		t.Fatalf("remove of a transitive package: %v", err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, project.ProjectFile))
	// A failing remove (the usage is unknown) leaves .project.json alone.
	if _, err := Remove(context.Background(), Options{Root: dir, Offline: true}, []string{wres, "git+file:///no/such/repo"}); err == nil || !strings.Contains(err.Error(), "no usage of") {
		t.Fatalf("unknown usage: %v", err)
	}
	if after, _ := os.ReadFile(filepath.Join(dir, project.ProjectFile)); !bytes.Equal(before, after) {
		t.Fatal(".project.json changed by a failing remove")
	}
	if got := libNames(t, dir); got != "acme-widgets_1.1.0,base_2.1.0,mine_1.0.0" {
		t.Fatalf("lib/ changed by a failing remove: %s", got)
	}

	// Removing widgets removes its closure (base too), but not the user's directory,
	// and not an installed copy whose contents no longer match the lock.
	write(t, dir, "lib/base_2.1.0/Base.kerml", "package Edited;\n")
	res, log = remove(t, dir, Options{Offline: true}, wres)
	if strings.Join(res.Pruned, ",") != "lib/acme-widgets_1.1.0" || !strings.Contains(log, "kept lib/base_2.1.0: its contents differ") {
		t.Fatalf("remove widgets: %+v\n%s", res, log)
	}
	if got := libNames(t, dir); got != "base_2.1.0,mine_1.0.0" {
		t.Fatalf("lib/ after: %s", got)
	}
	lk, _, _ = lock.Read(dir, project.LockFile)
	if len(lk.Packages) != 0 {
		t.Fatalf("lock after removing everything:\n%s", lk.Marshal())
	}
	p, _ = project.ReadProject(dir)
	if len(p.Usage) != 0 {
		t.Fatalf("usages after: %+v", p.Usage)
	}
}

func TestRemoveArchiveAndSeveralAtOnce(t *testing.T) {
	widgets, _ := fixture(t)
	wres := "git+file://" + filepath.ToSlash(widgets)
	pkg := t.TempDir()
	write(t, pkg, ".project.json", `{"name":"tools","publisher":"acme","version":"3.1.0"}`)
	write(t, pkg, "Tools.sysml", "package Tools;\n")
	kpar := filepath.Join(t.TempDir(), "acme-tools_3.1.0.kpar")
	if _, err := project.Bundle(pkg, kpar, project.CreatedTime()); err != nil {
		t.Fatal(err)
	}
	std := "https://www.omg.org/spec/SysML/20250201/Systems-Library.kpar"
	dir := consumer(t, project.Usage{Resource: wres + "@1.0.0"}, project.Usage{Resource: kpar}, project.Usage{Resource: std})
	run(t, dir, Options{})
	if got := libNames(t, dir); got != "acme-tools_3.1.0.kpar,acme-widgets_1.0.0" {
		t.Fatalf("lib/ before: %s", got)
	}
	res, _ := remove(t, dir, Options{}, kpar, std)
	if strings.Join(res.Pruned, ",") != "lib/acme-tools_3.1.0.kpar" || len(res.RemovedUsages) != 2 {
		t.Fatalf("%+v", res)
	}
	lk, _, _ := lock.Read(dir, project.LockFile)
	if len(lk.Packages) != 1 || lk.Find(ident(t, wres)) == nil {
		t.Fatalf("lock:\n%s", lk.Marshal())
	}
	if got := libNames(t, dir); got != "acme-widgets_1.0.0" {
		t.Fatalf("lib/ after: %s", got)
	}
}

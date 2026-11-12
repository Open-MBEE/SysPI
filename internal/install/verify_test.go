// SPDX-License-Identifier: Apache-2.0

package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Open-MBEE/SysPI/internal/lock"
	"github.com/Open-MBEE/SysPI/internal/project"
)

func problems(ds []Discrepancy) string {
	var s []string
	for i := range ds {
		s = append(s, ds[i].String())
	}
	return strings.Join(s, "\n")
}

func TestVerify(t *testing.T) {
	widgets, base := fixture(t)
	wres := "git+file://" + filepath.ToSlash(widgets)
	pkg := t.TempDir()
	write(t, pkg, ".project.json", `{"name":"tools","publisher":"acme","version":"3.1.0"}`)
	write(t, pkg, "Tools.sysml", "package Tools;\n")
	kpar := filepath.Join(t.TempDir(), "acme-tools_3.1.0.kpar")
	if _, err := project.Bundle(pkg, kpar, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	editable := t.TempDir()
	write(t, editable, ".project.json", `{"name":"dev","version":"0.0.1"}`)
	std := "https://www.omg.org/spec/SysML/20250201/Systems-Library.kpar"
	dir := consumer(t,
		project.Usage{Resource: wres, VersionConstraint: "~1.1.0"},
		project.Usage{Resource: kpar},
		project.Usage{Resource: editable},
		project.Usage{Resource: std},
	)
	if _, _, err := Verify(dir); err == nil || !strings.Contains(err.Error(), "no "+project.LockFile) {
		t.Fatalf("verify without a lock: %v", err)
	}
	run(t, dir, Options{})
	ds, n, err := Verify(dir)
	if err != nil || n != 5 || len(ds) != 0 {
		t.Fatalf("fresh install: %d checked, %v\n%s", n, err, problems(ds))
	}
	lockBefore, _ := os.ReadFile(filepath.Join(dir, project.LockFile))

	// Every kind of discrepancy at once, each reported, nothing repaired.
	write(t, dir, "lib/acme-widgets_1.1.0/Widgets.sysml", "package Widgets { part def Gadget; }\n")
	write(t, dir, "lib/acme-widgets_1.1.0/Extra.sysml", "package Extra;\n")
	write(t, dir, "lib/base_2.1.0/.project.json", `{"name":"base","version":"2.1.1","usage":[{"resource":"`+std+`"}]}`+"\n")
	tampered := tamper(t, filepath.Join(dir, "lib", "acme-tools_3.1.0.kpar"))
	os.Rename(tampered, filepath.Join(dir, "lib", "acme-tools_3.1.0.kpar"))
	os.RemoveAll(editable)
	p, _ := project.ReadProject(dir)
	p.Usage = append(p.Usage, project.Usage{Resource: "https://www.omg.org/spec/KerML/20250201/Semantic-Library.kpar"})
	// The widgets usage now wants a version the lock entry is not at, and a
	// second spelling of the repository (with .git dropped) names another location.
	p.Usage[0].VersionConstraint = "^1.2.0"
	p.Usage = append(p.Usage, project.Usage{Resource: strings.TrimSuffix(wres, ".git") + "@1.1.0"})
	p.Write(dir)
	// A locked package's own usage the lock no longer covers.
	lk, _, _ := lock.Read(dir, project.LockFile)
	lockedWidgets := lk.Find(ident(t, wres))
	lk.Put(lock.Entry{Resource: "pkg:generic/other?vcs_url=git%2Bfile:%2F%2F%2Fno%2Fother", Kind: lock.KindVCS, Name: "other", Version: "1.0.0", VCS: "git", Source: "file:///no/other", SHA256: "0", Usage: []lock.Usage{{Resource: "git+file:///no/such", VersionConstraint: "^1.0.0"}, {Resource: "git+file://" + filepath.ToSlash(base), VersionConstraint: "^3.0.0"}}})
	os.WriteFile(filepath.Join(dir, project.LockFile), lk.Marshal(), 0o644)
	lockBefore, _ = os.ReadFile(filepath.Join(dir, project.LockFile))
	_ = lockedWidgets

	ds, n, err = Verify(dir)
	if err != nil || n != 6 {
		t.Fatalf("verify: %d checked, %v", n, err)
	}
	got := problems(ds)
	for _, want := range []string{
		".project.json: usage of .project.json https://www.omg.org/spec/KerML/20250201/Semantic-Library.kpar: no lock entry; run syspi lock",
		"lib/acme-widgets_1.1.0: tree SHA-256 ",
		"lib/acme-widgets_1.1.0: checksum mismatch for Widgets.sysml",
		"lib/base_2.1.0: tree SHA-256 ",
		"lib/base_2.1.0: .project.json says base 2.1.1, the lock base 2.1.0",
		"lib/base_2.1.0: declares usage " + std + ", which the lock does not record",
		"lib/acme-tools_3.1.0.kpar: SHA-256 ",
		"lib/acme-tools_3.1.0.kpar: checksum mismatch for ",
		"lib/base_2.1.0: checksum mismatch for .project.json",
		editable + ": local directory missing",
		".project.json: usage of .project.json " + wres + ": the lock entry is at 1.1.0, the usage wants ^1.2.0",
		".project.json: usage of .project.json " + strings.TrimSuffix(wres, ".git") + "@1.1.0: no lock entry; run syspi lock",
		"lib/other_1.0.0: installed copy missing",
		"pkg:generic/other?vcs_url=git%2Bfile:%2F%2F%2Fno%2Fother: usage of pkg:generic/other?vcs_url=git%2Bfile:%2F%2F%2Fno%2Fother git+file:///no/such: no lock entry; run syspi lock",
		"pkg:generic/other?vcs_url=git%2Bfile:%2F%2F%2Fno%2Fother: usage of pkg:generic/other?vcs_url=git%2Bfile:%2F%2F%2Fno%2Fother git+file://" + filepath.ToSlash(base) + ": the lock entry is at 2.1.0, the usage wants ^3.0.0",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q", want)
		}
	}
	if len(ds) != 15 {
		t.Errorf("%d discrepancies:\n%s", len(ds), got)
	}
	// Extra.sysml has no recorded checksum, so only the tree hash tells of it.
	if strings.Contains(got, "Extra.sysml") {
		t.Errorf("unrecorded member reported as a checksum problem:\n%s", got)
	}
	// The project's usages come first, then the entries in resource order.
	var seenEntry bool
	for i := range ds {
		if ds[i].Resource == project.ProjectFile {
			if seenEntry {
				t.Errorf("project usage after an entry: %+v", ds[i])
			}
			continue
		}
		if seenEntry && ds[i-1].Resource > ds[i].Resource {
			t.Errorf("not in resource order: %q after %q", ds[i].Resource, ds[i-1].Resource)
		}
		seenEntry = true
	}
	if lockAfter, _ := os.ReadFile(filepath.Join(dir, project.LockFile)); string(lockAfter) != string(lockBefore) {
		t.Error("verify wrote the lock")
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "lib", "acme-widgets_1.1.0", "Widgets.sysml")); !strings.Contains(string(data), "Gadget") {
		t.Error("verify repaired lib/")
	}

	// A missing tree is one discrepancy, not a crash; a missing .meta.json
	// of a tag that shipped one is reported.
	os.RemoveAll(filepath.Join(dir, "lib", "base_2.1.0"))
	os.Remove(filepath.Join(dir, "lib", "acme-widgets_1.1.0", ".meta.json"))
	ds, _, _ = Verify(dir)
	got = problems(ds)
	if !strings.Contains(got, "lib/base_2.1.0: installed copy missing") || !strings.Contains(got, "lib/acme-widgets_1.1.0: .meta.json missing") {
		t.Errorf("after removals:\n%s", got)
	}
}

// A relative local directory is read from the project root, as the resolver reads it.
func TestVerifyRelativeLocalDir(t *testing.T) {
	dir := consumer(t, project.Usage{Resource: "pkgs/dev"})
	write(t, dir, "pkgs/dev/.project.json", `{"name":"dev","version":"0.0.1"}`)
	run(t, dir, Options{})
	wd, _ := os.Getwd()
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)
	ds, _, err := Verify(dir)
	if err != nil || len(ds) != 0 {
		t.Fatalf("%v\n%s", err, problems(ds))
	}
}

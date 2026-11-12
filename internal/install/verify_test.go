// SPDX-License-Identifier: Apache-2.0

package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	widgets, _ := fixture(t)
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
	p.Write(dir)

	ds, n, err = Verify(dir)
	if err != nil || n != 5 {
		t.Fatalf("verify: %d checked, %v", n, err)
	}
	got := problems(ds)
	for _, want := range []string{
		"https://www.omg.org/spec/KerML/20250201/Semantic-Library.kpar: usage of .project.json has no lock entry; run syspi lock",
		"lib/acme-widgets_1.1.0: tree SHA-256 ",
		"lib/acme-widgets_1.1.0: checksum mismatch for Widgets.sysml",
		"lib/base_2.1.0: tree SHA-256 ",
		"lib/base_2.1.0: .project.json says base 2.1.1, the lock base 2.1.0",
		"lib/base_2.1.0: declares usage " + std + ", which the lock does not record",
		"lib/acme-tools_3.1.0.kpar: SHA-256 ",
		"lib/acme-tools_3.1.0.kpar: checksum mismatch for ",
		"lib/base_2.1.0: checksum mismatch for .project.json",
		editable + ": local directory missing",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q", want)
		}
	}
	if len(ds) != 10 {
		t.Errorf("%d discrepancies:\n%s", len(ds), got)
	}
	// Extra.sysml has no recorded checksum, so only the tree hash tells of it.
	if strings.Contains(got, "Extra.sysml") {
		t.Errorf("unrecorded member reported as a checksum problem:\n%s", got)
	}
	// The unlocked usage comes first, then the entries in resource order.
	if ds[0].Path != "" {
		t.Errorf("first discrepancy is not the unlocked usage: %+v", ds[0])
	}
	for i := 2; i < len(ds); i++ {
		if ds[i-1].Resource > ds[i].Resource {
			t.Errorf("not in resource order: %q after %q", ds[i].Resource, ds[i-1].Resource)
		}
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

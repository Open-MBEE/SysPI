// SPDX-License-Identifier: Apache-2.0

package install

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Open-MBEE/SysPI/internal/project"
)

func TestList(t *testing.T) {
	widgets, _ := fixture(t)
	wres := "git+file://" + filepath.ToSlash(widgets)
	pkg := t.TempDir()
	write(t, pkg, ".project.json", `{"name":"tools","publisher":"acme","version":"3.1.0"}`)
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
	if _, err := List(dir); err == nil || !strings.Contains(err.Error(), "no "+project.LockFile) {
		t.Fatalf("list without a lock: %v", err)
	}
	run(t, dir, Options{})

	ls, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for i := range ls {
		l := &ls[i]
		got = append(got, l.Kind+" "+l.Version+" "+l.Path+" "+l.State())
	}
	// Resource order: the base repository (git+file:///…/base.git) sorts
	// before widgets; the standard IRI and the absolute paths sort by their text.
	want := map[string]bool{
		"vcs 2.1.0 lib/base_2.1.0 present":                true,
		"vcs 1.1.0 lib/acme-widgets_1.1.0 present":        true,
		"archive 3.1.0 lib/acme-tools_3.1.0.kpar present": true,
		"local 0.0.1  not installed":                      true,
		"standard   not installed":                        true,
	}
	if len(got) != len(want) {
		t.Fatalf("listing: %q", got)
	}
	for _, g := range got {
		if !want[g] {
			t.Errorf("unexpected line %q", g)
		}
	}
	for i := 1; i < len(ls); i++ {
		if ls[i-1].Resource >= ls[i].Resource {
			t.Fatalf("not in resource order: %q >= %q", ls[i-1].Resource, ls[i].Resource)
		}
	}
	for i := range ls {
		switch ls[i].Kind {
		case "vcs":
			if ls[i].VCS != "git" || ls[i].Revision == "" || ls[i].Source != "" {
				t.Errorf("vcs listing %+v", ls[i])
			}
		case "archive":
			if ls[i].Source != kpar {
				t.Errorf("archive listing %+v", ls[i])
			}
		case "local":
			if ls[i].Source != editable || ls[i].Installed != nil {
				t.Errorf("local listing %+v", ls[i])
			}
		case "standard":
			if ls[i].Resource != std || ls[i].Installed != nil {
				t.Errorf("standard listing %+v", ls[i])
			}
		}
	}

	// A copy that cannot be looked at is an error, not "missing" (file modes
	// do not deny a stat on Windows, nor to root).
	if runtime.GOOS != "windows" && os.Getuid() != 0 {
		os.Chmod(filepath.Join(dir, "lib"), 0o000)
		_, err = List(dir)
		os.Chmod(filepath.Join(dir, "lib"), 0o755)
		if err == nil {
			t.Fatal("unreadable lib/ listed without an error")
		}
	}

	// A copy that is gone is reported missing, not an error.
	os.RemoveAll(filepath.Join(dir, "lib", "acme-widgets_1.1.0"))
	os.Remove(filepath.Join(dir, "lib", "acme-tools_3.1.0.kpar"))
	ls, err = List(dir)
	if err != nil {
		t.Fatal(err)
	}
	var missing []string
	for i := range ls {
		if ls[i].State() == "missing" {
			missing = append(missing, ls[i].Path)
		}
	}
	if strings.Join(missing, ",") != "lib/acme-tools_3.1.0.kpar,lib/acme-widgets_1.1.0" {
		t.Fatalf("missing: %q", missing)
	}
}

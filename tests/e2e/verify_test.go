// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerify(t *testing.T) {
	bin := syspiBinary(t)
	root := t.TempDir()
	gitRes := "git+file://" + filepath.ToSlash(gitFixture(t, root))
	std := "https://www.omg.org/spec/SysML/20250201/Systems-Library.kpar"

	app := filepath.Join(root, "app")
	os.MkdirAll(app, 0o755)
	sh(t, app, bin, "init", "-name", "app")
	if out, err := shErr(app, nil, bin, "verify"); err == nil || !strings.Contains(out, "no .project.lock.json") {
		t.Fatalf("verify before lock: %v\n%s", err, out)
	}
	sh(t, app, bin, "add", gitRes+"@1.0.0")
	sh(t, app, bin, "add", gitRes+"#packages/gears", "~1.1.0")
	sh(t, app, bin, "add", std)

	out := sh(t, app, bin, "verify", "-offline")
	if out != "3 package(s) checked against .project.lock.json: no discrepancy\n" {
		t.Fatalf("verify:\n%s", out)
	}
	if out := sh(t, app, bin, "verify", "-q"); out != "" {
		t.Fatalf("verify -q:\n%s", out)
	}

	lockBefore, _ := os.ReadFile(filepath.Join(app, ".project.lock.json"))
	write(t, app, "lib/acme-gears_1.1.0/Gears.sysml", "package Gears { part def Cog; }\n")
	os.RemoveAll(filepath.Join(app, "lib", "acme-widgets_1.0.0"))
	out, err := shErr(app, nil, bin, "verify")
	if err == nil {
		t.Fatalf("verify passed on a tampered lib/:\n%s", out)
	}
	for _, want := range []string{
		"lib/acme-gears_1.1.0: tree SHA-256 ",
		"lib/acme-gears_1.1.0: checksum mismatch for Gears.sysml",
		"lib/acme-widgets_1.0.0: installed copy missing",
		"syspi: 3 package(s) checked against .project.lock.json: 3 discrepancy(ies)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if lockAfter, _ := os.ReadFile(filepath.Join(app, ".project.lock.json")); string(lockAfter) != string(lockBefore) {
		t.Error("verify wrote the lock")
	}
	if _, err := os.Stat(filepath.Join(app, "lib", "acme-widgets_1.0.0")); err == nil {
		t.Error("verify reinstalled lib/acme-widgets_1.0.0")
	}
	// install repairs what verify reported; verify then passes.
	sh(t, app, bin, "install")
	if out := sh(t, app, bin, "verify", "-C", app); !strings.HasSuffix(out, "no discrepancy\n") {
		t.Fatalf("after install:\n%s", out)
	}
	if out, err := shErr(app, nil, bin, "verify", "extra"); err == nil || !strings.Contains(out, "Usage: syspi verify") {
		t.Fatalf("verify with an argument: %v\n%s", err, out)
	}
}

func TestVerifyAcrossVCS(t *testing.T) {
	requireOrSkip(t, "hg", "svn", "svnadmin")
	bin := syspiBinary(t)
	root := t.TempDir()
	for name, res := range map[string]string{
		"git": "git+file://" + filepath.ToSlash(gitFixture(t, root)),
		"hg":  "hg+file://" + filepath.ToSlash(hgFixture(t, root)),
		"svn": "svn+file://" + strings.TrimPrefix(svnFixture(t, root), "file://"),
	} {
		app := filepath.Join(root, "app-"+name)
		os.MkdirAll(app, 0o755)
		sh(t, app, bin, "init", "-name", "app")
		sh(t, app, bin, "add", res+"@1.1.0")
		if out := sh(t, app, bin, "verify"); !strings.HasSuffix(out, "no discrepancy\n") {
			t.Errorf("%s:\n%s", name, out)
		}
		os.Remove(filepath.Join(app, "lib", "acme-widgets_1.1.0", "Widgets.sysml"))
		if out, err := shErr(app, nil, bin, "verify"); err == nil || !strings.Contains(out, "records a checksum for Widgets.sysml, which is missing") {
			t.Errorf("%s after deleting a member: %v\n%s", name, err, out)
		}
	}
}

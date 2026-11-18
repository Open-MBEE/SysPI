// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemove(t *testing.T) {
	bin := syspiBinary(t)
	root := t.TempDir()
	gitRes := "git+file://" + filepath.ToSlash(gitFixture(t, root))
	std := "https://www.omg.org/spec/SysML/20250201/Systems-Library.kpar"

	app := filepath.Join(root, "app")
	os.MkdirAll(app, 0o755)
	sh(t, app, bin, "init", "-name", "app")
	sh(t, app, bin, "add", gitRes+"@1.0.0")
	sh(t, app, bin, "add", gitRes+"#packages/gears", "~1.1.0")
	sh(t, app, bin, "add", std)
	write(t, app, "lib/mine_1.0.0/Mine.sysml", "package Mine;\n")
	metaBefore, _ := os.ReadFile(filepath.Join(app, ".meta.json"))

	// An unknown usage is refused and nothing changes.
	pjBefore, _ := os.ReadFile(filepath.Join(app, ".project.json"))
	lockBefore, _ := os.ReadFile(filepath.Join(app, ".project.lock.json"))
	out, err := shErr(app, nil, bin, "remove", "-offline", "git+file:///no/such/repo")
	if err == nil || !strings.Contains(out, "no usage of") {
		t.Fatalf("unknown usage: %v\n%s", err, out)
	}
	if pj, _ := os.ReadFile(filepath.Join(app, ".project.json")); string(pj) != string(pjBefore) {
		t.Fatal(".project.json changed by a failing remove")
	}

	// The sub-package goes, offline; widgets and the user's directory stay.
	out = sh(t, app, bin, "remove", "-offline", gitRes+"#packages/gears")
	if !strings.Contains(out, "removed usage ") || !strings.Contains(out, "removed lib/acme-gears_1.1.0\n") || !strings.Contains(out, "2 package(s) locked") {
		t.Fatalf("remove gears:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(app, "lib", "acme-gears_1.1.0")); err == nil {
		t.Error("lib/acme-gears_1.1.0 still there")
	}
	for _, want := range []string{"lib/acme-widgets_1.0.0/Widgets.sysml", "lib/mine_1.0.0/Mine.sysml"} {
		if _, err := os.Stat(filepath.Join(app, filepath.FromSlash(want))); err != nil {
			t.Errorf("%s: %v", want, err)
		}
	}
	lockAfter, _ := os.ReadFile(filepath.Join(app, ".project.lock.json"))
	if string(lockAfter) == string(lockBefore) || strings.Contains(string(lockAfter), "gears") {
		t.Fatalf("lock after remove:\n%s", lockAfter)
	}
	if metaAfter, _ := os.ReadFile(filepath.Join(app, ".meta.json")); string(metaAfter) == string(metaBefore) {
		t.Error(".meta.json not refreshed for the changed .project.json")
	}
	// Removing the same usage twice is an error.
	if out, err := shErr(app, nil, bin, "remove", gitRes+"#packages/gears"); err == nil || !strings.Contains(out, "no usage of") {
		t.Fatalf("second remove: %v\n%s", err, out)
	}
	// The standard library and widgets, in one call, by any accepted spelling.
	out = sh(t, app, bin, "remove", std, "pkg:generic/widgets?vcs_url="+url(gitRes))
	if !strings.Contains(out, "removed lib/acme-widgets_1.0.0\n") || !strings.Contains(out, "0 package(s) locked") {
		t.Fatalf("remove the rest:\n%s", out)
	}
	entries, _ := os.ReadDir(filepath.Join(app, "lib"))
	if len(entries) != 1 || entries[0].Name() != "mine_1.0.0" {
		t.Fatalf("lib/ after: %v", entries)
	}
	pj, _ := os.ReadFile(filepath.Join(app, ".project.json"))
	if strings.Contains(string(pj), "usage") {
		t.Fatalf(".project.json still has usages:\n%s", pj)
	}
}

// url percent-encodes a vcs_url the way the canonical identity does.
func url(vcsURL string) string {
	r := strings.NewReplacer("+", "%2B", "/", "%2F", "@", "%40", "#", "%23")
	return r.Replace(vcsURL)
}

func TestRemoveFromGitHgSvn(t *testing.T) {
	requireOrSkip(t, "git", "hg", "svn", "svnadmin")
	bin := syspiBinary(t)
	root := t.TempDir()
	sources := map[string]string{
		"git": "git+file://" + filepath.ToSlash(gitFixture(t, root)),
		"hg":  "hg+file://" + filepath.ToSlash(hgFixture(t, root)),
		"svn": "svn+file://" + strings.TrimPrefix(svnFixture(t, root), "file://"),
	}
	for name, res := range sources {
		app := filepath.Join(root, "app-"+name)
		os.MkdirAll(app, 0o755)
		sh(t, app, bin, "init", "-name", "app")
		sh(t, app, bin, "add", res, "~1.1.0")
		out := sh(t, app, bin, "remove", "-offline", res)
		if !strings.Contains(out, "removed lib/acme-widgets_1.1.0\n") {
			t.Fatalf("%s:\n%s", name, out)
		}
		if entries, _ := os.ReadDir(filepath.Join(app, "lib")); len(entries) != 0 {
			t.Errorf("%s: lib/ not empty: %v", name, entries)
		}
		lk, _ := os.ReadFile(filepath.Join(app, ".project.lock.json"))
		if !strings.Contains(string(lk), `"packages": []`) {
			t.Errorf("%s: lock:\n%s", name, lk)
		}
	}
}

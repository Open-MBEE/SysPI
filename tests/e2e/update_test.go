// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// looseProject rewrites the consuming .project.json so its one usage allows
// 1.0.0 up to (not including) 1.2.0, where the lock holds 1.0.0.
func looseProject(t *testing.T, app, res string) {
	t.Helper()
	write(t, app, ".project.json", "{\n  \"name\": \"app\",\n  \"version\": \"0.1.0\",\n  \"usage\": [\n    {\n      \"resource\": \""+res+"\",\n      \"versionConstraint\": \">=1.0.0 <1.2.0\"\n    }\n  ]\n}\n")
}

func TestUpdateAndOutdated(t *testing.T) {
	bin := syspiBinary(t)
	root := t.TempDir()
	gitRes := "git+file://" + filepath.ToSlash(gitFixture(t, root))

	app := filepath.Join(root, "app")
	os.MkdirAll(app, 0o755)
	sh(t, app, bin, "init", "-name", "app")
	sh(t, app, bin, "add", gitRes+"@1.0.0")
	looseProject(t, app, gitRes)
	// Every other command keeps the lock entry.
	sh(t, app, bin, "install")
	sh(t, app, bin, "lock")
	lockBefore, _ := os.ReadFile(filepath.Join(app, ".project.lock.json"))
	if !strings.Contains(string(lockBefore), `"version": "1.0.0"`) {
		t.Fatalf("install moved the lock:\n%s", lockBefore)
	}

	out := sh(t, app, bin, "outdated")
	if out != "widgets 1.0.0 -> 1.1.0\n" {
		t.Fatalf("outdated:\n%s", out)
	}
	var cs []map[string]string
	if err := json.Unmarshal([]byte(sh(t, app, bin, "outdated", "-json")), &cs); err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 || cs[0]["name"] != "widgets" || cs[0]["from"] != "1.0.0" || cs[0]["to"] != "1.1.0" || !strings.HasPrefix(cs[0]["resource"], "pkg:generic/widgets?vcs_url=") {
		t.Fatalf("outdated -json: %v", cs)
	}
	if lockNow, _ := os.ReadFile(filepath.Join(app, ".project.lock.json")); string(lockNow) != string(lockBefore) {
		t.Fatal("outdated wrote the lock")
	}
	if _, err := os.Stat(filepath.Join(app, "lib", "acme-widgets_1.1.0")); err == nil {
		t.Fatal("outdated installed something")
	}
	if out, err := shErr(app, nil, bin, "outdated", "-offline"); err == nil || !strings.Contains(out, "offline") {
		t.Fatalf("outdated -offline: %v\n%s", err, out)
	}

	out = sh(t, app, bin, "update")
	for _, want := range []string{"widgets 1.0.0 -> 1.1.0\n", "removed lib/acme-widgets_1.0.0\n", "1 package(s) locked: 1 installed", ".project.lock.json updated"} {
		if !strings.Contains(out, want) {
			t.Errorf("update lacks %q:\n%s", want, out)
		}
	}
	entries, _ := os.ReadDir(filepath.Join(app, "lib"))
	if len(entries) != 1 || entries[0].Name() != "acme-widgets_1.1.0" {
		t.Fatalf("lib/ after update: %v", entries)
	}
	if lockNow, _ := os.ReadFile(filepath.Join(app, ".project.lock.json")); !strings.Contains(string(lockNow), `"version": "1.1.0"`) || !strings.Contains(string(lockNow), `"tag": "v1.1.0"`) {
		t.Fatalf("lock after update:\n%s", lockNow)
	}
	if out := sh(t, app, bin, "outdated"); out != "every package is at the highest version its constraint allows\n" {
		t.Fatalf("outdated after update:\n%s", out)
	}
	if out := sh(t, app, bin, "update", gitRes); !strings.HasPrefix(out, "every package is at the highest version its constraint allows; nothing moved\n") {
		t.Fatalf("update at the top:\n%s", out)
	}
	if out, err := shErr(app, nil, bin, "update", "git+file:///no/such/repo"); err == nil || !strings.Contains(out, "no usage of") {
		t.Fatalf("update of an unknown usage: %v\n%s", err, out)
	}
}

func TestUpdateAcrossVCS(t *testing.T) {
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
		sh(t, app, bin, "add", res+"@1.0.0")
		looseProject(t, app, res)
		if out := sh(t, app, bin, "outdated"); out != "widgets 1.0.0 -> 1.1.0\n" {
			t.Errorf("%s outdated:\n%s", name, out)
		}
		out := sh(t, app, bin, "update", res)
		if !strings.Contains(out, "widgets 1.0.0 -> 1.1.0\n") || !strings.Contains(out, "removed lib/acme-widgets_1.0.0\n") {
			t.Errorf("%s update:\n%s", name, out)
		}
		if _, err := os.Stat(filepath.Join(app, "lib", "acme-widgets_1.1.0", "Widgets.sysml")); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

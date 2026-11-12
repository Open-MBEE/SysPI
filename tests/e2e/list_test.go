// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestList(t *testing.T) {
	bin := syspiBinary(t)
	root := t.TempDir()
	gitRes := "git+file://" + filepath.ToSlash(gitFixture(t, root))
	std := "https://www.omg.org/spec/SysML/20250201/Systems-Library.kpar"

	app := filepath.Join(root, "app")
	os.MkdirAll(app, 0o755)
	sh(t, app, bin, "init", "-name", "app")
	if out, err := shErr(app, nil, bin, "list"); err == nil || !strings.Contains(out, "no .project.lock.json") {
		t.Fatalf("list before lock: %v\n%s", err, out)
	}
	sh(t, app, bin, "add", gitRes+"@1.0.0")
	sh(t, app, bin, "add", gitRes+"#packages/gears", "~1.1.0")
	sh(t, app, bin, "add", std)

	out := sh(t, app, bin, "list", "-offline")
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 4 || !strings.HasPrefix(lines[0], "RESOURCE") {
		t.Fatalf("list:\n%s", out)
	}
	// Resource order: the standard IRI, then widgets' identity, then gears' (its subpath sorts after).
	for i, want := range []string{
		std + " - standard - - not installed",
		"pkg:generic/widgets?vcs_url= 1.0.0 git * lib/acme-widgets_1.0.0 present",
		"pkg:generic/widgets?vcs_url= %23packages%2Fgears 1.1.0 git * lib/acme-gears_1.1.0 present",
	} {
		if got := fields(lines[1+i]); got != want {
			t.Errorf("line %d: %q, want %q", 1+i, got, want)
		}
	}
	if sh(t, app, bin, "list") != out {
		t.Error("list is not deterministic")
	}

	var ls []map[string]any
	if err := json.Unmarshal([]byte(sh(t, app, bin, "list", "-json")), &ls); err != nil {
		t.Fatal(err)
	}
	if len(ls) != 3 || ls[0]["kind"] != "standard" || ls[0]["installed"] != nil || ls[2]["path"] != "lib/acme-gears_1.1.0" || ls[2]["installed"] != true || ls[2]["vcs"] != "git" || ls[2]["version"] != "1.1.0" || ls[2]["revision"] == "" {
		t.Fatalf("json: %v", ls)
	}

	// A copy that is gone shows as missing; the lock is untouched.
	lockBefore, _ := os.ReadFile(filepath.Join(app, ".project.lock.json"))
	os.RemoveAll(filepath.Join(app, "lib", "acme-gears_1.1.0"))
	out = sh(t, app, bin, "list")
	if lines = strings.Split(strings.TrimRight(out, "\n"), "\n"); !strings.HasSuffix(fields(lines[2]), "lib/acme-widgets_1.0.0 present") || !strings.HasSuffix(fields(lines[3]), "lib/acme-gears_1.1.0 missing") {
		t.Fatalf("after removal:\n%s", out)
	}
	if lockAfter, _ := os.ReadFile(filepath.Join(app, ".project.lock.json")); string(lockAfter) != string(lockBefore) {
		t.Error("list wrote the lock")
	}
	if out, err := shErr(app, nil, bin, "list", "extra"); err == nil || !strings.Contains(out, "Usage: syspi list") {
		t.Fatalf("list with an argument: %v\n%s", err, out)
	}
}

// fields is a listing line with its columns one space apart: the resource with
// its percent-encoded parts split out, and a 40-hex revision replaced by "*".
func fields(line string) string {
	var out []string
	for _, f := range strings.Fields(line) {
		if len(f) == 40 && strings.Trim(f, "0123456789abcdef") == "" {
			f = "*"
		}
		if strings.HasPrefix(f, "pkg:generic/") {
			id, rest, _ := strings.Cut(f, "vcs_url=")
			f = id + "vcs_url="
			if i := strings.Index(rest, "%23"); i >= 0 {
				f += " " + rest[i:]
			}
		}
		out = append(out, f)
	}
	return strings.Join(out, " ")
}

func TestListAcrossVCS(t *testing.T) {
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
		out := sh(t, app, bin, "list")
		if got := fields(strings.Split(strings.TrimSpace(out), "\n")[1]); !strings.Contains(got, " 1.1.0 "+name+" ") || !strings.HasSuffix(got, " lib/acme-widgets_1.1.0 present") {
			t.Errorf("%s:\n%s", name, out)
		}
	}
}

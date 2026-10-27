// SPDX-License-Identifier: Apache-2.0

// Package e2e drives the syspi binary end to end on local fixtures only:
// bare git, hg and svn repositories of the same package, a .kpar served
// over httptest, and a consuming project taken through init, add, install
// and lock. No network is used.
package e2e

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var (
	buildOnce sync.Once
	binPath   string
	buildErr  error
)

// syspiBinary builds cmd/syspi once per test run.
func syspiBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "syspi-bin-*")
		if err != nil {
			buildErr = err
			return
		}
		binPath = filepath.Join(dir, "syspi")
		cmd := exec.Command("go", "build", "-o", binPath, "../../cmd/syspi")
		if out, err := cmd.CombinedOutput(); err != nil {
			buildErr = &buildFailure{err, string(out)}
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return binPath
}

type buildFailure struct {
	err error
	out string
}

func (b *buildFailure) Error() string { return "go build: " + b.err.Error() + "\n" + b.out }

var gitEnv = []string{
	"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.org", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.org",
	"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "HGUSER=t <t@example.org>", "HGPLAIN=1", "HGRCPATH=", "SVN_EDITOR=true",
}

func sh(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()
	out, err := shErr(dir, nil, name, args...)
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return out
}

func shErr(dir string, env []string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(), gitEnv...), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func requireOrSkip(t *testing.T, names ...string) {
	t.Helper()
	for _, n := range names {
		if _, err := exec.LookPath(n); err != nil {
			if os.Getenv("SYSPI_REQUIRE_VCS") != "" {
				t.Fatalf("%s is required (SYSPI_REQUIRE_VCS set) but not on PATH", n)
			}
			t.Skipf("%s not on PATH", n)
		}
	}
}

// packageTree writes the "widgets" package at a version, with a
// "packages/gears" sub-package, into dir.
func packageTree(t *testing.T, dir, ver string) {
	t.Helper()
	write(t, dir, ".project.json", "{\n  \"name\": \"widgets\",\n  \"publisher\": \"acme\",\n  \"version\": \""+ver+"\"\n}\n")
	write(t, dir, "Widgets.sysml", "package Widgets {\n  part def Widget;\n  part def Frame;\n}\n")
	write(t, dir, "packages/gears/.project.json", "{\n  \"name\": \"gears\",\n  \"publisher\": \"acme\",\n  \"version\": \""+ver+"\"\n}\n")
	write(t, dir, "packages/gears/Gears.sysml", "package Gears {\n  part def Gear;\n}\n")
}

func gitFixture(t *testing.T, root string) string {
	work := filepath.Join(root, "git-work")
	os.MkdirAll(work, 0o755)
	sh(t, work, "git", "init", "-q", "-b", "main")
	packageTree(t, work, "1.0.0")
	sh(t, work, "git", "add", "-A")
	sh(t, work, "git", "commit", "-q", "-m", "1.0.0")
	sh(t, work, "git", "tag", "-a", "-m", "release", "v1.0.0")
	packageTree(t, work, "1.1.0")
	sh(t, work, "git", "commit", "-q", "-am", "1.1.0")
	sh(t, work, "git", "tag", "v1.1.0")
	// v1.2.0: .project.json still says 1.1.0.
	write(t, work, "Widgets.sysml", "package Widgets;\n")
	sh(t, work, "git", "commit", "-q", "-am", "mismatch")
	sh(t, work, "git", "tag", "v1.2.0")
	// v2.0.0: no .project.json at all.
	sh(t, work, "git", "rm", "-q", ".project.json")
	sh(t, work, "git", "commit", "-q", "-m", "no project")
	sh(t, work, "git", "tag", "v2.0.0")
	bare := filepath.Join(root, "widgets.git")
	sh(t, root, "git", "clone", "-q", "--bare", work, bare)
	return bare
}

func hgFixture(t *testing.T, root string) string {
	repo := filepath.Join(root, "widgets-hg")
	os.MkdirAll(repo, 0o755)
	sh(t, repo, "hg", "init")
	packageTree(t, repo, "1.0.0")
	sh(t, repo, "hg", "add", "-q")
	sh(t, repo, "hg", "commit", "-q", "-m", "1.0.0")
	sh(t, repo, "hg", "tag", "-q", "v1.0.0")
	packageTree(t, repo, "1.1.0")
	sh(t, repo, "hg", "commit", "-q", "-m", "1.1.0")
	sh(t, repo, "hg", "tag", "-q", "v1.1.0")
	return repo
}

func svnFixture(t *testing.T, root string) string {
	repo := filepath.Join(root, "widgets-svn")
	sh(t, root, "svnadmin", "create", repo)
	url := "file://" + filepath.ToSlash(repo)
	stage := filepath.Join(root, "svn-stage")
	os.MkdirAll(filepath.Join(stage, "tags"), 0o755)
	packageTree(t, filepath.Join(stage, "trunk"), "1.0.0")
	sh(t, root, "svn", "import", "-q", "-m", "import", stage, url)
	sh(t, root, "svn", "copy", "-q", "-m", "tag v1.0.0", url+"/trunk", url+"/tags/v1.0.0")
	wc := filepath.Join(root, "svn-wc")
	sh(t, root, "svn", "checkout", "-q", url+"/trunk", wc)
	packageTree(t, wc, "1.1.0")
	sh(t, wc, "svn", "commit", "-q", "-m", "1.1.0")
	sh(t, root, "svn", "copy", "-q", "-m", "tag v1.1.0", url+"/trunk", url+"/tags/v1.1.0")
	return url
}

// treeFiles returns rel path → content for every regular file under dir.
func treeFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		data, err := os.ReadFile(p)
		out[filepath.ToSlash(rel)] = string(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func lockField(t *testing.T, lockJSON, resource, field string) string {
	t.Helper()
	i := strings.Index(lockJSON, "\"resource\": \""+resource+"\"")
	if i < 0 {
		t.Fatalf("lock lacks %s:\n%s", resource, lockJSON)
	}
	rest := lockJSON[i:]
	if end := strings.Index(rest, "\n    }"); end >= 0 {
		rest = rest[:end]
	}
	j := strings.Index(rest, "\""+field+"\": \"")
	if j < 0 {
		return ""
	}
	rest = rest[j+len(field)+5:]
	return rest[:strings.IndexByte(rest, '"')]
}

func TestEndToEnd(t *testing.T) {
	bin := syspiBinary(t)
	root := t.TempDir()
	gitRepo := gitFixture(t, root)
	gitRes := "pkg:git/" + filepath.ToSlash(gitRepo)

	// A .kpar served over HTTP, built by syspi bundle from a package directory.
	tools := filepath.Join(root, "tools")
	write(t, tools, ".project.json", "{\n  \"name\": \"tools\",\n  \"publisher\": \"acme\",\n  \"version\": \"3.0.0\"\n}\n")
	write(t, tools, "Tools.sysml", "package Tools {\n  part def Tool;\n}\n")
	served := filepath.Join(root, "served")
	os.MkdirAll(served, 0o755)
	kpar := filepath.Join(served, "acme-tools_3.0.0.kpar")
	out, err := shErr(root, []string{"SOURCE_DATE_EPOCH=1700000000"}, bin, "bundle", "-o", kpar, tools)
	if err != nil {
		t.Fatalf("bundle: %v\n%s", err, out)
	}
	out2, _ := shErr(root, []string{"SOURCE_DATE_EPOCH=1700000000"}, bin, "bundle", "-o", kpar+".again", tools)
	a, _ := os.ReadFile(kpar)
	b, _ := os.ReadFile(kpar + ".again")
	if !bytes.Equal(a, b) {
		t.Fatalf("bundle is not byte-identical:\n%s\n%s", out, out2)
	}
	srv := httptest.NewServer(http.FileServer(http.Dir(served)))
	defer srv.Close()

	// init → add → install → lock.
	app := filepath.Join(root, "app")
	os.MkdirAll(app, 0o755)
	sh(t, app, bin, "init", "-publisher", "test", "-name", "app")
	write(t, app, "App.sysml", "package App {\n  import Widgets::*;\n  import Gears::*;\n  import Tools::*;\n  part frame : Frame;\n  part gear : Gear;\n  part tool : Tool;\n}\n")
	sh(t, app, bin, "add", gitRes+"@1.0.0")
	sh(t, app, bin, "add", gitRes+"#packages/gears", "~1.1.0")
	sh(t, app, bin, "add", srv.URL+"/acme-tools_3.0.0.kpar")
	sh(t, app, bin, "add", "https://www.omg.org/spec/SysML/20250201/Systems-Library.kpar")
	sh(t, app, bin, "install")
	sh(t, app, bin, "lock")

	for _, want := range []string{"lib/acme-widgets_1.0.0/.project.json", "lib/acme-widgets_1.0.0/.meta.json", "lib/acme-widgets_1.0.0/Widgets.sysml", "lib/acme-gears_1.1.0/Gears.sysml", "lib/acme-tools_3.0.0.kpar", ".project.lock.json", ".meta.json"} {
		if _, err := os.Stat(filepath.Join(app, filepath.FromSlash(want))); err != nil {
			t.Errorf("%s: %v", want, err)
		}
	}
	entries, _ := os.ReadDir(filepath.Join(app, "lib"))
	if len(entries) != 3 {
		t.Errorf("lib/ has %d entries", len(entries))
	}
	lock1, _ := os.ReadFile(filepath.Join(app, ".project.lock.json"))
	if lockField(t, string(lock1), gitRes, "vcs") != "git" || lockField(t, string(lock1), gitRes, "tag") != "v1.0.0" || len(lockField(t, string(lock1), gitRes, "revision")) != 40 || len(lockField(t, string(lock1), gitRes, "sha256")) != 64 {
		t.Errorf("lock entry:\n%s", lock1)
	}
	if strings.Contains(string(lock1), "\"commit\"") {
		t.Error("lock has a commit field")
	}
	if lockField(t, string(lock1), "https://www.omg.org/spec/SysML/20250201/Systems-Library.kpar", "kind") != "standard" {
		t.Error("standard library not recorded as standard")
	}

	// Fresh clone: .project.json + App.sysml only → install reproduces the lock byte for byte.
	clone := filepath.Join(root, "app-clone")
	os.MkdirAll(clone, 0o755)
	pj, _ := os.ReadFile(filepath.Join(app, ".project.json"))
	write(t, clone, ".project.json", string(pj))
	sh(t, clone, bin, "install")
	lock2, _ := os.ReadFile(filepath.Join(clone, ".project.lock.json"))
	if !bytes.Equal(lock1, lock2) {
		t.Fatalf("lock differs on a fresh clone:\n%s\n%s", lock1, lock2)
	}
	// Fresh clone with the lock: installs from it, lock untouched.
	clone2 := filepath.Join(root, "app-clone2")
	os.MkdirAll(clone2, 0o755)
	write(t, clone2, ".project.json", string(pj))
	write(t, clone2, ".project.lock.json", string(lock1))
	sh(t, clone2, bin, "install")
	lock3, _ := os.ReadFile(filepath.Join(clone2, ".project.lock.json"))
	if !bytes.Equal(lock1, lock3) {
		t.Fatal("lock changed when installing from it")
	}
	if ta, tb := treeFiles(t, filepath.Join(app, "lib", "acme-widgets_1.0.0")), treeFiles(t, filepath.Join(clone2, "lib", "acme-widgets_1.0.0")); len(ta) != len(tb) {
		t.Fatal("installed trees differ")
	}

	// Offline: the server and the repository are gone, install and lock still succeed.
	srv.Close()
	os.RemoveAll(gitRepo)
	sh(t, app, bin, "install", "-offline")
	sh(t, app, bin, "lock", "-offline")
	sh(t, app, bin, "sync")
	if out, err := shErr(clone, nil, bin, "install", "-offline"); err == nil || !strings.Contains(out, "offline") {
		// clone has lib/ already, so this succeeds; a clone without lib/ must refuse:
		t.Log(out)
	}
	clone3 := filepath.Join(root, "app-clone3")
	os.MkdirAll(clone3, 0o755)
	write(t, clone3, ".project.json", string(pj))
	write(t, clone3, ".project.lock.json", string(lock1))
	if out, err := shErr(clone3, nil, bin, "install", "-offline"); err == nil || !strings.Contains(out, "offline and") {
		t.Fatalf("offline install without lib/ did not refuse: %v\n%s", err, out)
	}
}

func TestSameTreeFromGitHgSvn(t *testing.T) {
	requireOrSkip(t, "git", "hg", "svn", "svnadmin")
	bin := syspiBinary(t)
	root := t.TempDir()
	sources := map[string]string{
		"git": "pkg:git/" + filepath.ToSlash(gitFixture(t, root)),
		"hg":  "pkg:hg/" + filepath.ToSlash(hgFixture(t, root)),
		"svn": "pkg:svn/" + strings.TrimPrefix(svnFixture(t, root), "file://"),
	}
	var trees []map[string]string
	var sums []string
	var revs []string
	for name, res := range sources {
		app := filepath.Join(root, "app-"+name)
		os.MkdirAll(app, 0o755)
		sh(t, app, bin, "init", "-name", "app")
		sh(t, app, bin, "add", res, "~1.1.0")
		lk, _ := os.ReadFile(filepath.Join(app, ".project.lock.json"))
		if v := lockField(t, string(lk), res, "vcs"); v != name {
			t.Errorf("%s: vcs %q", name, v)
		}
		if v := lockField(t, string(lk), res, "version"); v != "1.1.0" {
			t.Errorf("%s: version %q", name, v)
		}
		sums = append(sums, lockField(t, string(lk), res, "sha256"))
		revs = append(revs, lockField(t, string(lk), res, "revision"))
		tree := treeFiles(t, filepath.Join(app, "lib", "acme-widgets_1.1.0"))
		delete(tree, ".meta.json") // generated per install; its "created" differs
		trees = append(trees, tree)
	}
	for i := 1; i < len(trees); i++ {
		if sums[i] != sums[0] {
			t.Errorf("sha256 differs: %s vs %s", sums[i], sums[0])
		}
		if len(trees[i]) != len(trees[0]) {
			t.Fatalf("tree sizes differ: %d vs %d", len(trees[i]), len(trees[0]))
		}
		for k, v := range trees[0] {
			if trees[i][k] != v {
				t.Errorf("%s differs between installs", k)
			}
		}
		if revs[i] == revs[0] {
			t.Errorf("revisions should differ between systems: %s", revs[i])
		}
	}
}

func TestRefusals(t *testing.T) {
	bin := syspiBinary(t)
	root := t.TempDir()
	gitRepo := gitFixture(t, root)
	gitRes := "pkg:git/" + filepath.ToSlash(gitRepo)

	// An archive with a traversal member and one with a tampered member.
	good := filepath.Join(root, "good")
	write(t, good, ".project.json", "{\n  \"name\": \"g\",\n  \"version\": \"1.0.0\"\n}\n")
	write(t, good, "G.sysml", "package G;\n")
	goodKpar := filepath.Join(root, "g.kpar")
	sh(t, root, bin, "bundle", "-o", goodKpar, good)
	traversal := filepath.Join(root, "traversal.kpar")
	rezip(t, goodKpar, traversal, func(m map[string][]byte) { m["../escape.sysml"] = []byte("package Escape;") })
	tampered := filepath.Join(root, "tampered.kpar")
	rezip(t, goodKpar, tampered, func(m map[string][]byte) { m["G.sysml"] = []byte("package Tampered;") })

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"missing .project.json", []string{gitRes + "@2.0.0"}, "is not a package: no .project.json"},
		{"tag/version mismatch", []string{gitRes + "@1.2.0"}, "carries version 1.2.0 but its .project.json says \"version\": \"1.1.0\""},
		{"checksum mismatch", []string{tampered}, "checksum mismatch for G.sysml"},
		{"path traversal", []string{traversal}, "escapes the archive root"},
		{"pkg:github", []string{"pkg:github/acme/widgets@1.0.0"}, "write pkg:git/<host>/<path>"},
		{"pkg:cvs", []string{"pkg:cvs/host/repo@1.0.0"}, "not supported; supported: git, svn, hg"},
		{"unknown host", []string{"pkg:git/no-such-host.invalid/acme/widgets@1.0.0"}, "git ls-remote"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := filepath.Join(root, "app-"+strings.ReplaceAll(tc.name, " ", "-"))
			os.MkdirAll(app, 0o755)
			sh(t, app, bin, "init", "-name", "app")
			before, _ := os.ReadFile(filepath.Join(app, ".project.json"))
			out, err := shErr(app, nil, bin, append([]string{"add"}, tc.args...)...)
			if err == nil {
				t.Fatalf("add accepted:\n%s", out)
			}
			if !strings.Contains(out, tc.want) {
				t.Fatalf("output lacks %q:\n%s", tc.want, out)
			}
			after, _ := os.ReadFile(filepath.Join(app, ".project.json"))
			if !bytes.Equal(before, after) {
				t.Error(".project.json changed by a failing add")
			}
			if _, err := os.Stat(filepath.Join(app, ".project.lock.json")); err == nil {
				t.Error("lock written by a failing add")
			}
		})
	}

	// No git on PATH: a clear error naming the executable, not a panic.
	app := filepath.Join(root, "app-nogit")
	os.MkdirAll(app, 0o755)
	sh(t, app, bin, "init", "-name", "app")
	out, err := shErr(app, []string{"PATH=" + t.TempDir()}, bin, "add", gitRes+"@1.0.0")
	if err == nil || !strings.Contains(out, `"git" is required`) || strings.Contains(out, "panic") {
		t.Fatalf("missing git: %v\n%s", err, out)
	}
}

// SPDX-License-Identifier: Apache-2.0

package vcs

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Open-MBEE/SysPI/internal/digest"
	"github.com/Open-MBEE/SysPI/internal/version"
)

// requireOrSkip skips when the executable is absent locally; with
// SYSPI_REQUIRE_VCS=1 (CI) absence is a failure.
func requireOrSkip(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		if os.Getenv("SYSPI_REQUIRE_VCS") != "" {
			t.Fatalf("%s is required (SYSPI_REQUIRE_VCS set) but not on PATH", name)
		}
		t.Skipf("%s not on PATH", name)
	}
}

func sh(t *testing.T, dir string, env []string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return string(out)
}

// writeTree writes the package content for one version into dir.
func writeTree(t *testing.T, dir, ver string) {
	t.Helper()
	files := map[string]string{
		".project.json":   "{\n  \"name\": \"widgets\",\n  \"publisher\": \"acme\",\n  \"version\": \"" + ver + "\"\n}\n",
		"Widgets.sysml":   "package Widgets { part def Widget; }\n",
		"sub/Extra.sysml": "package Extra;\n",
	}
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

var gitEnv = []string{
	"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.org", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.org",
	"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
}

// gitFixture creates a bare repository with tags v1.0.0 (annotated) and 1.1.0 (lightweight), plus a non-version tag.
func gitFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	work := filepath.Join(root, "work")
	os.Mkdir(work, 0o755)
	sh(t, work, gitEnv, "git", "init", "-q", "-b", "main")
	writeTree(t, work, "1.0.0")
	sh(t, work, gitEnv, "git", "add", "-A")
	sh(t, work, gitEnv, "git", "commit", "-q", "-m", "v1.0.0")
	sh(t, work, gitEnv, "git", "tag", "-a", "-m", "release", "v1.0.0")
	sh(t, work, gitEnv, "git", "tag", "release-2024")
	writeTree(t, work, "1.1.0")
	sh(t, work, gitEnv, "git", "commit", "-q", "-am", "1.1.0")
	sh(t, work, gitEnv, "git", "tag", "1.1.0")
	bare := filepath.Join(root, "widgets.git")
	sh(t, root, gitEnv, "git", "clone", "-q", "--bare", work, bare)
	return bare
}

func hgFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	repo := filepath.Join(root, "widgets")
	os.Mkdir(repo, 0o755)
	env := []string{"HGUSER=t <t@example.org>", "HGPLAIN=1", "HGRCPATH="}
	sh(t, repo, env, "hg", "init")
	writeTree(t, repo, "1.0.0")
	sh(t, repo, env, "hg", "add", "-q")
	sh(t, repo, env, "hg", "commit", "-q", "-m", "v1.0.0")
	sh(t, repo, env, "hg", "tag", "-q", "v1.0.0")
	writeTree(t, repo, "1.1.0")
	sh(t, repo, env, "hg", "commit", "-q", "-m", "1.1.0")
	sh(t, repo, env, "hg", "tag", "-q", "1.1.0")
	return repo
}

// fileURL is the file:// URL of an absolute path on this machine (file:///C:/x on Windows).
func fileURL(p string) string {
	p = filepath.ToSlash(p)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return "file://" + p
}

func svnFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	sh(t, root, nil, "svnadmin", "create", repo)
	url := fileURL(repo)
	stage := filepath.Join(root, "stage")
	os.MkdirAll(filepath.Join(stage, "trunk"), 0o755)
	os.MkdirAll(filepath.Join(stage, "tags"), 0o755)
	writeTree(t, filepath.Join(stage, "trunk"), "1.0.0")
	env := []string{"SVN_EDITOR=true"}
	sh(t, root, env, "svn", "import", "-q", "-m", "import", stage, url)
	sh(t, root, env, "svn", "copy", "-q", "-m", "tag v1.0.0", url+"/trunk", url+"/tags/v1.0.0")
	wc := filepath.Join(root, "wc")
	sh(t, root, env, "svn", "checkout", "-q", url+"/trunk", wc)
	writeTree(t, wc, "1.1.0")
	sh(t, wc, env, "svn", "commit", "-q", "-m", "1.1.0")
	sh(t, root, env, "svn", "copy", "-q", "-m", "tag 1.1.0", url+"/trunk", url+"/tags/1.1.0")
	sh(t, root, env, "svn", "mkdir", "-q", "-m", "not a version", url+"/tags/experimental")
	return url
}

func TestSourcesAgree(t *testing.T) {
	ctx := context.Background()
	type fixture struct {
		name string
		make func(*testing.T) string
	}
	fixtures := []fixture{{"git", gitFixture}, {"hg", hgFixture}, {"svn", svnFixture}}
	digests := map[string]map[string]string{} // version -> vcs -> digest
	revisions := map[string]string{}
	for _, fx := range fixtures {
		t.Run(fx.name, func(t *testing.T) {
			requireOrSkip(t, fx.name)
			if fx.name == "svn" {
				requireOrSkip(t, "svnadmin")
			}
			src, err := Open(fx.name, fx.make(t))
			if err != nil {
				t.Fatal(err)
			}
			vs, err := src.ListVersions(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(vs) != 2 || vs[0].Tag != "v1.0.0" || vs[1].Tag != "1.1.0" {
				t.Fatalf("ListVersions = %+v", vs)
			}
			for _, v := range vs {
				rev, err := src.Revision(ctx, v)
				if err != nil {
					t.Fatal(err)
				}
				switch fx.name {
				case "git":
					if len(rev) != 40 {
						t.Errorf("git revision %q", rev)
					}
				case "hg":
					if len(rev) != 40 {
						t.Errorf("hg revision %q", rev)
					}
				case "svn":
					if rev != "2" && rev != "4" {
						t.Errorf("svn revision %q", rev)
					}
				}
				revisions[fx.name+" "+v.Tag] = rev
				dst := filepath.Join(t.TempDir(), "out")
				if err := src.Export(ctx, v, dst); err != nil {
					t.Fatal(err)
				}
				for _, meta := range []string{".git", ".hg", ".svn", ".hg_archival.txt", ".hgtags"} {
					if _, err := os.Stat(filepath.Join(dst, meta)); err == nil {
						t.Errorf("export left %s behind", meta)
					}
				}
				d, err := digest.Tree(dst)
				if err != nil {
					t.Fatal(err)
				}
				if digests[v.Version.String()] == nil {
					digests[v.Version.String()] = map[string]string{}
				}
				digests[v.Version.String()][fx.name] = d
			}
		})
	}
	for ver, byVCS := range digests {
		var want string
		for name, d := range byVCS {
			if want == "" {
				want = d
			} else if d != want {
				t.Errorf("version %s: %s digest %s differs from %s", ver, name, d, want)
			}
		}
	}
	if len(digests["1.0.0"]) > 1 && revisions["git v1.0.0"] == revisions["hg v1.0.0"] {
		t.Error("revisions should differ between systems")
	}
}

func TestSelectAndErrors(t *testing.T) {
	vs := versionsFromNames([]string{"v1.0.0", "1.0.0", "1.1.0", "v1.1.0", "2.0.0-rc.1", "junk", "v2"})
	if len(vs) != 3 || vs[0].Tag != "v1.0.0" || vs[1].Tag != "v1.1.0" || vs[2].Tag != "2.0.0-rc.1" {
		t.Fatalf("versionsFromNames = %+v", vs)
	}
	c, _ := version.ParseConstraint("^1.0.0")
	v, err := Select(vs, c)
	if err != nil || v.Tag != "v1.1.0" {
		t.Fatalf("Select = %v %v", v, err)
	}
	c, _ = version.ParseConstraint("^3.0.0")
	if _, err := Select(vs, c); err == nil || !strings.Contains(err.Error(), "available: v1.0.0, v1.1.0, 2.0.0-rc.1") {
		t.Fatalf("Select error = %v", err)
	}
	if _, err := Open("cvs", "x"); err == nil {
		t.Fatal("cvs accepted")
	}
	t.Setenv("PATH", t.TempDir())
	src, _ := Open("hg", "/nowhere")
	_, err = src.ListVersions(context.Background())
	if !errors.Is(err, ErrNoExecutable) || !strings.Contains(err.Error(), `"hg"`) {
		t.Fatalf("missing executable error = %v", err)
	}
}

func TestRedactAndCheckedOutTime(t *testing.T) {
	if got := redact("git clone -- https://user:secret@host/repo.git"); got != "git clone -- https://***@host/repo.git" {
		t.Fatalf("redact = %q", got)
	}
	src, _ := Open("git", "https://user:secret@no-such-host.invalid/repo.git")
	if _, err := src.ListVersions(context.Background()); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("credentials in error: %v", err)
	}
	bare := gitFixture(t)
	work := filepath.Join(filepath.Dir(bare), "work")
	tm, ok := CheckedOutTime(context.Background(), work)
	if !ok || tm.IsZero() || tm.Year() < 2020 {
		t.Fatalf("CheckedOutTime = %v %v", tm, ok)
	}
	if _, ok := CheckedOutTime(context.Background(), t.TempDir()); ok {
		t.Fatal("plain directory has a checkout time")
	}
}

func TestGitRefusals(t *testing.T) {
	ctx := context.Background()
	src, _ := Open("git", filepath.Join(t.TempDir(), "missing.git"))
	if _, err := src.ListVersions(ctx); err == nil {
		t.Fatal("missing repository listed")
	}
	bare := gitFixture(t)
	src, _ = Open("git", bare)
	if err := src.Export(ctx, Version{Tag: "nope"}, filepath.Join(t.TempDir(), "out")); err == nil {
		t.Fatal("unknown tag exported")
	}
}

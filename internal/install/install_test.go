// SPDX-License-Identifier: Apache-2.0

package install

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Open-MBEE/SysPI/internal/digest"
	"github.com/Open-MBEE/SysPI/internal/lock"
	"github.com/Open-MBEE/SysPI/internal/origin"
	"github.com/Open-MBEE/SysPI/internal/project"
)

var gitEnv = []string{
	"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.org", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.org",
	"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), gitEnv...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fixture builds a bare repository "widgets" with v1.0.0 and v1.1.0 (the
// latter with .meta.json and a usage of base), a sub-package "gears" under
// packages/gears at both tags, and a bare "base" repository at v2.0.0.
func fixture(t *testing.T) (widgets, base string) {
	root := t.TempDir()
	w := filepath.Join(root, "w")
	os.Mkdir(w, 0o755)
	git(t, w, "init", "-q", "-b", "main")
	write(t, w, ".project.json", `{"name":"widgets","publisher":"acme","version":"1.0.0"}`+"\n")
	write(t, w, "Widgets.sysml", "package Widgets { part def Widget; }\n")
	write(t, w, "packages/gears/.project.json", `{"name":"gears","publisher":"acme","version":"1.0.0"}`+"\n")
	write(t, w, "packages/gears/Gears.sysml", "package Gears;\n")
	git(t, w, "add", "-A")
	git(t, w, "commit", "-q", "-m", "1.0.0")
	git(t, w, "tag", "v1.0.0")
	write(t, w, ".project.json", `{"name":"widgets","publisher":"acme","version":"1.1.0","usage":[{"resource":"git+file://`+filepath.ToSlash(filepath.Join(root, "base.git"))+`","versionConstraint":"^2.1.0"}]}`+"\n")
	write(t, w, "packages/gears/.project.json", `{"name":"gears","publisher":"acme","version":"1.1.0"}`+"\n")
	m, err := project.GenerateMeta(w, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Write(w); err != nil {
		t.Fatal(err)
	}
	git(t, w, "add", "-A")
	git(t, w, "commit", "-q", "-m", "1.1.0")
	git(t, w, "tag", "v1.1.0")
	// A tag whose .project.json disagrees with the tag.
	write(t, w, ".project.json", `{"name":"widgets","publisher":"acme","version":"1.1.0"}`+"\n")
	os.Remove(filepath.Join(w, ".meta.json"))
	git(t, w, "commit", "-q", "-am", "mismatch")
	git(t, w, "tag", "v1.2.0")
	// Build metadata must agree exactly.
	write(t, w, ".project.json", `{"name":"widgets","publisher":"acme","version":"1.3.0+build.2"}`+"\n")
	git(t, w, "commit", "-q", "-am", "build metadata")
	git(t, w, "tag", "v1.3.0+build.1")
	widgets = filepath.Join(root, "widgets.git")
	git(t, root, "clone", "-q", "--bare", w, widgets)

	b := filepath.Join(root, "b")
	os.Mkdir(b, 0o755)
	git(t, b, "init", "-q", "-b", "main")
	write(t, b, ".project.json", `{"name":"base","version":"2.0.0"}`+"\n")
	write(t, b, "Base.kerml", "package Base;\n")
	git(t, b, "add", "-A")
	git(t, b, "commit", "-q", "-m", "2.0.0")
	git(t, b, "tag", "2.0.0")
	write(t, b, ".project.json", `{"name":"base","version":"2.1.0"}`+"\n")
	git(t, b, "commit", "-q", "-am", "2.1.0")
	git(t, b, "tag", "2.1.0")
	git(t, b, "rm", "-q", ".project.json")
	git(t, b, "commit", "-q", "-m", "no project")
	git(t, b, "tag", "3.0.0")
	base = filepath.Join(root, "base.git")
	git(t, root, "clone", "-q", "--bare", b, base)
	return widgets, base
}

func consumer(t *testing.T, usages ...project.Usage) string {
	dir := t.TempDir()
	p := &project.Project{Name: "app", Publisher: "test", Version: "0.1.0", Usage: usages}
	if err := p.Write(dir); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "App.sysml", "package App;\n")
	return dir
}

// ident is the lock resource (canonical, version-free identity) of a resource.
func ident(t *testing.T, res string) string {
	t.Helper()
	o, err := origin.Parse(res)
	if err != nil {
		t.Fatal(err)
	}
	return o.Identity()
}

func run(t *testing.T, dir string, opts Options) (*Result, string) {
	t.Helper()
	var log bytes.Buffer
	opts.Root = dir
	opts.Log = &log
	res, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v\n%s", err, log.String())
	}
	return res, log.String()
}

func runErr(t *testing.T, dir string, opts Options, want string) {
	t.Helper()
	opts.Root = dir
	_, err := Run(context.Background(), opts)
	if err == nil {
		t.Fatalf("Run succeeded, want error containing %q", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q lacks %q", err, want)
	}
}

func TestGitInstallLockAndOffline(t *testing.T) {
	widgets, _ := fixture(t)
	wres := "git+file://" + filepath.ToSlash(widgets)
	dir := consumer(t,
		project.Usage{Resource: wres + "@1.0.0"},
		project.Usage{Resource: wres + "#packages/gears", VersionConstraint: "~1.1.0"},
		project.Usage{Resource: "https://www.omg.org/spec/KerML/20250201/Kernel-Libraries.kpar"},
	)
	res, log := run(t, dir, Options{})
	if len(res.Installed) != 2 || !res.LockWritten {
		t.Fatalf("result %+v\n%s", res, log)
	}
	for _, want := range []string{"lib/acme-widgets_1.0.0/.project.json", "lib/acme-widgets_1.0.0/.meta.json", "lib/acme-gears_1.1.0/Gears.sysml", "lib/acme-gears_1.1.0/.meta.json"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(want))); err != nil {
			t.Errorf("%s: %v", want, err)
		}
	}
	if !strings.Contains(log, "has no .meta.json; generated one") {
		t.Errorf("log lacks the regenerated-meta notice:\n%s", log)
	}
	lk, _, err := lock.Read(dir, project.LockFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(lk.Packages) != 3 {
		t.Fatalf("lock has %d packages", len(lk.Packages))
	}
	w := lk.Find(ident(t, wres))
	if w == nil || w.Kind != lock.KindVCS || w.VCS != "git" || w.Tag != "v1.0.0" || len(w.Revision) != 40 || w.SHA256 == "" || !w.MetaGenerated || w.Publisher != "acme" {
		t.Fatalf("widgets entry %+v", w)
	}
	g := lk.Find(ident(t, wres+"#packages/gears"))
	if g == nil || g.Version != "1.1.0" || g.Tag != "v1.1.0" || g.Revision == w.Revision {
		t.Fatalf("gears entry %+v", g)
	}
	std := lk.Find("https://www.omg.org/spec/KerML/20250201/Kernel-Libraries.kpar")
	if std == nil || std.Kind != lock.KindStandard || len(res.Skipped) != 1 {
		t.Fatalf("standard entry %+v", std)
	}
	// The digest of the installed tree without the generated meta equals the lock.
	if d, _ := digest.TreeExcept(filepath.Join(dir, "lib", "acme-widgets_1.0.0"), project.MetaFile); d != w.SHA256 {
		t.Fatalf("installed digest %s != lock %s", d, w.SHA256)
	}
	first, _ := os.ReadFile(filepath.Join(dir, project.LockFile))

	// Second run: nothing fetched, lock unchanged.
	res2, _ := run(t, dir, Options{Offline: true})
	if len(res2.Installed) != 0 || len(res2.Reused) != 2 || res2.LockWritten {
		t.Fatalf("second run %+v", res2)
	}

	// Fresh clone of the consumer (project + lock, no lib): byte-identical lock.
	fresh := t.TempDir()
	pj, _ := os.ReadFile(filepath.Join(dir, project.ProjectFile))
	write(t, fresh, project.ProjectFile, string(pj))
	write(t, fresh, project.LockFile, string(first))
	res3, _ := run(t, fresh, Options{})
	if len(res3.Installed) != 2 || res3.LockWritten {
		t.Fatalf("fresh run %+v", res3)
	}
	second, _ := os.ReadFile(filepath.Join(fresh, project.LockFile))
	if !bytes.Equal(first, second) {
		t.Fatalf("lock differs:\n%s\n%s", first, second)
	}
	// Another fresh clone without a lock resolves the same bytes.
	fresh2 := t.TempDir()
	write(t, fresh2, project.ProjectFile, string(pj))
	run(t, fresh2, Options{})
	third, _ := os.ReadFile(filepath.Join(fresh2, project.LockFile))
	if !bytes.Equal(first, third) {
		t.Fatalf("re-resolved lock differs:\n%s\n%s", first, third)
	}

	// A lock whose resources carry an older, non-canonical spelling still
	// matches the installed trees offline.
	legacy := strings.ReplaceAll(string(first), `"`+ident(t, wres)+`"`, `"`+wres+`"`)
	if legacy == string(first) {
		t.Fatal("lock did not contain the canonical widgets identity")
	}
	write(t, dir, project.LockFile, legacy)
	res5, log5 := run(t, dir, Options{Offline: true})
	if len(res5.Reused) != 2 || !res5.LockWritten {
		t.Fatalf("legacy lock run %+v\n%s", res5, log5)
	}
	if again, _ := os.ReadFile(filepath.Join(dir, project.LockFile)); !bytes.Equal(again, first) {
		t.Fatalf("legacy lock not rewritten canonically:\n%s", again)
	}

	// Offline with the repository gone: still satisfied.
	os.RemoveAll(widgets)
	res4, _ := run(t, dir, Options{})
	if len(res4.Reused) != 2 {
		t.Fatalf("offline run %+v", res4)
	}
	// Offline on a clone without lib/: a clear error.
	fresh3 := t.TempDir()
	write(t, fresh3, project.ProjectFile, string(pj))
	write(t, fresh3, project.LockFile, string(first))
	runErr(t, fresh3, Options{Offline: true}, "offline and pkg:generic/")
	// A tampered installed tree is reinstalled from the lock... which needs the repository.
	write(t, dir, "lib/acme-widgets_1.0.0/Widgets.sysml", "tampered")
	runErr(t, dir, Options{}, "usage")
}

func TestCanonicalResources(t *testing.T) {
	l := lock.New()
	l.Put(lock.Entry{Resource: "pkg:git/example.org/team/widgets", Kind: lock.KindVCS})
	l.Put(lock.Entry{Resource: "git+ssh://example.org/team/gears#packages/gears", Kind: lock.KindVCS})
	l.Put(lock.Entry{Resource: "https://example.org/dist/tools_1.0.0.kpar", Kind: lock.KindArchive})
	// An older lock dropped .git from the identity but kept it in source.
	l.Put(lock.Entry{Resource: "pkg:generic/rover?vcs_url=git%2Bhttps:%2F%2Fexample.org%2Fteam%2Frover", Kind: lock.KindVCS, VCS: "git", Source: "https://example.org/team/rover.git"})
	canonicalResources(l)
	want := []string{
		"pkg:generic/widgets?vcs_url=git%2Bhttps:%2F%2Fexample.org%2Fteam%2Fwidgets",
		"pkg:generic/gears?vcs_url=git%2Bssh:%2F%2Fexample.org%2Fteam%2Fgears%23packages%2Fgears",
		"https://example.org/dist/tools_1.0.0.kpar",
		"pkg:generic/rover?vcs_url=git%2Bhttps:%2F%2Fexample.org%2Fteam%2Frover.git",
	}
	for i, w := range want {
		if l.Packages[i].Resource != w {
			t.Errorf("entry %d = %s, want %s", i, l.Packages[i].Resource, w)
		}
	}
}

func TestRefusals(t *testing.T) {
	widgets, base := fixture(t)
	wres := "git+file://" + filepath.ToSlash(widgets)
	bres := "git+file://" + filepath.ToSlash(base)
	cases := []struct {
		name  string
		usage project.Usage
		want  string
	}{
		{"missing project at tag", project.Usage{Resource: bres + "@3.0.0"}, "is not a package: no .project.json"},
		{"tag/version mismatch", project.Usage{Resource: wres + "@1.2.0"}, `carries version 1.2.0 but its .project.json says "version": "1.1.0"`},
		{"no matching version", project.Usage{Resource: wres, VersionConstraint: "^5.0.0"}, "no version satisfies \"^5.0.0\"; available: v1.0.0, v1.1.0, v1.2.0, v1.3.0+build.1"},
		{"build metadata mismatch", project.Usage{Resource: wres, VersionConstraint: "~1.3.0"}, `carries version 1.3.0+build.1 but its .project.json says "version": "1.3.0+build.2"`},
		{"provider type", project.Usage{Resource: "pkg:github/acme/widgets@1.0.0"}, "write pkg:git/github.com/acme/widgets"},
		{"unsupported vcs", project.Usage{Resource: "pkg:cvs/host/repo@1.0.0"}, "pkg:cvs is not supported"},
		{"missing subpath", project.Usage{Resource: wres + "@1.0.0#nope"}, "has no directory nope"},
		{"unknown host", project.Usage{Resource: "pkg:git/nonexistent.invalid/acme/widgets@1.0.0"}, "git ls-remote"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := consumer(t, tc.usage)
			runErr(t, dir, Options{}, tc.want)
			if _, err := os.Stat(filepath.Join(dir, "lib")); err == nil {
				entries, _ := os.ReadDir(filepath.Join(dir, "lib"))
				if len(entries) != 0 {
					t.Errorf("lib/ not empty after refusal: %v", entries)
				}
			}
			if _, err := os.Stat(filepath.Join(dir, project.LockFile)); err == nil {
				t.Error("lock written after refusal")
			}
		})
	}
}

func TestTransitiveAndConflict(t *testing.T) {
	widgets, base := fixture(t)
	wres := "git+file://" + filepath.ToSlash(widgets)
	bres := "git+file://" + filepath.ToSlash(base)
	dir := consumer(t, project.Usage{Resource: wres, VersionConstraint: "~1.1.0"})
	res, _ := run(t, dir, Options{})
	if len(res.Installed) != 2 {
		t.Fatalf("closure not installed: %+v", res)
	}
	lk, _, _ := lock.Read(dir, project.LockFile)
	w := lk.Find(ident(t, wres))
	if w.MetaGenerated || len(w.Usage) != 1 || w.Usage[0].Resource != "git+file://"+filepath.ToSlash(base) || w.Usage[0].VersionConstraint != "^2.1.0" {
		t.Fatalf("widgets entry %+v", w)
	}
	if b := lk.Find(ident(t, bres)); b == nil || b.Version != "2.1.0" || b.Tag != "2.1.0" {
		t.Fatalf("base entry %+v", b)
	}
	// Compatible constraints agree on 2.1.0; an exact 2.0.0 conflicts with widgets' ^2.1.0.
	dir2 := consumer(t, project.Usage{Resource: bres, VersionConstraint: "^2.0.0"}, project.Usage{Resource: wres + "@1.1.0"})
	run(t, dir2, Options{})
	dir3 := consumer(t, project.Usage{Resource: bres + "@2.0.0"}, project.Usage{Resource: wres + "@1.1.0"})
	runErr(t, dir3, Options{}, "already resolved to 2.0.0")
	// A reused (locked, installed) widgets keeps its ^2.1.0 on base: adding base@2.0.0 later still conflicts,
	// and the failed run leaves lib/ and the lock as they were.
	before := treeList(t, filepath.Join(dir, "lib"))
	lockBefore, _ := os.ReadFile(filepath.Join(dir, project.LockFile))
	p, _ := project.ReadProject(dir)
	p.Usage = append([]project.Usage{{Resource: bres + "@2.0.0"}}, p.Usage...)
	p.Write(dir)
	runErr(t, dir, Options{}, "already resolved to 2.0.0")
	if after := treeList(t, filepath.Join(dir, "lib")); strings.Join(after, "\n") != strings.Join(before, "\n") {
		t.Fatalf("lib/ changed by a failing run:\n%v\n%v", before, after)
	}
	if lockAfter, _ := os.ReadFile(filepath.Join(dir, project.LockFile)); !bytes.Equal(lockBefore, lockAfter) {
		t.Fatal("lock changed by a failing run")
	}
	// Two different repositories whose packages share publisher, name and version collide.
	twin := t.TempDir()
	git(t, twin, "init", "-q", "-b", "main")
	write(t, twin, ".project.json", `{"name":"base","version":"2.1.0"}`+"\n")
	write(t, twin, "Other.kerml", "package Other;\n")
	git(t, twin, "add", "-A")
	git(t, twin, "commit", "-q", "-m", "2.1.0")
	git(t, twin, "tag", "2.1.0")
	runErr(t, consumer(t, project.Usage{Resource: bres + "@2.1.0"}, project.Usage{Resource: "git+file://" + filepath.ToSlash(twin) + "@2.1.0"}), Options{}, "both install as lib/base_2.1.0 with different contents")
	// LockOnly writes the lock and installs nothing.
	dir4 := consumer(t, project.Usage{Resource: wres + "@1.1.0"})
	res4, _ := run(t, dir4, Options{LockOnly: true})
	if _, err := os.Stat(filepath.Join(dir4, "lib")); err == nil || !res4.LockWritten || len(res4.Installed) != 0 {
		t.Fatalf("lock-only installed something: %+v", res4)
	}
	l4, _, _ := lock.Read(dir4, project.LockFile)
	if len(l4.Packages) != 2 {
		t.Fatalf("lock-only lock %+v", l4)
	}
}

func TestArchivesAndLocalDir(t *testing.T) {
	pkg := t.TempDir()
	write(t, pkg, ".project.json", `{"name":"tools","publisher":"acme","version":"3.1.0","usage":[{"resource":"https://www.omg.org/spec/SysML/20250201/Systems-Library.kpar"}]}`)
	write(t, pkg, "Tools.sysml", "package Tools;\n")
	kpar := filepath.Join(t.TempDir(), "acme-tools_3.1.0.kpar")
	if _, err := project.Bundle(pkg, kpar, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	sum, _ := digest.File(kpar)
	srv := httptest.NewServer(http.FileServer(http.Dir(filepath.Dir(kpar))))
	defer srv.Close()
	editable := t.TempDir()
	write(t, editable, ".project.json", `{"name":"dev","version":"0.0.1"}`)

	dir := consumer(t,
		project.Usage{Resource: kpar},
		project.Usage{Resource: srv.URL + "/acme-tools_3.1.0.kpar"},
		project.Usage{Resource: editable},
	)
	res, _ := run(t, dir, Options{})
	if len(res.Installed) != 1 || len(res.Reused) != 1 || len(res.Skipped) != 2 {
		t.Fatalf("result %+v", res)
	}
	if d, _ := digest.File(filepath.Join(dir, "lib", "acme-tools_3.1.0.kpar")); d != sum {
		t.Fatal("installed archive differs")
	}
	lk, _, _ := lock.Read(dir, project.LockFile)
	if e := lk.Find(srv.URL + "/acme-tools_3.1.0.kpar"); e == nil || e.Kind != lock.KindArchive || e.SHA256 != sum || e.Usage[0].Resource != "https://www.omg.org/spec/SysML/20250201/Systems-Library.kpar" {
		t.Fatalf("remote entry %+v", e)
	}
	if e := lk.Find(editable); e == nil || e.Kind != lock.KindLocal || e.Name != "dev" {
		t.Fatalf("local entry %+v", e)
	}
	// Offline from the lock with the server and the local archive gone.
	srv.Close()
	os.Rename(kpar, kpar+".moved")
	res2, _ := run(t, dir, Options{Offline: true})
	if len(res2.Reused) != 2 {
		t.Fatalf("offline %+v", res2)
	}
	os.Rename(kpar+".moved", kpar)
	// Two usages of one editable directory with incompatible constraints conflict.
	runErr(t, consumer(t, project.Usage{Resource: editable}, project.Usage{Resource: editable, VersionConstraint: "^1.0.0"}), Options{}, "already resolved to 0.0.1")
}

// tamper copies an archive and flips a byte of a member's content by
// rebuilding it with one file changed but the original .meta.json.
func tamper(t *testing.T, kpar string) string {
	t.Helper()
	a, err := project.OpenArchive(kpar)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	dir := t.TempDir()
	if err := a.Extract(filepath.Join(dir, "x")); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "x/Tools.sysml", "package Evil;\n")
	meta := a.Meta.Marshal()
	// Bundle regenerates meta, so assemble by hand: bundle, then overwrite .meta.json member via Extract+zip is long; instead write a raw zip.
	out := filepath.Join(dir, "bad.kpar")
	writeRawZip(t, out, map[string][]byte{
		".project.json": a.Project.Marshal(),
		".meta.json":    meta,
		"Tools.sysml":   []byte("package Evil;\n"),
	})
	return out
}

// treeList lists every path under dir, sorted.
func treeList(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil {
			rel, _ := filepath.Rel(dir, p)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	return out
}

func TestOfflineNeverReadsHTTPCredentials(t *testing.T) {
	widgets, _ := fixture(t)
	dir := consumer(t, project.Usage{Resource: "git+file://" + widgets + "@1.0.0"})
	run(t, dir, Options{})
	t.Setenv("NETRC", filepath.Join(t.TempDir(), "missing"))
	res, _ := run(t, dir, Options{Offline: true})
	if len(res.Reused) != 1 {
		t.Fatalf("%+v", res)
	}
}

func TestPublishRollsBackWhenTheLockCannotBeWritten(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	widgets, _ := fixture(t)
	dir := consumer(t, project.Usage{Resource: "git+file://" + widgets + "@1.0.0"})
	run(t, dir, Options{})
	before, _ := os.ReadFile(filepath.Join(dir, project.LockFile))
	// Move to 1.1.0 (replaces the installed tree) with the project directory read-only,
	// so the lock cannot be written after lib/ was updated.
	write(t, dir, project.ProjectFile, `{"name":"app","version":"0.1.0","usage":[{"resource":"git+file://`+widgets+`@1.1.0"}]}`)
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	opts := Options{Root: dir}
	_, err := Run(context.Background(), opts)
	os.Chmod(dir, 0o755)
	if err == nil || !strings.Contains(err.Error(), "permission denied") || !strings.Contains(err.Error(), project.LockFile) {
		t.Fatalf("the lock write must be what failed: %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, project.LockFile))
	if string(before) != string(after) {
		t.Fatal("lock changed")
	}
	entries, _ := os.ReadDir(filepath.Join(dir, project.LibDir))
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if strings.Join(names, ",") != "acme-widgets_1.0.0" {
		t.Fatalf("lib/ must match the lock still on disk, got %v", names)
	}
}

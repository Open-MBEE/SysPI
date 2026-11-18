// SPDX-License-Identifier: Apache-2.0

package install

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Open-MBEE/SysPI/internal/lock"
	"github.com/Open-MBEE/SysPI/internal/origin"
	"github.com/Open-MBEE/SysPI/internal/project"
)

// toolsArchive bundles a tools 3.1.0 archive with the given model text.
func toolsArchive(t *testing.T, model string) []byte {
	t.Helper()
	pkg := t.TempDir()
	write(t, pkg, ".project.json", `{"name":"tools","publisher":"acme","version":"3.1.0"}`)
	write(t, pkg, "Tools.sysml", model)
	write(t, pkg, ".meta.json", `{"index":{},"created":"2000-01-01T00:00:00Z","metamodel":"https://www.omg.org/spec/SysML/20250201"}`)
	kpar := filepath.Join(t.TempDir(), "tools.kpar")
	if _, err := project.Bundle(pkg, kpar, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	return readFile(t, kpar)
}

// A rejected candidate's download is rolled back with it: the archive fetched
// for the next candidate is held to the declared checksums by its own bytes.
func TestBacktrackingForgetsRejectedArchiveChecksums(t *testing.T) {
	_, base := fixture(t)
	bres := "git+file://" + filepath.ToSlash(base)
	first, second := toolsArchive(t, "package Tools;\n"), toolsArchive(t, "package Tools { part def Tool; }\n")
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) == 1 {
			w.Write(first)
			return
		}
		w.Write(second)
	}))
	defer srv.Close()
	sum := sha512.Sum512(first)
	id := "pkg:generic/tools?download_url=" + origin.Encode(srv.URL+"/tools.kpar")
	declared := id + "&checksum=sha512:" + hex.EncodeToString(sum[:])

	// widgets 1.1.0 needs base ^2.1.0 and the checksummed archive; 1.0.0 only the archive.
	root := t.TempDir()
	w := filepath.Join(root, "w")
	os.Mkdir(w, 0o755)
	git(t, w, "init", "-q", "-b", "main")
	write(t, w, ".project.json", `{"name":"widgets","publisher":"acme","version":"1.0.0","usage":[{"resource":"`+id+`"}]}`+"\n")
	git(t, w, "add", "-A")
	git(t, w, "commit", "-q", "-m", "1.0.0")
	git(t, w, "tag", "v1.0.0")
	write(t, w, ".project.json", `{"name":"widgets","publisher":"acme","version":"1.1.0","usage":[{"resource":"`+declared+`"},{"resource":"`+bres+`","versionConstraint":"^2.1.0"}]}`+"\n")
	git(t, w, "commit", "-q", "-am", "1.1.0")
	git(t, w, "tag", "v1.1.0")
	wres := "git+file://" + filepath.ToSlash(w)
	gate := t.TempDir()
	write(t, gate, ".project.json", `{"name":"gate","version":"0.0.1","usage":[{"resource":"`+declared+`"}]}`)

	dir := consumer(t, project.Usage{Resource: wres}, project.Usage{Resource: bres + "@2.0.0"}, project.Usage{Resource: gate})
	before := treeList(t, dir)
	runErr(t, dir, Options{}, "checksum mismatch: the resource declares sha512 "+hex.EncodeToString(sum[:]))
	if hits.Load() != 2 {
		t.Fatalf("the archive was requested %d times, want once per candidate", hits.Load())
	}
	if after := treeList(t, dir); strings.Join(after, "\n") != strings.Join(before, "\n") {
		t.Fatalf("the failed run changed the tree:\n%v\n%v", before, after)
	}
}

// A hosted archive resolved under a rejected candidate is staged, counted and
// locked once, for the candidate that wins.
func TestBacktrackingKeepsOneCopyOfAHostedArchive(t *testing.T) {
	widgets, base := fixture(t)
	wres := "git+file://" + filepath.ToSlash(widgets)
	bres := "git+file://" + filepath.ToSlash(base)
	archive := toolsArchive(t, "package Tools;\n")
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Write(archive)
	}))
	defer srv.Close()
	sum := sha512.Sum512(archive)
	id := "pkg:generic/tools?download_url=" + origin.Encode(srv.URL+"/tools.kpar")
	declared := id + "&checksum=sha512:" + hex.EncodeToString(sum[:])
	gate := t.TempDir()
	write(t, gate, ".project.json", `{"name":"gate","version":"0.0.1","usage":[{"resource":"`+declared+`"}]}`)

	dir := consumer(t,
		project.Usage{Resource: wres, VersionConstraint: ">=1.0.0 <1.2.0"},
		project.Usage{Resource: declared},
		project.Usage{Resource: bres + "@2.0.0"},
		project.Usage{Resource: gate},
	)
	res, log := run(t, dir, Options{})
	if got := lockedVersions(t, dir); got != "base 2.0.0,gate 0.0.1,tools 3.1.0,widgets 1.0.0" {
		t.Fatalf("locked: %s", got)
	}
	if got := libNames(t, dir); got != "acme-tools_3.1.0.kpar,acme-widgets_1.0.0,base_2.0.0" {
		t.Fatalf("lib/: %s", got)
	}
	if len(res.Installed) != 3 || len(res.Reused) != 0 || strings.Count(log, "installed tools 3.1.0") != 1 {
		t.Fatalf("result %+v\n%s", res, log)
	}
	// Changes are in resource order, and the local directory's resource is its path.
	changed := strings.Split(describeAll(res.Changes), "; ")
	sort.Strings(changed)
	if got := strings.Join(changed, "; "); got != "base 2.0.0 (new); gate 0.0.1 (new); tools 3.1.0 (new); widgets 1.0.0 (new)" {
		t.Fatalf("changes: %s", got)
	}
	if hits.Load() != 2 {
		t.Fatalf("the archive was requested %d times, want once per candidate", hits.Load())
	}
	if ds, _, err := Verify(dir); err != nil || len(ds) != 0 {
		t.Fatalf("verify after backtracking: %v\n%s", err, problems(ds))
	}
	// The lock now holds the graph: nothing is fetched again, online or offline.
	for _, opts := range []Options{{}, {Offline: true}} {
		if again, _ := run(t, dir, opts); again.LockWritten || len(again.Installed) != 0 || len(again.Changes) != 0 || hits.Load() != 2 {
			t.Fatalf("second run %+v: %+v after %d requests", opts, again, hits.Load())
		}
	}
}

// inconsistentLock locks and installs widgets 1.1.0 (which needs base ^2.1.0)
// beside base 2.0.0, as a badly merged lock would.
func inconsistentLock(t *testing.T, wres, bres string) string {
	t.Helper()
	dir := consumer(t, project.Usage{Resource: bres, VersionConstraint: "^2.0.0"}, project.Usage{Resource: wres, VersionConstraint: ">=1.0.0 <1.2.0"})
	run(t, dir, Options{})
	other := consumer(t, project.Usage{Resource: bres + "@2.0.0"})
	run(t, other, Options{})
	if err := os.Rename(filepath.Join(other, "lib", "base_2.0.0"), filepath.Join(dir, "lib", "base_2.0.0")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(dir, "lib", "base_2.1.0")); err != nil {
		t.Fatal(err)
	}
	l, _, err := lock.Read(dir, project.LockFile)
	if err != nil {
		t.Fatal(err)
	}
	from, _, err := lock.Read(other, project.LockFile)
	if err != nil {
		t.Fatal(err)
	}
	l.Put(*from.Find(ident(t, bres)))
	if _, err := l.Write(dir, project.LockFile); err != nil {
		t.Fatal(err)
	}
	if got := lockedVersions(t, dir); got != "base 2.0.0,widgets 1.1.0" {
		t.Fatalf("locked: %s", got)
	}
	return dir
}

// Offline, a conflict inside the lock is reported from the lock alone: no
// repository is opened to look for another version.
func TestOfflineConflictNeverOpensARepository(t *testing.T) {
	widgets, base := fixture(t)
	dir := inconsistentLock(t, "git+file://"+filepath.ToSlash(widgets), "git+file://"+filepath.ToSlash(base))
	for _, repo := range []string{widgets, base} {
		if err := os.Rename(repo, repo+".gone"); err != nil {
			t.Fatal(err)
		}
	}
	before := treeList(t, dir)
	for _, opts := range []Options{{Offline: true}, {Offline: true, LockOnly: true}} {
		runErr(t, dir, opts, "already resolved to 2.0.0")
	}
	if after := treeList(t, dir); strings.Join(after, "\n") != strings.Join(before, "\n") {
		t.Fatalf("the failed runs changed the tree:\n%v\n%v", before, after)
	}
}

// remove re-resolves through the same solver: an entry backtracking moves is
// reported as moved and its old copy pruned; a rejected candidate is neither.
func TestRemoveBacktracksAndPrunesWhatMoved(t *testing.T) {
	widgets, base := fixture(t)
	wres := "git+file://" + filepath.ToSlash(widgets)
	bres := "git+file://" + filepath.ToSlash(base)
	dir := inconsistentLock(t, wres, bres)
	extra := t.TempDir()
	write(t, extra, ".project.json", `{"name":"extra","version":"0.0.1"}`)
	p, err := project.ReadProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	p.Usage = append(p.Usage, project.Usage{Resource: extra})
	if err := p.Write(dir); err != nil {
		t.Fatal(err)
	}
	res, log := remove(t, dir, Options{}, extra)
	if got := describeAll(res.Changes); got != "widgets 1.1.0 -> 1.0.0" {
		t.Fatalf("changes: %s", got)
	}
	if got := strings.Join(res.Pruned, ","); got != "lib/acme-widgets_1.1.0" {
		t.Fatalf("pruned: %s\n%s", got, log)
	}
	if got := libNames(t, dir); got != "acme-widgets_1.0.0,base_2.0.0" {
		t.Fatalf("lib/: %s", got)
	}
	if len(res.Installed) != 1 || len(res.Reused) != 1 || strings.Contains(log, "installed widgets 1.1.0") {
		t.Fatalf("result %+v\n%s", res, log)
	}
	if ds, _, err := Verify(dir); err != nil || len(ds) != 0 {
		t.Fatalf("verify after remove: %v\n%s", err, problems(ds))
	}
}

// update takes the highest version for what it names, locked or not, and
// reports the conflict instead of settling for a lower one.
func TestUpdateOfAnUnlockedUsageDoesNotBacktrack(t *testing.T) {
	widgets, base := fixture(t)
	wres := "git+file://" + filepath.ToSlash(widgets)
	bres := "git+file://" + filepath.ToSlash(base)
	dir := consumer(t, project.Usage{Resource: bres + "@2.0.0"})
	run(t, dir, Options{})
	rewrite(t, dir, project.Usage{Resource: bres + "@2.0.0"}, project.Usage{Resource: wres, VersionConstraint: ">=1.0.0 <1.2.0"})
	before := treeList(t, dir)
	for _, named := range [][]string{{wres}, nil} {
		if _, err := Outdated(context.Background(), Options{Root: dir}, named); err == nil || !strings.Contains(err.Error(), "already resolved to 2.0.0") {
			t.Fatalf("outdated %v: %v", named, err)
		}
		if _, err := Update(context.Background(), Options{Root: dir}, named); err == nil || !strings.Contains(err.Error(), "already resolved to 2.0.0") {
			t.Fatalf("update %v: %v", named, err)
		}
	}
	if after := treeList(t, dir); strings.Join(after, "\n") != strings.Join(before, "\n") {
		t.Fatalf("the failed runs changed the tree:\n%v\n%v", before, after)
	}
	// install is free to settle: widgets 1.0.0 needs no base at all.
	run(t, dir, Options{})
	if got := lockedVersions(t, dir); got != "base 2.0.0,widgets 1.0.0" {
		t.Fatalf("locked: %s", got)
	}
}

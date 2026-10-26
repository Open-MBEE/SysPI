// SPDX-License-Identifier: Apache-2.0

package lock

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseLegacyStringUsages(t *testing.T) {
	l, err := Parse([]byte(`{"lockVersion":1,"packages":[{"resource":"pkg:git/example.org/app","kind":"vcs","name":"app","version":"1.0.0","usage":["pkg:git/example.org/core@1.2.0",{"resource":"pkg:git/example.org/util","versionConstraint":"^2.0.0"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	u := l.Packages[0].Usage
	if len(u) != 2 || u[0] != (Usage{Resource: "pkg:git/example.org/core@1.2.0"}) || u[1] != (Usage{Resource: "pkg:git/example.org/util", VersionConstraint: "^2.0.0"}) {
		t.Fatalf("%+v", u)
	}
	if _, err := Parse(l.Marshal()); err != nil {
		t.Fatalf("migrated form must round-trip: %v", err)
	}
}

func TestWriteThroughSymlink(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "shared.lock.json")
	os.WriteFile(shared, []byte("{}"), 0o644)
	dir := filepath.Join(root, "a")
	os.Mkdir(dir, 0o755)
	if err := os.Symlink(shared, filepath.Join(dir, "lock.json")); err != nil {
		t.Skip(err)
	}
	l := New()
	l.Packages = append(l.Packages, Entry{Resource: "pkg:git/example.org/core", Kind: KindVCS, Name: "core", Version: "1.0.0"})
	if wrote, err := l.Write(dir, "lock.json"); err != nil || !wrote {
		t.Fatal(wrote, err)
	}
	if fi, err := os.Lstat(filepath.Join(dir, "lock.json")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the symlink must be kept")
	}
	if got, _ := os.ReadFile(shared); string(got) != string(l.Marshal()) {
		t.Fatalf("target not updated:\n%s", got)
	}
	if left, _ := filepath.Glob(filepath.Join(root, ".*")); len(left) != 0 {
		t.Fatalf("temporary files left: %v", left)
	}
}

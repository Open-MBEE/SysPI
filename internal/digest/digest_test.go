// SPDX-License-Identifier: Apache-2.0

package digest

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTree(t *testing.T) {
	a := t.TempDir()
	write(t, a, "b.sysml", "package B;")
	write(t, a, "sub/a.sysml", "package A;")
	write(t, a, ".project.json", "{}")
	write(t, a, ".git/config", "ignored")
	os.MkdirAll(filepath.Join(a, "empty"), 0o755)
	got, err := Tree(a)
	if err != nil {
		t.Fatal(err)
	}
	// Documented algorithm, computed by hand.
	h := sha256.New()
	for _, e := range [][2]string{{".project.json", "{}"}, {"b.sysml", "package B;"}, {"sub/a.sysml", "package A;"}} {
		h.Write([]byte(itoa(len(e[0])) + "\n" + e[0] + "\n" + itoa(len(e[1])) + "\n" + e[1] + "\n"))
	}
	if want := hex.EncodeToString(h.Sum(nil)); got != want {
		t.Fatalf("Tree = %s, want %s", got, want)
	}

	// Same files, different modes and order of creation: same digest.
	b := t.TempDir()
	write(t, b, "sub/a.sysml", "package A;")
	write(t, b, ".project.json", "{}")
	write(t, b, "b.sysml", "package B;")
	os.Chmod(filepath.Join(b, "b.sysml"), 0o755)
	write(t, b, ".hg/requires", "ignored")
	if got2, _ := Tree(b); got2 != got {
		t.Fatalf("mode/order changed digest: %s vs %s", got2, got)
	}
	// Content change: different digest.
	write(t, b, "b.sysml", "package C;")
	if got3, _ := Tree(b); got3 == got {
		t.Fatal("content change kept digest")
	}
	// Symlink refused.
	if err := os.Symlink("b.sysml", filepath.Join(b, "link")); err == nil {
		if _, err := Tree(b); err == nil {
			t.Fatal("symlink accepted")
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

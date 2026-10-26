// SPDX-License-Identifier: Apache-2.0

package lock

import (
	"bytes"
	"strings"
	"testing"
)

func TestDeterministicRoundTrip(t *testing.T) {
	a := New()
	a.Put(Entry{Resource: "pkg:git/h/b", Kind: KindVCS, Name: "b", Version: "1.0.0", VCS: "git", Source: "https://h/b.git", Tag: "v1.0.0", Revision: "abc", SHA256: "01", Usage: []string{"z", "a"}})
	a.Put(Entry{Resource: "pkg:git/h/a", Kind: KindVCS, Name: "a", Version: "2.0.0", VCS: "svn", Source: "https://h/a", Tag: "2.0.0", Revision: "7", SHA256: "02"})
	b := New()
	b.Put(Entry{Resource: "pkg:git/h/a", Kind: KindVCS, Name: "a", Version: "2.0.0", VCS: "svn", Source: "https://h/a", Tag: "2.0.0", Revision: "7", SHA256: "02"})
	b.Put(Entry{Resource: "pkg:git/h/b", Kind: KindVCS, Name: "b", Version: "1.0.0", VCS: "git", Source: "https://h/b.git", Tag: "v1.0.0", Revision: "abc", SHA256: "01", Usage: []string{"a", "z"}})
	if !bytes.Equal(a.Marshal(), b.Marshal()) {
		t.Fatalf("insertion order changed bytes:\n%s\n%s", a.Marshal(), b.Marshal())
	}
	text := string(a.Marshal())
	if !strings.HasPrefix(text, "{\n  \"lockVersion\": 1,\n  \"packages\": [\n    {\n      \"resource\": \"pkg:git/h/a\"") || !strings.HasSuffix(text, "}\n") {
		t.Fatalf("layout:\n%s", text)
	}
	if strings.Contains(text, "\"commit\"") {
		t.Fatal("lock has a commit field")
	}
	l, err := Parse([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(l.Marshal(), a.Marshal()) {
		t.Fatal("round trip changed bytes")
	}
	if l.Find("pkg:git/h/b").Usage[0] != "a" {
		t.Fatal("usage not sorted")
	}
	a.Put(Entry{Resource: "pkg:git/h/a", Kind: KindVCS, Name: "a", Version: "2.1.0"})
	if len(a.Packages) != 2 || a.Find("pkg:git/h/a").Version != "2.1.0" {
		t.Fatal("Put did not replace")
	}
	dir := t.TempDir()
	if wrote, err := a.Write(dir, "l.json"); err != nil || !wrote {
		t.Fatal(err)
	}
	if wrote, _ := a.Write(dir, "l.json"); wrote {
		t.Fatal("rewrote identical lock")
	}
	if _, ok, err := Read(dir, "l.json"); err != nil || !ok {
		t.Fatal(err)
	}
	if _, ok, err := Read(dir, "none.json"); err != nil || ok {
		t.Fatal("missing lock reported present")
	}
	for _, bad := range []string{`{}`, `{"lockVersion":2,"packages":[]}`, `{"lockVersion":1,"packages":[{"resource":"x","kind":"vcs"}]}`, `{"lockVersion":1,"packages":[],"extra":1}`} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("Parse(%s) accepted", bad)
		}
	}
}

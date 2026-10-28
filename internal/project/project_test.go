// SPDX-License-Identifier: Apache-2.0

package project

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fixture(t *testing.T) string {
	dir := t.TempDir()
	writeFile(t, dir, ".project.json", `{"name":"widgets","publisher":"acme","version":"1.0.0","usage":[{"resource":"pkg:git/example.org/acme/base","versionConstraint":"^2.0.0"}]}`)
	writeFile(t, dir, "Widgets.sysml", "package Widgets {\n  package Inner;\n}\nlibrary package 'Odd Name';\n")
	writeFile(t, dir, "sub/Extra.kerml", "package Extra;\n")
	writeFile(t, dir, "README.md", "hi\n")
	writeFile(t, dir, ".git/HEAD", "ref\n")
	writeFile(t, dir, "lib/other_1.0.0/.project.json", "{}")
	return dir
}

func TestProjectAndMeta(t *testing.T) {
	dir := fixture(t)
	p, err := ReadProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.InstallName() != "acme-widgets_1.0.0" {
		t.Fatalf("InstallName = %q", p.InstallName())
	}
	if (&Project{Name: "a b", Version: "1.0.0"}).InstallName() != "a_b_1.0.0" {
		t.Fatal("InstallName without publisher")
	}
	for _, bad := range []string{`{}`, `{"name":"x"}`, `{"name":"x","version":"1"}`, `{"name":"x","version":"1.0.0","usage":[{}]}`, `{"name":"x","version":"1.0.0","usage":[{"resource":"a","versionConstraint":"1.x"}]}`} {
		if _, err := ParseProject([]byte(bad)); err == nil {
			t.Errorf("ParseProject(%s) accepted", bad)
		}
	}
	if _, err := ReadProject(t.TempDir()); err == nil || !strings.Contains(err.Error(), "no .project.json") {
		t.Fatalf("missing project error = %v", err)
	}

	created := time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)
	m, err := GenerateMeta(dir, created)
	if err != nil {
		t.Fatal(err)
	}
	if m.Created != "2024-05-06T07:08:09Z" {
		t.Errorf("created = %q", m.Created)
	}
	if m.Index["Widgets"] != "Widgets.sysml" || m.Index["Odd Name"] != "Widgets.sysml" || m.Index["Extra"] != "sub/Extra.kerml" || len(m.Index) != 3 {
		t.Errorf("index = %v", m.Index)
	}
	for _, rel := range []string{".project.json", "Widgets.sysml", "sub/Extra.kerml", "README.md"} {
		if _, ok := m.Checksum[rel]; !ok {
			t.Errorf("no checksum for %s", rel)
		}
	}
	for _, rel := range []string{".git/HEAD", "lib/other_1.0.0/.project.json", ".meta.json"} {
		if _, ok := m.Checksum[rel]; ok {
			t.Errorf("checksum for excluded %s", rel)
		}
	}
	if un, err := VerifyChecksums(dir, m); err != nil || len(un) != 0 {
		t.Fatal(err, un)
	}
	writeFile(t, dir, "README.md", "changed\n")
	if _, err := VerifyChecksums(dir, m); err == nil || !strings.Contains(err.Error(), "checksum mismatch for README.md") {
		t.Fatalf("mismatch error = %v", err)
	}
	writeFile(t, dir, "README.md", "hi\n")
	writeFile(t, dir, "new.sysml", "package New;")
	if un, err := VerifyChecksums(dir, m); err != nil || len(un) != 1 || un[0] != "new.sysml" {
		t.Fatalf("unlisted member = %v %v", un, err)
	}
	m.Checksum["x"] = Checksums{{Value: "00", Algorithm: "MD5"}}
	writeFile(t, dir, "x", "")
	os.Remove(filepath.Join(dir, "new.sysml"))
	if _, err := VerifyChecksums(dir, m); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("unknown algorithm error = %v", err)
	}
	delete(m.Checksum, "x")
	os.Remove(filepath.Join(dir, "x"))
	m.Checksum["README.md"] = Checksums{{Value: "", Algorithm: "NONE"}}
	if un, err := VerifyChecksums(dir, m); err != nil || len(un) != 1 || un[0] != "README.md" {
		t.Fatalf("NONE entry = %v %v", un, err)
	}
	// The object-shaped checksum some producers write and the spec's list shape both parse.
	var one, list Meta
	if err := json.Unmarshal([]byte(`{"index":{},"created":"x","checksum":{"a":{"value":"v","algorithm":"SHA256"}}}`), &one); err != nil || len(one.Checksum["a"]) != 1 {
		t.Fatalf("object shape: %v", err)
	}
	if err := json.Unmarshal([]byte(`{"index":{},"created":"x","checksum":{"a":[{"value":"v","algorithm":"SHA256"}]}}`), &list); err != nil || len(list.Checksum["a"]) != 1 {
		t.Fatalf("list shape: %v", err)
	}
	if !strings.Contains(string(one.Marshal()), `"a": [`) {
		t.Fatal("lists are not what is written")
	}
	t.Setenv("SOURCE_DATE_EPOCH", "1700000000")
	if CreatedTime().Unix() != 1700000000 {
		t.Error("SOURCE_DATE_EPOCH ignored")
	}
}

func TestBundleDeterministicAndOpen(t *testing.T) {
	dir := fixture(t)
	created := time.Unix(1700000000, 0)
	out1 := filepath.Join(t.TempDir(), "a.kpar")
	out2 := filepath.Join(t.TempDir(), "b.kpar")
	if _, err := Bundle(dir, out1, created); err != nil {
		t.Fatal(err)
	}
	// Touch mtimes and rewrite a file in a different order: bytes must not change.
	time.Sleep(10 * time.Millisecond)
	writeFile(t, dir, "sub/Extra.kerml", "package Extra;\n")
	if _, err := Bundle(dir, out2, created); err != nil {
		t.Fatal(err)
	}
	b1, _ := os.ReadFile(out1)
	b2, _ := os.ReadFile(out2)
	if !bytes.Equal(b1, b2) {
		t.Fatal("bundles differ")
	}
	// Output inside the project: a second build must not swallow the first.
	in1 := filepath.Join(dir, "dist", "w.kpar")
	if _, err := Bundle(dir, in1, created); err != nil {
		t.Fatal(err)
	}
	if _, err := Bundle(dir, in1, created); err != nil {
		t.Fatal(err)
	}
	if bi, _ := os.ReadFile(in1); !bytes.Equal(bi, b1) {
		t.Fatal("bundle written inside the project differs")
	}
	os.RemoveAll(filepath.Join(dir, "dist"))
	a, err := OpenArchive(out1)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if a.Project.Name != "widgets" || len(a.Members) != 3 || a.Members[0] != "README.md" {
		t.Fatalf("archive = %+v", a)
	}
	if _, ok := a.Meta.Checksum["lib/other_1.0.0/.project.json"]; ok {
		t.Fatal("lib/ bundled")
	}
	dst := filepath.Join(t.TempDir(), "x")
	if err := a.Extract(dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "sub", "Extra.kerml")); err != nil {
		t.Fatal(err)
	}
	m, ok, err := ReadMeta(dst)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if _, err := VerifyChecksums(dst, m); err != nil {
		t.Fatal(err)
	}
	if err := a.Extract(dst); err == nil {
		t.Fatal("extract over existing accepted")
	}
}

// rawZip builds an archive from name→content pairs with no verification.
func rawZip(t *testing.T, members map[string]string) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range members {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(content))
	}
	zw.Close()
	p := filepath.Join(t.TempDir(), "raw.kpar")
	os.WriteFile(p, buf.Bytes(), 0o644)
	return p
}

func TestOpenArchiveRefusals(t *testing.T) {
	good := fixture(t)
	out := filepath.Join(t.TempDir(), "good.kpar")
	if _, err := Bundle(good, out, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	a, _ := OpenArchive(out)
	metaJSON := string(a.Meta.Marshal())
	projJSON, _ := os.ReadFile(filepath.Join(good, ".project.json"))
	a.Close()
	read := func(rel string) string {
		b, _ := os.ReadFile(filepath.Join(good, filepath.FromSlash(rel)))
		return string(b)
	}

	cases := map[string]struct {
		members map[string]string
		want    string
	}{
		"no project": {map[string]string{".meta.json": metaJSON, "a.sysml": "x"}, "no .project.json"},
		"no meta":    {map[string]string{".project.json": string(projJSON)}, "no .meta.json"},
		"traversal":  {map[string]string{".project.json": string(projJSON), ".meta.json": metaJSON, "../evil.sysml": "x"}, "escapes the archive root"},
		"absolute":   {map[string]string{".project.json": string(projJSON), ".meta.json": metaJSON, "/etc/passwd": "x"}, "absolute paths"},
		"backslash":  {map[string]string{".project.json": string(projJSON), ".meta.json": metaJSON, `a\b.sysml`: "x"}, "backslashes"},
		"mismatch": {map[string]string{".project.json": string(projJSON), ".meta.json": metaJSON,
			"README.md": "tampered\n", "Widgets.sysml": read("Widgets.sysml"), "sub/Extra.kerml": read("sub/Extra.kerml")}, "checksum mismatch for README.md"},
		"missing member": {map[string]string{".project.json": string(projJSON), ".meta.json": metaJSON}, "not in the archive"},
		"bad project":    {map[string]string{".project.json": `{"name":"x"}`, ".meta.json": metaJSON}, `"version" is required`},
	}
	for name, tc := range cases {
		p := rawZip(t, tc.members)
		a, err := OpenArchive(p)
		if err == nil {
			a.Close()
			t.Errorf("%s: accepted", name)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %q lacks %q", name, err, tc.want)
		}
	}
	// A damaged member without a recorded checksum is still caught by the ZIP CRC.
	damaged := rawZip(t, map[string]string{".project.json": string(projJSON), ".meta.json": metaJSON,
		"README.md": "hi\n", "Widgets.sysml": read("Widgets.sysml"), "sub/Extra.kerml": read("sub/Extra.kerml"), "extra.sysml": strings.Repeat("package Extra;\n", 50)})
	raw, _ := os.ReadFile(damaged)
	idx := bytes.LastIndex(raw, []byte("extra.sysml")) // the central directory entry; the local header is earlier
	loc := bytes.Index(raw, []byte("extra.sysml"))
	if loc < 0 || idx <= loc {
		t.Fatal("fixture layout")
	}
	raw[loc+len("extra.sysml")+10] ^= 0xff // inside the deflate stream
	os.WriteFile(damaged, raw, 0o644)
	if da, err := OpenArchive(damaged); err == nil {
		da.Close()
		t.Fatal("damaged unrecorded member accepted")
	}
	un := rawZip(t, map[string]string{".project.json": string(projJSON), ".meta.json": metaJSON,
		"README.md": "hi\n", "Widgets.sysml": read("Widgets.sysml"), "sub/Extra.kerml": read("sub/Extra.kerml"), "extra.sysml": "x"})
	if ua, err := OpenArchive(un); err != nil || len(ua.Unrecorded) != 1 || ua.Unrecorded[0] != "extra.sysml" {
		t.Fatalf("unlisted member: %v", err)
	} else {
		ua.Close()
	}
	if _, err := OpenArchive(filepath.Join(good, ".project.json")); err == nil || !strings.Contains(err.Error(), "not a readable ZIP") {
		t.Errorf("non-zip error = %v", err)
	}
	if err := os.Symlink("README.md", filepath.Join(good, "link.sysml")); err == nil {
		if _, err := Bundle(good, filepath.Join(t.TempDir(), "s.kpar"), time.Unix(0, 0)); err == nil || !strings.Contains(err.Error(), "symbolic links") {
			t.Errorf("symlink bundle error = %v", err)
		}
	}
}

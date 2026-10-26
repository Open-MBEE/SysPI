// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"testing"
)

// rezip rewrites a ZIP with its members passed through edit.
func rezip(t *testing.T, in, out string, edit func(map[string][]byte)) {
	t.Helper()
	zr, err := zip.OpenReader(in)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	members := map[string][]byte{}
	for _, f := range zr.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(r)
		r.Close()
		members[f.Name] = data
	}
	edit(members)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range members {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(data)
	}
	zw.Close()
	if err := os.WriteFile(out, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

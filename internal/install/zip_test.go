// SPDX-License-Identifier: Apache-2.0

package install

import (
	"archive/zip"
	"bytes"
	"os"
	"testing"
)

func writeRawZip(t *testing.T, out string, members map[string][]byte) {
	t.Helper()
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

// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Open-MBEE/SysPI/internal/project"
)

func TestBundleWithExporterIndex(t *testing.T) {
	bin := syspiBinary(t)
	root := t.TempDir()
	write(t, root, project.ProjectFile, `{"name":"library","version":"1.0.0"}`)
	write(t, root, project.MetaFile, sysmlBundleMeta)
	write(t, root, "models/vehicles.sysml", "part def <v> Vehicle;\n")
	write(t, root, "index.json", `{"Vehicle":"models/vehicles.sysml","v":"models/vehicles.sysml"}`)
	out := filepath.Join(root, "library.kpar")
	sh(t, root, bin, "bundle", "-index", "index.json", "-o", out, ".")
	archive, err := project.OpenArchive(out)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	want := map[string]string{"Vehicle": "models/vehicles.sysml", "v": "models/vehicles.sysml"}
	if !reflect.DeepEqual(archive.Meta.Index, want) || len(archive.Members) != 1 {
		t.Fatalf("bundle index: %v; members: %v", archive.Meta.Index, archive.Members)
	}

	write(t, root, "index.json", `{"Vehicle":"../vehicles.sysml"}`)
	bad := filepath.Join(root, "invalid.kpar")
	output, err := shErr(root, nil, bin, "bundle", "-index", "index.json", "-o", bad, ".")
	if err == nil || !strings.Contains(output, "must name a model file in the archive") {
		t.Fatalf("invalid index: %v\n%s", err, output)
	}
	if _, err := os.Stat(bad); !os.IsNotExist(err) {
		t.Fatalf("invalid index wrote an archive: %v", err)
	}
}

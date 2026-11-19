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

// The generated index includes root declarations only, independent of line layout.
func TestBundleWithTextIndex(t *testing.T) {
	bin := syspiBinary(t)
	root := t.TempDir()
	out := filepath.Join(t.TempDir(), "models.kpar")
	write(t, root, ".project.json", `{"name":"models","version":"1.0.0"}`)
	write(t, root, ".meta.json", sysmlBundleMeta)
	write(t, root, "models/vehicle.sysml", `// part def Fake;
/* package AlsoFake; */
package <p> 'Vehicle Library' { part def Nested; }
abstract part def <v> Vehicle; part <r> rover : Vehicle;
alias <a> Car for Vehicle;
doc notes /* braces { } and package Imaginary; */
attribute caption = "ignored } ; {";
public import Units::*;
`)
	write(t, root, "types.kerml", "class <g> 'Gear\\'s Type'; feature speed;\n")
	sh(t, root, bin, "bundle", "-o", out, ".")
	a, err := project.OpenArchive(out)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	want := map[string]string{
		"p": "models/vehicle.sysml", "Vehicle Library": "models/vehicle.sysml",
		"v": "models/vehicle.sysml", "Vehicle": "models/vehicle.sysml",
		"r": "models/vehicle.sysml", "rover": "models/vehicle.sysml",
		"a": "models/vehicle.sysml", "Car": "models/vehicle.sysml",
		"notes": "models/vehicle.sysml", "caption": "models/vehicle.sysml",
		"g": "types.kerml", "Gear's Type": "types.kerml", "speed": "types.kerml",
	}
	if !reflect.DeepEqual(a.Meta.Index, want) {
		t.Fatalf("index = %v, want %v", a.Meta.Index, want)
	}

	for _, tc := range []struct{ contents, diagnostic string }{
		{"part def Vehicle;\n", `name "Vehicle" is declared in`},
		{"#Extension part def New;\n", "other.sysml: line 1: unsupported top-level declaration"},
		{"connector System::a to System::b;\n", "other.sysml: line 1: cannot distinguish a name from a reference"},
		{"package Incomplete { part def Nested;\n", "other.sysml: line 1: unterminated or unbalanced declaration"},
	} {
		write(t, root, "other.sysml", tc.contents)
		output, err := shErr(root, nil, bin, "bundle", "-o", out, ".")
		if err == nil || !strings.Contains(output, tc.diagnostic) || !strings.Contains(output, "supply -index") {
			t.Fatalf("want %q: %v\n%s", tc.diagnostic, err, output)
		}
	}
	if err := os.Remove(filepath.Join(root, "other.sysml")); err != nil {
		t.Fatal(err)
	}
	write(t, root, "extra.xmi", "<model/>")
	output, err := shErr(root, nil, bin, "bundle", "-o", out, ".")
	if err == nil || !strings.Contains(output, "extra.xmi: automatic indexing requires a textual model; supply -index") {
		t.Fatalf("expected explicit index for XMI: %v\n%s", err, output)
	}
}

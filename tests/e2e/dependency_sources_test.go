// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Open-MBEE/SysPI/internal/project"
)

func TestDependencySourcesVerifyLocalGraph(t *testing.T) {
	bin := syspiBinary(t)
	root := t.TempDir()
	power := t.TempDir()
	drive := t.TempDir()
	write(t, power, project.ProjectFile, `{"name":"power","version":"1.0.0"}`)
	write(t, power, "Power.sysml", "package Power;\n")
	write(t, drive, project.ProjectFile, fmt.Sprintf(`{"name":"drive","version":"1.0.0","usage":[{"resource":%q}]}`, filepath.ToSlash(power)))
	write(t, drive, "Drive.sysml", "package Drive;\n")
	write(t, root, project.ProjectFile, fmt.Sprintf(`{"name":"vehicle","version":"1.0.0","usage":[{"resource":%q}]}`, filepath.ToSlash(drive)))
	write(t, root, "Vehicle.sysml", "part def <v> Vehicle;\n")
	sh(t, root, bin, "install")
	exported := sh(t, root, bin, "dependency-sources")
	for _, name := range []string{"Drive.sysml", "Power.sysml"} {
		if !strings.Contains(exported, name) {
			t.Fatalf("dependency export missing %s: %s", name, exported)
		}
	}
	if strings.Contains(exported, "Vehicle.sysml") {
		t.Fatalf("dependency export contains a project source: %s", exported)
	}
	if err := os.Remove(filepath.Join(power, project.ProjectFile)); err != nil {
		t.Fatal(err)
	}
	if output, err := shErr(root, nil, bin, "dependency-sources"); err == nil {
		t.Fatalf("dependency export accepted an invalid local project: %s", output)
	}
}

func TestExampleKPARs(t *testing.T) {
	root := filepath.Join("..", "..", "examples", "kpar")
	for _, builder := range []string{"syspi", "sysand"} {
		for _, example := range []struct{ name, member, dependency string }{
			{"power", "Power.sysml", ""},
			{"drive", "Drive.sysml", "power"},
			{"vehicle", "Vehicle.sysml", "drive"},
		} {
			t.Run(builder+"/"+example.name, func(t *testing.T) {
				a, err := project.OpenArchive(filepath.Join(root, builder, example.name+"-1.0.0.kpar"))
				if err != nil {
					t.Fatal(err)
				}
				defer a.Close()
				if a.Project.Name != example.name || a.Project.Version != "1.0.0" || len(a.Meta.Checksum[example.member]) == 0 {
					t.Fatalf("wrong project metadata or missing model checksum: %+v", a.Project)
				}
				var want []string
				if example.dependency != "" {
					want = []string{"http://127.0.0.1:8765/" + builder + "/" + example.dependency + "-1.0.0.kpar"}
				}
				var got []string
				for _, usage := range a.Project.Usage {
					got = append(got, usage.Resource)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("usages = %v, want %v", got, want)
				}
				files, err := a.ExtractTextModels(filepath.Join(t.TempDir(), "models"))
				if err != nil || len(files) != 1 || filepath.Base(files[0]) != example.member {
					t.Fatalf("extract %s: %v, %v", example.member, files, err)
				}
				data, err := os.ReadFile(files[0])
				if err != nil {
					t.Fatal(err)
				}
				source, err := os.ReadFile(filepath.Join(root, "models", example.member))
				if err != nil {
					t.Fatal(err)
				}
				if strings.ReplaceAll(string(data), "\r\n", "\n") != strings.ReplaceAll(string(source), "\r\n", "\n") {
					t.Fatalf("archived source differs from %s", example.member)
				}
			})
		}
	}
}

func TestDependencySourceExport(t *testing.T) {
	bin := syspiBinary(t)
	root, local, served := t.TempDir(), t.TempDir(), t.TempDir()
	server := httptest.NewServer(http.FileServer(http.Dir(served)))
	defer server.Close()
	write(t, local, project.ProjectFile, `{"name":"local","version":"1.0.0"}`)
	write(t, local, "Local.sysml", "package Local;\n")
	write(t, local, "nested/Extra.kerml", "package Extra;\n")

	library := t.TempDir()
	write(t, library, project.ProjectFile, `{"name":"tools","version":"1.0.0"}`)
	write(t, library, project.MetaFile, sysmlBundleMeta)
	write(t, library, "Tools.sysml", "package Tools;\n")
	write(t, library, "nested/Support.kerml", "package Support;\n")
	sh(t, library, bin, "bundle", "-o", filepath.Join(served, "tools.kpar"), ".")
	data, err := os.ReadFile(filepath.Join(served, "tools.kpar"))
	if err != nil {
		t.Fatal(err)
	}
	write(t, served, "mirror.kpar", string(data))

	wrapper := t.TempDir()
	write(t, wrapper, project.ProjectFile, fmt.Sprintf(`{"name":"suite","version":"1.0.0","usage":[{"resource":%q}]}`, server.URL+"/tools.kpar"))
	sh(t, wrapper, bin, "bundle", "-o", filepath.Join(served, "suite.kpar"), ".")
	sh(t, root, bin, "init", "-name", "demo")
	sh(t, root, bin, "add", server.URL+"/suite.kpar")
	sh(t, root, bin, "add", server.URL+"/mirror.kpar")
	sh(t, root, bin, "add", local)
	sh(t, root, bin, "verify")

	output := sh(t, root, bin, "dependency-sources")
	var sources []struct{ Path, Text string }
	if err := json.Unmarshal([]byte(output), &sources); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"Local.sysml": "package Local;\n", "Extra.kerml": "package Extra;\n",
		"Tools.sysml": "package Tools;\n", "Support.kerml": "package Support;\n",
	}
	for _, source := range sources {
		name := filepath.Base(source.Path)
		if text, ok := want[name]; !ok || source.Text != text {
			t.Fatalf("unexpected or duplicate source: %+v", source)
		}
		delete(want, name)
	}
	if len(want) != 0 {
		t.Fatalf("missing sources: %v", want)
	}
}

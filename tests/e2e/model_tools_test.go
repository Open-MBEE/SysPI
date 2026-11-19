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

const modelToolFixture = "SYSPI_TEST_MODEL_TOOL"

// Re-exec the test binary as an opt-in stand-in for either model tool.
func TestMain(m *testing.M) {
	if os.Getenv(modelToolFixture) == "1" {
		runModelToolFixture()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runModelToolFixture() {
	args := os.Args[1:]
	switch {
	case len(args) == 4 && args[0] == "-bundle" && args[2] == "-o":
		data, err := os.ReadFile(filepath.Join(args[1], "Vehicle.sysml"))
		if err != nil || string(data) != "part def <v> Vehicle;\n" {
			os.Exit(10)
		}
		if _, err := os.Stat(filepath.Join(args[1], project.MetaFile)); !os.IsNotExist(err) {
			os.Exit(11)
		}
		if err := os.WriteFile(filepath.Join(args[1], project.MetaFile), []byte(sysmlBundleMeta), 0o644); err != nil {
			os.Exit(11)
		}
		if _, err := project.BundleWithIndex(args[1], args[3], project.CreatedTime(), os.Getenv("SYSPI_TEST_INDEX")); err != nil {
			os.Stderr.WriteString(err.Error())
			os.Exit(12)
		}
	case len(args) >= 3 && args[0] == "-warnings-as-errors" && args[1] == "-validate":
		if data, err := os.ReadFile(args[2]); err != nil || string(data) != "part def <v> Vehicle;\n" {
			os.Exit(13)
		}
		if expected := os.Getenv("SYSPI_TEST_KPAR"); expected != "" {
			if len(args) < 4 || filepath.Base(args[3]) != expected {
				os.Exit(13)
			}
		} else if len(args) != 3 {
			os.Exit(13)
		}
	case len(args) == 4 && args[0] == "-validate" && args[1] == "-json":
		file := args[3]
		if os.Getenv("SYSPI_TEST_DIRECT_FAIL") == "1" {
			file = args[2]
		}
		fmt.Printf(`{"status":"unresolved","exit":2,"diagnostics":[{"file":%q,"line":2,"column":9,"code":"unresolved","message":"unresolved reference: Power::Battery"}]}`, file)
		os.Exit(2)
	case len(args) == 5 && args[0] == "check" && args[1] == "--strict" && args[2] == "--lib":
		if !filepath.IsAbs(args[4]) || filepath.Base(args[4]) != "Vehicle.sysml" {
			os.Exit(14)
		}
		if os.Getenv("SYSPI_TEST_TOOLKIT_FAIL") == "1" {
			os.Stderr.WriteString("unresolved reference")
			os.Exit(15)
		}
	case len(args) == 5 && args[0] == "kpar-index" && args[1] == "--root" && args[3] == "--":
		if !filepath.IsAbs(args[2]) || args[4] != "Vehicle.sysml" {
			os.Exit(17)
		}
		fmt.Print(`{"Vehicle":"Vehicle.sysml","v":"Vehicle.sysml"}`)
	case len(args) >= 5 && args[0] == "check-project" && args[1] == "--strict" && args[2] == "--direct-only":
		var expected []string
		switch filepath.Base(args[4]) {
		case "Vehicle.sysml":
			expected = []string{"--project", "Vehicle.sysml", "--direct", "Drive.sysml", "--transitive", "Power.sysml"}
		case "Drive.sysml":
			expected = []string{"--project", "Drive.sysml", "--direct", "Power.sysml"}
		case "Power.sysml":
			expected = []string{"--project", "Power.sysml"}
		}
		if len(args) != 3+len(expected) {
			os.Exit(18)
		}
		for i, want := range expected {
			if filepath.Base(args[i+3]) != want {
				os.Exit(18)
			}
		}
	default:
		os.Exit(16)
	}
}

func TestDirectModelDependencies(t *testing.T) {
	bin := syspiBinary(t)
	tool, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
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
	env := []string{modelToolFixture + "=1", "SYSPI_TEST_KPAR=Drive.sysml"}
	output, err := shErr(root, env, bin, "check-model", "-opensysml", tool, "-direct")
	if err != nil {
		t.Fatalf("errors from omitted dependencies of Drive should be ignored: %v\n%s", err, output)
	}
	output, err = shErr(root, append(env, "SYSPI_TEST_DIRECT_FAIL=1"), bin, "check-model", "-opensysml", tool, "-direct")
	if err == nil || !strings.Contains(output, "unresolved reference: Power::Battery") {
		t.Fatalf("missing direct dependency: %v\n%s", err, output)
	}
	output, err = shErr(root, env, bin, "check-model", "-toolkit", tool, "-direct")
	if err != nil {
		t.Fatalf("toolkit project scopes: %v\n%s", err, output)
	}
	if err := os.Remove(filepath.Join(power, project.ProjectFile)); err != nil {
		t.Fatal(err)
	}
	if output, err := shErr(root, nil, bin, "dependency-sources"); err == nil {
		t.Fatalf("dependency export accepted an invalid local project: %s", output)
	}
}

func TestOptionalModelTools(t *testing.T) {
	bin := syspiBinary(t)
	root := t.TempDir()
	tool, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	write(t, root, project.ProjectFile, `{"name":"vehicle","version":"1.0.0"}`)
	write(t, root, project.MetaFile, sysmlBundleMeta)
	write(t, root, "Vehicle.sysml", "part def <v> Vehicle;\n")
	temp := t.TempDir()
	index := filepath.Join(temp, "index.json")
	write(t, temp, "index.json", `{"Vehicle":"Vehicle.sysml","v":"Vehicle.sysml"}`)
	env := []string{modelToolFixture + "=1", "SYSPI_TEST_INDEX=" + index}

	out := filepath.Join(temp, "vehicle.kpar")
	output, err := shErr(root, env, bin, "bundle", "-index-opensysml", tool, "-o", out, ".")
	if err != nil {
		t.Fatalf("exported index: %v\n%s", err, output)
	}
	a, err := project.OpenArchive(out)
	if err != nil {
		t.Fatal(err)
	}
	a.Close()
	want := map[string]string{"Vehicle": "Vehicle.sysml", "v": "Vehicle.sysml"}
	if !reflect.DeepEqual(a.Meta.Index, want) {
		t.Fatalf("index = %v, want %v", a.Meta.Index, want)
	}
	toolkitOut := filepath.Join(temp, "toolkit.kpar")
	output, err = shErr(root, env, bin, "bundle", "-index-toolkit", tool, "-o", toolkitOut, ".")
	if err != nil {
		t.Fatalf("toolkit index: %v\n%s", err, output)
	}
	toolkitArchive, err := project.OpenArchive(toolkitOut)
	if err != nil {
		t.Fatal(err)
	}
	toolkitArchive.Close()
	if !reflect.DeepEqual(toolkitArchive.Meta.Index, want) {
		t.Fatalf("toolkit index = %v, want %v", toolkitArchive.Meta.Index, want)
	}
	output, err = shErr(root, env, bin, "check-model", "-opensysml", tool, "-toolkit", tool, "-library", ".", "Vehicle.sysml")
	if err != nil {
		t.Fatalf("two validators: %v\n%s", err, output)
	}
	output, err = shErr(root, append(env, "SYSPI_TEST_TOOLKIT_FAIL=1"), bin, "check-model", "-toolkit", tool, "-library", ".", "Vehicle.sysml")
	if err == nil || !strings.Contains(output, "unresolved reference") {
		t.Fatalf("toolkit failure: %v\n%s", err, output)
	}

	dependency := t.TempDir()
	write(t, dependency, project.ProjectFile, `{"name":"drive","version":"1.0.0"}`)
	write(t, dependency, project.MetaFile, sysmlBundleMeta)
	write(t, dependency, "Drive.sysml", "package Drive;\n")
	sh(t, dependency, bin, "bundle", "-o", filepath.Join(temp, "download.kpar"), ".")
	server := httptest.NewServer(http.FileServer(http.Dir(temp)))
	defer server.Close()
	sh(t, root, bin, "add", server.URL+"/download.kpar")
	archiveEnv := append(env, "SYSPI_TEST_KPAR=Drive.sysml")
	output, err = shErr(root, archiveEnv, bin, "check-model", "-opensysml", tool)
	if err != nil {
		t.Fatalf("locked archives: %v\n%s", err, output)
	}

	write(t, root, "Support.kerml", "package Support;\n")
	output, err = shErr(root, env, bin, "bundle", "-index-opensysml", tool, "-o", out, ".")
	if err == nil || !strings.Contains(output, "cannot mix") {
		t.Fatalf("mixed language: %v\n%s", err, output)
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

func TestToolkitRelease(t *testing.T) {
	tool := os.Getenv("SYSPI_TEST_TOOLKIT")
	if tool == "" {
		t.Skip("set SYSPI_TEST_TOOLKIT to the sysml-toolkit 0.13.0 executable")
	}
	bin := syspiBinary(t)
	root := t.TempDir()
	served := t.TempDir()
	server := httptest.NewServer(http.FileServer(http.Dir(served)))
	defer server.Close()

	if got := strings.TrimSpace(sh(t, root, tool, "--version")); got != "sysmlv2 0.13.0" {
		t.Fatalf("expected sysmlv2 0.13.0, got %q", got)
	}
	bundle := func(name, model string, usages ...string) string {
		t.Helper()
		dir := t.TempDir()
		var entries []string
		for _, usage := range usages {
			entries = append(entries, fmt.Sprintf(`{"resource":%q}`, usage))
		}
		manifest := fmt.Sprintf(`{"name":%q,"version":"1.0.0","usage":[%s]}`, name, strings.Join(entries, ","))
		write(t, dir, project.ProjectFile, manifest)
		write(t, dir, project.MetaFile, sysmlBundleMeta)
		write(t, dir, "models/Library.sysml", model)
		filename := name + ".kpar"
		sh(t, dir, bin, "bundle", "-index-toolkit", tool, "-o", filepath.Join(served, filename), ".")
		return server.URL + "/" + filename
	}

	power := bundle("power", "package <p> Power { part def Battery; }\n")
	vehicle := bundle("vehicle", "package Vehicle { public import Power::Battery; part def Rover; }\n", power)
	archive, err := project.OpenArchive(filepath.Join(served, "power.kpar"))
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	want := map[string]string{"Power": "models/Library.sysml", "p": "models/Library.sysml"}
	if !reflect.DeepEqual(archive.Meta.Index, want) {
		t.Fatalf("toolkit index = %v, want %v", archive.Meta.Index, want)
	}

	sh(t, root, bin, "init", "-name", "demo")
	write(t, root, "Demo.sysml", "package Demo { part rover : Vehicle::Rover; }\n")
	sh(t, root, bin, "add", vehicle)
	sh(t, root, bin, "verify")
	sh(t, root, bin, "check-model", "-toolkit", tool, "-direct")
	write(t, root, "Demo.sysml", "package Demo { part battery : Vehicle::Battery; }\n")
	sh(t, root, bin, "check-model", "-toolkit", tool)
	if output, err := shErr(root, nil, bin, "check-model", "-toolkit", tool, "-direct"); err == nil ||
		!strings.Contains(output, "Vehicle::Battery") || !strings.Contains(output, "transitive") {
		t.Fatalf("expected a direct-dependency failure through a re-export: %v\n%s", err, output)
	}
	sh(t, root, bin, "add", power)
	sh(t, root, bin, "check-model", "-toolkit", tool, "-direct")

	exportDir := t.TempDir()
	write(t, exportDir, "Standalone.sysml", "package Standalone { part def Sample; }\n")
	sh(t, exportDir, tool, "convert", "--to", "kpar", "-o", filepath.Join(served, "standalone.kpar"), "Standalone.sysml")
	sh(t, root, bin, "add", server.URL+"/standalone.kpar")
	sh(t, root, bin, "verify")
	write(t, root, "Demo.sysml", "package Demo { part sample : Standalone::Sample; }\n")
	sh(t, root, bin, "check-model", "-toolkit", tool, "-direct")

	wrapperModel := "package Wrapper { part def Assembly; part battery : Vehicle::Battery; }\n"
	wrapper := bundle("wrapper", wrapperModel, vehicle)
	qualified := bundle("qualified", wrapperModel, vehicle, power)
	graphRoot := t.TempDir()
	sh(t, graphRoot, bin, "init", "-name", "assembly-demo")
	write(t, graphRoot, "Demo.sysml", "package Demo { part assembly : Wrapper::Assembly; }\n")
	sh(t, graphRoot, bin, "add", wrapper)
	sh(t, graphRoot, bin, "check-model", "-toolkit", tool)
	if output, err := shErr(graphRoot, nil, bin, "check-model", "-toolkit", tool, "-direct"); err == nil ||
		!strings.Contains(output, "checking declared dependencies of "+wrapper) || !strings.Contains(output, "Vehicle::Battery") {
		t.Fatalf("expected a package-scoped direct-dependency failure: %v\n%s", err, output)
	}
	sh(t, graphRoot, bin, "remove", wrapper)
	sh(t, graphRoot, bin, "add", qualified)
	sh(t, graphRoot, bin, "check-model", "-toolkit", tool, "-direct")

	suiteDir := t.TempDir()
	write(t, suiteDir, project.ProjectFile, fmt.Sprintf(`{"name":"suite","version":"1.0.0","usage":[{"resource":%q}]}`, power))
	sh(t, suiteDir, bin, "bundle", "-o", filepath.Join(served, "suite.kpar"), ".")
	powerArchive, err := os.ReadFile(filepath.Join(served, "power.kpar"))
	if err != nil {
		t.Fatal(err)
	}
	write(t, served, "power-mirror.kpar", string(powerArchive))
	suiteRoot := t.TempDir()
	sh(t, suiteRoot, bin, "init", "-name", "suite-demo")
	write(t, suiteRoot, "Demo.sysml", "package Demo { part battery : Power::Battery; }\n")
	sh(t, suiteRoot, bin, "add", server.URL+"/suite.kpar")
	sh(t, suiteRoot, bin, "check-model", "-toolkit", tool)
	if output, err := shErr(suiteRoot, nil, bin, "check-model", "-toolkit", tool, "-direct"); err == nil ||
		!strings.Contains(output, "Power::Battery") || !strings.Contains(output, "transitive") {
		t.Fatalf("expected a transitive-only reference through the metadata-only suite: %v\n%s", err, output)
	}
	sh(t, suiteRoot, bin, "add", server.URL+"/power-mirror.kpar")
	sh(t, suiteRoot, bin, "check-model", "-toolkit", tool, "-direct")
}

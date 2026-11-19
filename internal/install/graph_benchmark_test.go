// SPDX-License-Identifier: Apache-2.0

package install

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Open-MBEE/SysPI/internal/project"
)

func BenchmarkDependencyGraph(b *testing.B) {
	if _, err := exec.LookPath("git"); err != nil {
		b.Skip("Git is needed for the repository fixtures")
	}
	for _, width := range []int{8, 32} {
		b.Run(fmt.Sprintf("%d-packages", width*4+2), func(b *testing.B) {
			widgets, base := fixture(b)
			wres := "git+file://" + filepath.ToSlash(widgets)
			bres := "git+file://" + filepath.ToSlash(base)
			rootUsages := graphFixture(b, width, bres)
			for _, lockOnly := range []bool{true, false} {
				for _, solvable := range []bool{true, false} {
					name := "install"
					if lockOnly {
						name = "lock"
					}
					constraint := ">=1.0.0 <1.2.0"
					if !solvable {
						name += "-conflict"
						constraint = "1.1.0"
					}
					b.Run(name, func(b *testing.B) {
						usages := append([]project.Usage{{Resource: wres, VersionConstraint: constraint}}, rootUsages...)
						manifest := project.Project{Name: "app", Version: "1.0.0", Usage: usages}
						for b.Loop() {
							b.StopTimer()
							dir := b.TempDir()
							if err := manifest.Write(dir); err != nil {
								b.Fatal(err)
							}
							b.StartTimer()
							result, err := Run(context.Background(), Options{Root: dir, LockOnly: lockOnly})
							if solvable {
								if err != nil || len(result.Lock.Packages) != width*4+2 {
									b.Fatalf("unexpected resolution: %+v, %v", result, err)
								}
							} else if err == nil || !strings.Contains(err.Error(), "wants") {
								b.Fatalf("expected an unsatisfied requirement, got %v", err)
							}
						}
					})
				}
			}
		})
	}
}

// Four layers share dependencies. The last layer exposes the conflicting pin.
func graphFixture(b *testing.B, width int, base string) []project.Usage {
	b.Helper()
	dir := b.TempDir()
	locate := func(layer, i int) string { return filepath.Join(dir, fmt.Sprintf("layer-%d-%d", layer, i)) }
	var roots []project.Usage
	for layer := range 4 {
		for i := range width {
			path := locate(layer, i)
			if err := os.Mkdir(path, 0o755); err != nil {
				b.Fatal(err)
			}
			p := project.Project{Name: filepath.Base(path), Version: "1.0.0"}
			if layer == 3 {
				p.Usage = []project.Usage{{Resource: base + "@2.0.0"}}
			} else {
				p.Usage = []project.Usage{{Resource: locate(layer+1, i)}, {Resource: locate(layer+1, (i+1)%width)}}
			}
			if err := p.Write(path); err != nil {
				b.Fatal(err)
			}
			if layer == 0 {
				roots = append(roots, project.Usage{Resource: path})
			}
		}
	}
	return roots
}

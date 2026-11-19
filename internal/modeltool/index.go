// SPDX-License-Identifier: Apache-2.0

package modeltool

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/Open-MBEE/SysPI/internal/project"
)

// ExportIndex generates an index from a temporary project with no .meta.json.
func ExportIndex(ctx context.Context, executable, dir, scratch string, output io.Writer) (string, error) {
	members, err := project.Members(dir)
	if err != nil {
		return "", err
	}
	models, err := project.TextModelMembers(members)
	if err != nil {
		return "", err
	}
	if len(models) == 0 {
		return "", fmt.Errorf("OpenSysML index export requires .sysml or .kerml files")
	}
	input := filepath.Join(scratch, "model")
	for _, name := range append(models, project.ProjectFile) {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			return "", err
		}
		dst := filepath.Join(input, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return "", err
		}
	}
	archive := filepath.Join(scratch, "model.kpar")
	if err := command(ctx, executable, output, "-bundle", input, "-o", archive); err != nil {
		return "", err
	}
	return project.WriteArchiveIndex(archive, filepath.Join(scratch, "index.json"), models)
}

// ExportToolkitIndex asks sysml-toolkit for the JSON index of the selected files.
func ExportToolkitIndex(ctx context.Context, executable, dir, scratch string, output io.Writer) (string, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	members, err := project.Members(root)
	if err != nil {
		return "", err
	}
	models, err := project.TextModelMembers(members)
	if err != nil {
		return "", err
	}
	if len(models) == 0 {
		return "", fmt.Errorf("sysml-toolkit index export requires .sysml or .kerml files")
	}
	file := filepath.Join(scratch, "index.json")
	out, err := os.Create(file)
	if err != nil {
		return "", err
	}
	args := append([]string{"kpar-index", "--root", root, "--"}, models...)
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Stdout, cmd.Stderr = out, output
	runErr := cmd.Run()
	closeErr := out.Close()
	if runErr != nil {
		return "", fmt.Errorf("model tool %q: %w", executable, runErr)
	}
	return file, closeErr
}

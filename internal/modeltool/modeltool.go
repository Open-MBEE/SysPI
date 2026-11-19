// SPDX-License-Identifier: Apache-2.0

// Package modeltool runs model tools explicitly selected by the caller.
package modeltool

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
)

// ValidateOpenSysML checks the supplied models and installed archives together.
func ValidateOpenSysML(ctx context.Context, executable string, files []string, output io.Writer) error {
	args := append([]string{"-warnings-as-errors", "-validate"}, files...)
	return command(ctx, executable, output, args...)
}

// ValidateToolkit runs an independent check over the explicitly selected files.
func ValidateToolkit(ctx context.Context, executable, library string, files []string, output io.Writer) error {
	if len(files) == 0 {
		return fmt.Errorf("sysml-toolkit requires one or more explicit model files")
	}
	args := []string{"check", "--strict"}
	if library != "" {
		args = append(args, "--lib", library)
	}
	for _, file := range files {
		ext := strings.ToLower(filepath.Ext(file))
		if ext != ".sysml" && ext != ".kerml" {
			return fmt.Errorf("%q: sysml-toolkit expects a textual SysML or KerML file", file)
		}
		abs, err := filepath.Abs(file)
		if err != nil {
			return err
		}
		args = append(args, abs)
	}
	return command(ctx, executable, output, args...)
}

// ValidateToolkitProject uses the caller's exact checked dependency scopes.
func ValidateToolkitProject(ctx context.Context, executable, library string, own, direct, transitive []string, checkDirect bool, output io.Writer) error {
	args := []string{"check-project", "--strict"}
	if checkDirect {
		args = append(args, "--direct-only")
	}
	if library != "" {
		args = append(args, "--lib", library)
	}
	for _, group := range []struct {
		flag  string
		files []string
	}{{"--project", own}, {"--direct", direct}, {"--transitive", transitive}} {
		for _, file := range group.files {
			args = append(args, group.flag, file)
		}
	}
	return command(ctx, executable, output, args...)
}

func command(ctx context.Context, executable string, output io.Writer, args ...string) error {
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("model tool %q: %w", executable, err)
	}
	return nil
}

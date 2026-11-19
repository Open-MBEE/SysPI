// SPDX-License-Identifier: Apache-2.0

package modeltool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"slices"
)

type modelDiagnostic struct {
	File    string
	Line    int
	Column  int
	Code    string
	Message string
}

// ValidateDirect reruns name lookup with just the project's declared direct sources.
// Call it after the full model check has passed.
func ValidateDirect(ctx context.Context, executable string, own, direct []string, output io.Writer) error {
	args := append([]string{"-validate", "-json"}, own...)
	args = append(args, direct...)
	cmd := exec.CommandContext(ctx, executable, args...)
	var report bytes.Buffer
	cmd.Stdout, cmd.Stderr = &report, output
	runErr := cmd.Run()
	var exit *exec.ExitError
	if runErr != nil && (!errors.As(runErr, &exit) || ctx.Err() != nil) {
		return fmt.Errorf("checking direct model dependencies with %q: %w", executable, runErr)
	}
	var result struct {
		Status      string
		Exit        int
		Diagnostics []modelDiagnostic
	}
	if err := json.Unmarshal(report.Bytes(), &result); err != nil {
		return fmt.Errorf("reading OpenSysML direct dependency diagnostics: %w", err)
	}
	if runErr == nil && (result.Status != "holds" || result.Exit != 0) {
		return fmt.Errorf("unexpected OpenSysML direct dependency status %q (exit %d)", result.Status, result.Exit)
	}
	if runErr != nil && (result.Status != "unresolved" || result.Exit != exit.ExitCode() || len(result.Diagnostics) == 0) {
		return fmt.Errorf("OpenSysML could not check direct model dependencies: %w", runErr)
	}
	for _, d := range result.Diagnostics {
		if d.Code != "unresolved" || !slices.Contains(direct, filepath.Clean(d.File)) {
			return fmt.Errorf("checking direct model dependencies: %s:%d:%d: %s", d.File, d.Line, d.Column, d.Message)
		}
	}
	return nil
}

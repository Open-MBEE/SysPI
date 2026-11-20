// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"unicode/utf8"

	"github.com/Open-MBEE/SysPI/internal/install"
)

func cmdDependencySources(args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("dependency-sources", stderr, "[-C dir]")
	root := fs.String("C", ".", "project directory")
	if err := parse(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errUsage
	}
	scratch, err := os.MkdirTemp("", "syspi-dependency-sources-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)
	files, err := install.DependencyModels(*root, scratch)
	if err != nil {
		return err
	}
	type source struct {
		Path string `json:"path"`
		Text string `json:"text"`
	}
	sources := make([]source, 0, len(files))
	for _, file := range files {
		path, err := filepath.Rel(scratch, file)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		if !utf8.Valid(data) {
			return fmt.Errorf("%s: dependency model is not valid UTF-8", path)
		}
		sources = append(sources, source{Path: filepath.ToSlash(path), Text: string(data)})
	}
	return json.NewEncoder(stdout).Encode(sources)
}

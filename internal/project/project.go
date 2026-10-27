// SPDX-License-Identifier: Apache-2.0

// Package project reads, verifies and writes the files of a KerML project
// interchange package: .project.json (the InterchangeProject of KerML 1.1
// clause 10.3, Table 12), .meta.json (Table 13) and the .kpar archive that
// zips both with the model interchange files.
package project

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Open-MBEE/SysPI/internal/version"
)

const (
	// ProjectFile is the InterchangeProject file at the root of a package.
	ProjectFile = ".project.json"
	// MetaFile is the interchange metadata file beside it.
	MetaFile = ".meta.json"
	// LockFile is this tool's lock, beside .project.json of the consuming project.
	LockFile = ".project.lock.json"
	// ArchiveExt is the project interchange archive extension.
	ArchiveExt = ".kpar"
	// LibDir is where packages are installed, beside the consuming .project.json.
	LibDir = "lib"
	// SysandLibDir is sysand's install directory, read but never written.
	SysandLibDir = ".sysand/lib"
)

// Usage is one entry of .project.json's usage list.
type Usage struct {
	Resource          string `json:"resource"`
	VersionConstraint string `json:"versionConstraint,omitempty"`
}

// Project is an InterchangeProject. Publisher is not in Table 12; it is the
// convention sysand and OpenSysML share for the install directory name.
type Project struct {
	Name        string   `json:"name"`
	Publisher   string   `json:"publisher,omitempty"`
	Description string   `json:"description,omitempty"`
	Version     string   `json:"version"`
	License     string   `json:"license,omitempty"`
	Maintainer  []string `json:"maintainer,omitempty"`
	Website     string   `json:"website,omitempty"`
	Topic       []string `json:"topic,omitempty"`
	Usage       []Usage  `json:"usage,omitempty"`
}

// ParseProject decodes and validates a .project.json body.
func ParseProject(data []byte) (*Project, error) {
	var p Project
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("%s: %v", ProjectFile, err)
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

// Validate checks the mandatory properties of Table 12 and that version is a
// semantic version (recommended by the clause, required by this tool so that
// constraints and tags can be compared).
func (p *Project) Validate() error {
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("%s: \"name\" is required", ProjectFile)
	}
	if strings.TrimSpace(p.Version) == "" {
		return fmt.Errorf("%s: \"version\" is required", ProjectFile)
	}
	if _, err := version.Parse(p.Version); err != nil {
		return fmt.Errorf("%s: version %v", ProjectFile, err)
	}
	for i, u := range p.Usage {
		if strings.TrimSpace(u.Resource) == "" {
			return fmt.Errorf("%s: usage[%d] has no \"resource\"", ProjectFile, i)
		}
		if u.VersionConstraint != "" {
			if _, err := version.ParseConstraint(u.VersionConstraint); err != nil {
				return fmt.Errorf("%s: usage %q: %v", ProjectFile, u.Resource, err)
			}
		}
	}
	return nil
}

// ReadProject reads <dir>/.project.json. A missing file is reported as
// ErrNoProject wrapped with the directory.
func ReadProject(dir string) (*Project, error) {
	data, err := os.ReadFile(filepath.Join(dir, ProjectFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w in %s", ErrNoProject, dir)
		}
		return nil, err
	}
	return ParseProject(data)
}

// ErrNoProject reports a directory or archive without .project.json.
var ErrNoProject = errors.New("no " + ProjectFile)

// Marshal renders the project as indented JSON with a trailing newline,
// properties in Table 12 order.
func (p *Project) Marshal() []byte {
	data, _ := json.MarshalIndent(p, "", "  ")
	return append(data, '\n')
}

// Write writes <dir>/.project.json.
func (p *Project) Write(dir string) error {
	return os.WriteFile(filepath.Join(dir, ProjectFile), p.Marshal(), 0o644)
}

// InstallName is the directory (or .kpar stem) a package is installed as:
// <publisher>-<name>_<version>, or <name>_<version> without a publisher.
func (p *Project) InstallName() string {
	stem := safeSegment(p.Name)
	if p.Publisher != "" {
		stem = safeSegment(p.Publisher) + "-" + stem
	}
	return stem + "_" + p.Version
}

// safeSegment keeps a name usable as one path segment.
func safeSegment(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), ".")
}

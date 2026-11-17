// SPDX-License-Identifier: Apache-2.0

package project

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Checksum is one digest recorded in .meta.json.
type Checksum struct {
	Value     string `json:"value"`
	Algorithm string `json:"algorithm"`
}

// Checksums holds the digests recorded for one member. Table 13 describes
// a list, while the normative JSON schema specifies a single object.
type Checksums []Checksum

// MarshalJSON writes generated singleton checksums as objects. Keep other
// lists intact when reserializing existing metadata.
func (c Checksums) MarshalJSON() ([]byte, error) {
	if len(c) == 1 {
		return json.Marshal(c[0])
	}
	return json.Marshal([]Checksum(c))
}

// UnmarshalJSON accepts a list of checksum objects or one object.
func (c *Checksums) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		var one Checksum
		if err := json.Unmarshal(trimmed, &one); err != nil {
			return err
		}
		*c = Checksums{one}
		return nil
	}
	var list []Checksum
	if err := json.Unmarshal(trimmed, &list); err != nil {
		return err
	}
	*c = list
	return nil
}

// Recorded reports whether the list carries a checksum that can be checked:
// an "algorithm": "NONE" entry (a placeholder some producers write) records nothing.
func (c Checksums) Recorded() bool {
	for _, s := range c {
		if !strings.EqualFold(s.Algorithm, "NONE") {
			return true
		}
	}
	return false
}

// Meta is the interchange metadata of Table 13.
type Meta struct {
	Index           map[string]string    `json:"index"`
	Created         string               `json:"created"`
	Metamodel       string               `json:"metamodel,omitempty"`
	IncludesDerived *bool                `json:"includesDerived,omitempty"`
	IncludesImplied *bool                `json:"includesImplied,omitempty"`
	Checksum        map[string]Checksums `json:"checksum,omitempty"`
}

// SHA256 is the algorithm name this tool writes (Table 13, Note 6).
const SHA256 = "SHA256"

const sysmlMetamodelPrefix = "https://www.omg.org/spec/SysML/"
const sysml20Metamodel = sysmlMetamodelPrefix + "20250201"

// sysmlMetamodel is the version-specific form of SysML 2.0 clause 2: the
// prefix and OMG's eight-digit date stamp.
var sysmlMetamodel = regexp.MustCompile(`^` + regexp.QuoteMeta(sysmlMetamodelPrefix) + `[0-9]{8}$`)

// validateBundleMetamodel refuses a bundle with .sysml members, or one that
// declares a SysML metamodel, unless the declaration is a versioned SysML URI.
func validateBundleMetamodel(metamodel string, members []string) error {
	isSysML := strings.HasPrefix(metamodel, sysmlMetamodelPrefix)
	for _, member := range members {
		if strings.EqualFold(filepath.Ext(member), ".sysml") {
			isSysML = true
			break
		}
	}
	if isSysML && !sysmlMetamodel.MatchString(metamodel) {
		return fmt.Errorf("%s: a SysML bundle requires \"metamodel\" to be the versioned SysML URI (%q for SysML 2.0), got %q", MetaFile, sysml20Metamodel, metamodel)
	}
	return nil
}

// KeepDeclared copies what an author declares and no file content implies:
// metamodel, includesDerived and includesImplied.
func (m *Meta) KeepDeclared(from *Meta) {
	m.Metamodel, m.IncludesDerived, m.IncludesImplied = from.Metamodel, from.IncludesDerived, from.IncludesImplied
}

// ParseMeta decodes a .meta.json body.
func ParseMeta(data []byte) (*Meta, error) {
	var m Meta
	if err := json.NewDecoder(bytes.NewReader(data)).Decode(&m); err != nil {
		return nil, fmt.Errorf("%s: %v", MetaFile, err)
	}
	if m.Index == nil {
		return nil, fmt.Errorf("%s: \"index\" is required", MetaFile)
	}
	if m.Created == "" {
		return nil, fmt.Errorf("%s: \"created\" is required", MetaFile)
	}
	return &m, nil
}

// ReadMeta reads <dir>/.meta.json; ok is false when the file is absent.
func ReadMeta(dir string) (m *Meta, ok bool, err error) {
	data, err := os.ReadFile(filepath.Join(dir, MetaFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	m, err = ParseMeta(data)
	return m, err == nil, err
}

// Marshal renders the metadata as indented JSON with sorted object keys and
// a trailing newline.
func (m *Meta) Marshal() []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	enc.Encode(m)
	return buf.Bytes()
}

// Write writes <dir>/.meta.json.
func (m *Meta) Write(dir string) error {
	return os.WriteFile(filepath.Join(dir, MetaFile), m.Marshal(), 0o644)
}

// CreatedTime is the timestamp a generated .meta.json records: SOURCE_DATE_EPOCH
// when set (reproducible builds), otherwise now, in UTC to the second.
func CreatedTime() time.Time {
	if s := os.Getenv("SOURCE_DATE_EPOCH"); s != "" {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return time.Unix(n, 0).UTC()
		}
	}
	return time.Now().UTC().Truncate(time.Second)
}

// modelExts are the model interchange file extensions of KerML 10.2 and SysML.
var modelExts = map[string]bool{".kerml": true, ".sysml": true, ".json": true, ".xmi": true}

// IsModelFile reports whether a path names a model interchange file.
func IsModelFile(rel string) bool {
	base := filepath.Base(rel)
	if base == ProjectFile || base == MetaFile {
		return false
	}
	return modelExts[strings.ToLower(filepath.Ext(base))]
}

// Members lists the files of a project directory relative to it with "/"
// separators, sorted, excluding version-control metadata, the lock file and
// the given skip paths.
func Members(dir string, skip ...string) ([]string, error) {
	skipped := map[string]bool{}
	for _, s := range skip {
		skipped[s] = true
	}
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if p != dir && (d.Name() == ".git" || d.Name() == ".hg" || d.Name() == ".svn" || rel == LibDir) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%s: symbolic links are not allowed in a package", rel)
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s: not a regular file", rel)
		}
		if rel == LockFile || rel == MetaFile || skipped[rel] {
			return nil
		}
		out = append(out, rel)
		return nil
	})
	sort.Strings(out)
	return out, err
}

// GenerateMeta builds the metadata for a project directory: an index of the
// top-level package declarations of every textual model file and a SHA-256
// checksum of every member except .meta.json itself.
func GenerateMeta(dir string, created time.Time, skip ...string) (*Meta, error) {
	members, err := Members(dir, skip...)
	if err != nil {
		return nil, err
	}
	return generateMeta(dir, created, members, nil)
}

func generateMeta(dir string, created time.Time, members []string, index map[string]string) (*Meta, error) {
	m := &Meta{Index: index, Created: created.UTC().Format(time.RFC3339), Checksum: map[string]Checksums{}}
	if m.Index == nil {
		m.Index = map[string]string{}
	}
	for _, rel := range members {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(data)
		m.Checksum[rel] = Checksums{{Value: hex.EncodeToString(sum[:]), Algorithm: SHA256}}
		if index == nil && IsModelFile(rel) {
			for _, name := range topLevelPackages(rel, data) {
				if prev, dup := m.Index[name]; !dup || rel < prev {
					m.Index[name] = rel
				}
			}
		}
	}
	return m, nil
}

var packageDecl = regexp.MustCompile(`(?m)^\s*(?:(?:standard|library|private|public|abstract)\s+)*package\s+(?:'([^']+)'|([\p{L}_][\p{L}\p{N}_]*))`)

// topLevelPackages scans a textual model file for package declarations at
// brace depth zero. JSON and XMI files are not indexed.
func topLevelPackages(rel string, data []byte) []string {
	ext := strings.ToLower(filepath.Ext(rel))
	if ext != ".sysml" && ext != ".kerml" {
		return nil
	}
	var names []string
	depth := 0
	src := string(data)
	for _, line := range strings.Split(src, "\n") {
		if depth == 0 {
			if m := packageDecl.FindStringSubmatch(line); m != nil {
				if m[1] != "" {
					names = append(names, m[1])
				} else {
					names = append(names, m[2])
				}
			}
		}
		depth += strings.Count(line, "{") - strings.Count(line, "}")
		if depth < 0 {
			depth = 0
		}
	}
	return names
}

// VerifyChecksums checks every checksum .meta.json records against the files
// in dir; Table 13 lets the metadata cover "some or all" of the files, so a
// member without one is not an error. It returns the first mismatch and the
// members that have no checksum that could be checked.
func VerifyChecksums(dir string, m *Meta) (unrecorded []string, err error) {
	members, err := Members(dir)
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, rel := range members {
		have[rel] = true
		sums, ok := m.Checksum[rel]
		if !ok || !sums.Recorded() {
			unrecorded = append(unrecorded, rel)
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return nil, err
		}
		if err := checkSums(rel, data, sums); err != nil {
			return nil, err
		}
	}
	for _, rel := range sortedKeys(m.Checksum) {
		if !have[rel] {
			return nil, fmt.Errorf("%s: %s records a checksum for %s, which is missing", dir, MetaFile, rel)
		}
	}
	return unrecorded, nil
}

func sortedKeys(m map[string]Checksums) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// checkSums verifies the recorded checksums this tool understands; an entry
// with an algorithm it does not implement is an error, not a pass.
func checkSums(rel string, data []byte, sums Checksums) error {
	for _, c := range sums {
		switch strings.ToUpper(strings.ReplaceAll(c.Algorithm, "-", "")) {
		case "NONE":
		case "SHA256":
			sum := sha256.Sum256(data)
			if !strings.EqualFold(hex.EncodeToString(sum[:]), c.Value) {
				return fmt.Errorf("checksum mismatch for %s: %s records SHA256 %s, content has %s", rel, MetaFile, c.Value, hex.EncodeToString(sum[:]))
			}
		default:
			return fmt.Errorf("%s: checksum algorithm %q for %s is not supported (SHA256 is)", MetaFile, c.Algorithm, rel)
		}
	}
	return nil
}

// MismatchedChecksums lists every member whose recorded checksum does not
// match the file in dir, and every recorded member that is missing — all of
// them, where VerifyChecksums stops at the first.
func MismatchedChecksums(dir string, m *Meta) ([]string, error) {
	members, err := Members(dir)
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	var bad []string
	for _, rel := range members {
		have[rel] = true
		sums, ok := m.Checksum[rel]
		if !ok || !sums.Recorded() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return nil, err
		}
		if err := checkSums(rel, data, sums); err != nil {
			bad = append(bad, err.Error())
		}
	}
	for _, rel := range sortedKeys(m.Checksum) {
		if !have[rel] {
			bad = append(bad, MetaFile+" records a checksum for "+rel+", which is missing")
		}
	}
	return bad, nil
}

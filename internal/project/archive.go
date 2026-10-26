// SPDX-License-Identifier: Apache-2.0

package project

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	// MaxArchiveSize bounds the total uncompressed size of a .kpar.
	MaxArchiveSize int64 = 512 << 20
	// MaxArchiveMembers bounds the number of members of a .kpar.
	MaxArchiveMembers = 100000
	// maxMetaSize bounds the two JSON files, which are read before anything else.
	maxMetaSize int64 = 16 << 20
)

// zipEpoch is the fixed member timestamp of a bundle (ZIP cannot store earlier).
var zipEpoch = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)

// Archive is an opened and verified .kpar.
type Archive struct {
	Path    string
	Project *Project
	Meta    *Meta
	// SHA256 is the hex digest of the archive file itself.
	SHA256 string
	// Members are the model-file member paths, sorted.
	Members []string
	// Unrecorded are the members .meta.json records no checksum for.
	Unrecorded []string
	rc         *zip.ReadCloser
	byName     map[string]*zip.File
}

// Close releases the archive.
func (a *Archive) Close() error { return a.rc.Close() }

// IsArchiveFile reports whether the path has the .kpar extension.
func IsArchiveFile(p string) bool { return strings.EqualFold(filepath.Ext(p), ArchiveExt) }

// OpenArchive opens a .kpar and verifies it before any model content is
// read: member paths are confined to the archive root, the size bounds hold,
// exactly one top-level .project.json and .meta.json exist, and every
// recorded checksum matches. Nothing is extracted here.
func OpenArchive(p string) (*Archive, error) {
	st, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if st.Size() > MaxArchiveSize {
		return nil, fmt.Errorf("%s: archive is %d bytes, above the %d byte limit", p, st.Size(), MaxArchiveSize)
	}
	rc, err := zip.OpenReader(p)
	if err != nil {
		return nil, fmt.Errorf("%s: not a readable ZIP archive: %v", p, err)
	}
	a := &Archive{Path: p, rc: rc, byName: map[string]*zip.File{}}
	if len(rc.File) > MaxArchiveMembers {
		rc.Close()
		return nil, fmt.Errorf("%s: %d members, above the %d member limit", p, len(rc.File), MaxArchiveMembers)
	}
	var total int64
	for _, f := range rc.File {
		name, err := normalizeMemberPath(f.Name)
		if err != nil {
			rc.Close()
			return nil, fmt.Errorf("%s: %v", p, err)
		}
		if name == "" || strings.HasSuffix(f.Name, "/") {
			continue // directory entry
		}
		if f.Mode()&fs.ModeSymlink != 0 {
			rc.Close()
			return nil, fmt.Errorf("%s: member %s is a symbolic link, which is not allowed", p, name)
		}
		if !f.Mode().IsRegular() {
			rc.Close()
			return nil, fmt.Errorf("%s: member %s is not a regular file", p, name)
		}
		if _, dup := a.byName[name]; dup {
			rc.Close()
			return nil, fmt.Errorf("%s: member %s appears twice", p, name)
		}
		total += int64(f.UncompressedSize64)
		if total > MaxArchiveSize {
			rc.Close()
			return nil, fmt.Errorf("%s: members unpack to more than %d bytes", p, MaxArchiveSize)
		}
		a.byName[name] = f
	}
	if _, err := a.readMetaFiles(); err != nil {
		rc.Close()
		return nil, err
	}
	for name := range a.byName {
		if name != ProjectFile && name != MetaFile {
			a.Members = append(a.Members, name)
		}
	}
	sort.Strings(a.Members)
	if err := a.verify(); err != nil {
		rc.Close()
		return nil, err
	}
	if a.SHA256, err = fileSHA256(p); err != nil {
		rc.Close()
		return nil, err
	}
	return a, nil
}

func (a *Archive) readMetaFiles() (bool, error) {
	pf, ok := a.byName[ProjectFile]
	if !ok {
		return false, fmt.Errorf("%s: %w at the archive root", a.Path, ErrNoProject)
	}
	data, err := a.readMember(pf, maxMetaSize)
	if err != nil {
		return false, err
	}
	if a.Project, err = ParseProject(data); err != nil {
		return false, fmt.Errorf("%s: %v", a.Path, err)
	}
	mf, ok := a.byName[MetaFile]
	if !ok {
		return false, fmt.Errorf("%s: no %s at the archive root (KerML 10.3 requires exactly one)", a.Path, MetaFile)
	}
	if data, err = a.readMember(mf, maxMetaSize); err != nil {
		return false, err
	}
	if a.Meta, err = ParseMeta(data); err != nil {
		return false, fmt.Errorf("%s: %v", a.Path, err)
	}
	return true, nil
}

// verify checks every recorded checksum; members the metadata does not
// cover are listed in Unrecorded (Table 13 allows "some or all").
func (a *Archive) verify() error {
	names := make([]string, 0, len(a.byName))
	for name := range a.byName {
		if name != MetaFile {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		f := a.byName[name]
		sums, ok := a.Meta.Checksum[name]
		if !ok || !sums.Recorded() {
			a.Unrecorded = append(a.Unrecorded, name)
			continue
		}
		data, err := a.readMember(f, MaxArchiveSize)
		if err != nil {
			return err
		}
		if err := checkSums(name, data, sums); err != nil {
			return fmt.Errorf("%s: %v", a.Path, err)
		}
	}
	for name := range a.Meta.Checksum {
		if _, ok := a.byName[name]; !ok {
			return fmt.Errorf("%s: %s records a checksum for %s, which is not in the archive", a.Path, MetaFile, name)
		}
	}
	return nil
}

func (a *Archive) readMember(f *zip.File, limit int64) ([]byte, error) {
	r, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("%s: %s: %v", a.Path, f.Name, err)
	}
	defer r.Close()
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("%s: %s: %v", a.Path, f.Name, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s: %s is larger than its %d byte limit", a.Path, f.Name, limit)
	}
	return data, nil
}

// Extract writes the archive's members into dst, which must not exist.
func (a *Archive) Extract(dst string) error {
	if _, err := os.Lstat(dst); err == nil {
		return fmt.Errorf("%s already exists", dst)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for name, f := range a.byName {
		target := filepath.Join(dst, filepath.FromSlash(name))
		if err := confinedTo(dst, target); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		data, err := a.readMember(f, MaxArchiveSize)
		if err != nil {
			return err
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// normalizeMemberPath validates a ZIP member name: relative, "/"-separated,
// no "." or ".." segments, no backslashes or NULs. It returns "" for a
// directory entry.
func normalizeMemberPath(name string) (string, error) {
	if strings.ContainsAny(name, "\\\x00") {
		return "", fmt.Errorf("member %q: backslashes and NUL are not allowed in a member path", name)
	}
	if strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("member %q: absolute paths are not allowed", name)
	}
	trimmed := strings.TrimSuffix(name, "/")
	if trimmed == "" {
		return "", nil
	}
	for _, seg := range strings.Split(trimmed, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", fmt.Errorf("member %q: path escapes the archive root", name)
		}
		if len(seg) >= 2 && seg[1] == ':' {
			return "", fmt.Errorf("member %q: drive letters are not allowed", name)
		}
	}
	if strings.HasSuffix(name, "/") {
		return "", nil
	}
	return path.Clean(trimmed), nil
}

// confinedTo reports an error unless target is within root (no symlink in
// an existing prefix may lead outside).
func confinedTo(root, target string) error {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	absRoot, err = filepath.EvalSymlinks(absRoot)
	if err != nil {
		return err
	}
	// Resolve the deepest existing prefix of target.
	probe := target
	for {
		if _, err := os.Lstat(probe); err == nil {
			break
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			break
		}
		probe = parent
	}
	resolved, err := filepath.EvalSymlinks(probe)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(absRoot, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s escapes %s", target, root)
	}
	return nil
}

// Bundle writes the project directory as a deterministic .kpar: members
// sorted, fixed 1980-01-01 timestamps, .meta.json regenerated with the
// given creation time (CreatedTime honours SOURCE_DATE_EPOCH). The same
// tree and time give byte-identical output.
func Bundle(dir, out string, created time.Time) (*Meta, error) {
	if _, err := ReadProject(dir); err != nil {
		return nil, err
	}
	meta, err := GenerateMeta(dir, created)
	if err != nil {
		return nil, err
	}
	members, err := Members(dir)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	names := append([]string{MetaFile}, members...)
	sort.Strings(names)
	for _, name := range names {
		var data []byte
		if name == MetaFile {
			data = meta.Marshal()
		} else if data, err = os.ReadFile(filepath.Join(dir, filepath.FromSlash(name))); err != nil {
			return nil, err
		}
		hdr := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: zipEpoch}
		hdr.SetMode(0o644)
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(data); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return nil, err
	}
	return meta, os.WriteFile(out, buf.Bytes(), 0o644)
}

func fileSHA256(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

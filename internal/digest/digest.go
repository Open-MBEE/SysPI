// SPDX-License-Identifier: Apache-2.0

// Package digest computes the one deterministic SHA-256 a lock records for a
// package tree, so the same package served from git, svn or hg hashes alike.
//
// The algorithm: walk the tree, skipping version-control metadata directories
// (.git, .hg, .svn); take every regular file's path relative to the root with
// "/" separators; sort the paths bytewise; for each, feed the digest the
// decimal length of the path, "\n", the path, "\n", the decimal length of the
// content, "\n", the content bytes, "\n". File modes, timestamps and empty
// directories do not contribute. Symbolic links are refused.
package digest

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
)

// metadataDirs are version-control directories excluded from the digest.
var metadataDirs = map[string]bool{".git": true, ".hg": true, ".svn": true}

// Tree returns the hex SHA-256 of the directory as described in the package
// documentation.
func Tree(root string) (string, error) { return TreeExcept(root) }

// TreeExcept is Tree with the named top-level files left out, for a tree
// whose .meta.json was added after it was hashed.
func TreeExcept(root string, skipTopLevel ...string) (string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != root && metadataDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		for _, skip := range skipTopLevel {
			if rel == skip {
				return nil
			}
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%s: symbolic links are not allowed in a package tree", rel)
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s: not a regular file", rel)
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, rel := range paths {
		f, err := os.Open(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return "", err
		}
		st, err := f.Stat()
		if err != nil {
			f.Close()
			return "", err
		}
		io.WriteString(h, strconv.Itoa(len(rel)))
		io.WriteString(h, "\n")
		io.WriteString(h, rel)
		io.WriteString(h, "\n")
		io.WriteString(h, strconv.FormatInt(st.Size(), 10))
		io.WriteString(h, "\n")
		n, err := io.Copy(h, f)
		f.Close()
		if err != nil {
			return "", err
		}
		if n != st.Size() {
			return "", fmt.Errorf("%s: changed while being hashed", rel)
		}
		io.WriteString(h, "\n")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// File returns the hex SHA-256 of one file's bytes.
func File(path string) (string, error) {
	f, err := os.Open(path)
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

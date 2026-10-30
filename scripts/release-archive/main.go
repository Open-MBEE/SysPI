// SPDX-License-Identifier: Apache-2.0

// Command release-archive writes a reproducible .tar.gz or .zip of the given files:
// sorted entries, numeric owner 0, one mtime, no tool-specific headers.
//
//	go run ./scripts/release-archive -out syspi-linux-amd64.tar.gz -mtime 1700000000 syspi LICENSE
package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func main() {
	out := flag.String("out", "", "archive to write (.tar.gz or .zip)")
	mtime := flag.Int64("mtime", 0, "Unix time stamped on every entry")
	flag.Parse()
	if *out == "" || flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: release-archive -out <archive> -mtime <unix time> <file>...")
		os.Exit(2)
	}
	files := append([]string(nil), flag.Args()...)
	sort.Strings(files)
	if err := write(*out, time.Unix(*mtime, 0).UTC(), files); err != nil {
		fmt.Fprintln(os.Stderr, "release-archive:", err)
		os.Exit(1)
	}
}

func write(out string, mtime time.Time, files []string) error {
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	switch {
	case strings.HasSuffix(out, ".tar.gz"):
		err = writeTar(f, mtime, files)
	case strings.HasSuffix(out, ".zip"):
		err = writeZip(f, mtime, files)
	default:
		err = fmt.Errorf("%s: want a .tar.gz or .zip name", out)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// mode is 0755 for executables (anything not named LICENSE or ending in .exe keeps its
// executable bit from disk), 0644 otherwise; the host's umask never leaks into the archive.
func mode(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	if info.Mode()&0o111 != 0 || strings.HasSuffix(path, ".exe") {
		return 0o755, nil
	}
	return 0o644, nil
}

func writeTar(w io.Writer, mtime time.Time, files []string) error {
	gz, err := gzip.NewWriterLevel(w, gzip.BestCompression)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(gz)
	for _, path := range files {
		m, err := mode(path)
		if err != nil {
			return err
		}
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		hdr := &tar.Header{
			Typeflag: tar.TypeReg,
			Name:     filepath.Base(path),
			Size:     info.Size(),
			Mode:     m,
			ModTime:  mtime,
			Format:   tar.FormatUSTAR,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if err := copyFile(tw, path); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func writeZip(w io.Writer, mtime time.Time, files []string) error {
	zw := zip.NewWriter(w)
	for _, path := range files {
		m, err := mode(path)
		if err != nil {
			return err
		}
		hdr := &zip.FileHeader{Name: filepath.Base(path), Method: zip.Deflate, Modified: mtime}
		hdr.SetMode(os.FileMode(m))
		ew, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		if err := copyFile(ew, path); err != nil {
			return err
		}
	}
	return zw.Close()
}

func copyFile(w io.Writer, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(w, f)
	return err
}

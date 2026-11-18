// SPDX-License-Identifier: Apache-2.0

package project

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/Open-MBEE/SysPI/internal/digest"
	"github.com/Open-MBEE/SysPI/internal/netrc"
	"github.com/Open-MBEE/SysPI/internal/origin"
)

// PublishArchive verifies and uploads a KPAR to a static HTTPS artifact registry and
// returns the canonical download_url usage that pins the uploaded bytes.
func PublishArchive(ctx context.Context, client *http.Client, file, destination string) (string, error) {
	u, err := url.Parse(destination)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil ||
		u.ForceQuery || u.RawQuery != "" || strings.Contains(destination, "#") || !IsArchiveFile(u.Path) {
		return "", fmt.Errorf("publish destination must be an HTTPS .kpar URL without credentials, query, or fragment")
	}
	o, err := origin.Parse(destination)
	if err != nil || o.Kind != origin.RemoteArchive || o.Source != destination {
		return "", fmt.Errorf("publish destination must resolve as a remote KPAR resource")
	}
	if !IsArchiveFile(file) {
		return "", fmt.Errorf("publish input must have a .kpar extension")
	}
	source, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > MaxArchiveSize {
		return "", fmt.Errorf("%s: expected a regular KPAR file no larger than %d bytes", file, MaxArchiveSize)
	}
	// Upload the same private snapshot that was verified.
	snapshot, err := os.CreateTemp("", "syspi-publish-*.kpar")
	if err != nil {
		return "", err
	}
	defer os.Remove(snapshot.Name())
	defer snapshot.Close()
	size, err := io.Copy(snapshot, io.LimitReader(source, MaxArchiveSize+1))
	if err != nil {
		return "", err
	}
	if size > MaxArchiveSize {
		return "", fmt.Errorf("%s: archive exceeds %d bytes", file, MaxArchiveSize)
	}
	archive, err := OpenArchive(snapshot.Name())
	if err != nil {
		return "", fmt.Errorf("verifying %s: %w", file, err)
	}
	usage := (&origin.Origin{
		Kind: origin.RemoteArchive, Name: archive.Project.Name, Version: archive.Project.Version, Source: destination,
		Checksums: []digest.Checksum{{Algorithm: "sha256", Value: archive.SHA256}},
	}).Canonical()
	archive.Close()
	if _, err := snapshot.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	if client == nil {
		client, err = netrc.Client()
		if err != nil {
			return "", err
		}
	}
	upload := *client
	upload.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), snapshot)
	if err != nil {
		return "", err
	}
	req.ContentLength = size
	req.Header.Set("Content-Type", "application/zip")
	req.Header.Set("If-None-Match", "*")
	resp, err := upload.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusNoContent:
		return usage, nil
	case http.StatusPreconditionFailed:
		return "", fmt.Errorf("publish %s: artifact already exists", u)
	default:
		return "", fmt.Errorf("publish %s: HTTP %d%s", u, resp.StatusCode, netrc.Hint(resp.StatusCode, u.Hostname()))
	}
}

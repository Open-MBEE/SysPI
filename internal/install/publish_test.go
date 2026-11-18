// SPDX-License-Identifier: Apache-2.0

package install

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Open-MBEE/SysPI/internal/origin"
	"github.com/Open-MBEE/SysPI/internal/project"
)

func TestPublishAndInstall(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, project.ProjectFile, `{"name":"library","version":"1.0.0"}`)
	write(t, dir, "Base.kerml", "package Base;\n")
	file := filepath.Join(t.TempDir(), "library.kpar")
	if _, err := project.Bundle(dir, file, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var stored []byte
	putCount := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case http.MethodPut:
			putCount++
			if r.Header.Get("If-None-Match") != "*" {
				w.WriteHeader(http.StatusBadRequest)
			} else if stored != nil {
				w.WriteHeader(http.StatusPreconditionFailed)
			} else {
				var err error
				stored, err = io.ReadAll(r.Body)
				if err != nil {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.WriteHeader(http.StatusCreated)
			}
		case http.MethodGet:
			w.Write(stored)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()
	ctx := context.Background()
	url := server.URL + "/library/1.0.0/library.kpar"
	usage, err := project.PublishArchive(ctx, server.Client(), file, url)
	if err != nil {
		t.Fatal(err)
	}
	o, err := origin.Parse(usage)
	if err != nil || !o.Hosted() || o.Canonical() != usage || o.Version != "1.0.0" || len(o.Checksums) != 1 {
		t.Fatalf("published usage %q: %+v, %v", usage, o, err)
	}
	if _, err := project.PublishArchive(ctx, server.Client(), file, url); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("publish existing artifact: %v", err)
	}
	root := consumer(t, project.Usage{Resource: usage})
	res, _ := run(t, root, Options{HTTP: server.Client()})
	if entry := res.Lock.Find(o.Identity()); entry == nil || entry.SHA256 != o.Checksums[0].Value {
		t.Fatalf("published digest was not locked: %+v", entry)
	}
	published, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	installed, err := os.ReadFile(filepath.Join(root, project.LibDir, "library_1.0.0.kpar"))
	if err != nil || !bytes.Equal(published, installed) {
		t.Fatalf("installed bytes differ: %v", err)
	}
	write(t, filepath.Dir(file), filepath.Base(file), "invalid ZIP")
	if _, err := project.PublishArchive(ctx, server.Client(), file, url); err == nil || !strings.Contains(err.Error(), "verifying") {
		t.Fatalf("publish invalid archive: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if putCount != 2 {
		t.Fatalf("unexpected uploads: %d", putCount)
	}
}

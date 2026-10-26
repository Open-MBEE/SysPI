// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// emitIndex writes a one-package index for acme/tools 3.0.0 under root.
func emitIndex(t *testing.T, root string) string {
	t.Helper()
	bin := syspiBinary(t)
	pkg := filepath.Join(root, "tools")
	write(t, pkg, ".project.json", "{\n  \"name\": \"tools\",\n  \"publisher\": \"acme\",\n  \"version\": \"3.0.0\"\n}\n")
	write(t, pkg, "Tools.sysml", "package Tools;\n")
	idx := filepath.Join(root, "index")
	sh(t, root, bin, "index", "emit", "-o", idx, pkg)
	return idx
}

func installed(t *testing.T, app string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(app, "lib", "acme-tools_3.0.0.kpar")); err != nil {
		t.Fatal(err)
	}
	lk, _ := os.ReadFile(filepath.Join(app, ".project.lock.json"))
	pj, _ := os.ReadFile(filepath.Join(app, ".project.json"))
	for _, secret := range []string{"t0k3n", "s3cret"} {
		if strings.Contains(string(lk), secret) || strings.Contains(string(pj), secret) {
			t.Fatalf("%s leaked into a project file", secret)
		}
	}
}

func TestSysandDiscoveryDocument(t *testing.T) {
	bin := syspiBinary(t)
	root := t.TempDir()
	idx := emitIndex(t, root)
	var files int32
	fileSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&files, 1)
		http.FileServer(http.Dir(idx)).ServeHTTP(w, r)
	}))
	defer fileSrv.Close()
	disco := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sysand-index-config.json" {
			w.Write([]byte(`{"index_root": "` + fileSrv.URL + `/", "api_root": "` + fileSrv.URL + `/api/"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer disco.Close()
	app := initApp(t, root, "app-disco")
	sh(t, app, bin, "add", "-index", disco.URL, "pkg:sysand/acme/tools")
	installed(t, app)
	if atomic.LoadInt32(&files) < 2 {
		t.Fatalf("versions.json and project.kpar must come from index_root, saw %d requests", files)
	}
	lk, _ := os.ReadFile(filepath.Join(app, ".project.lock.json"))
	if !strings.Contains(string(lk), fileSrv.URL+"/acme/tools/3.0.0/project.kpar") {
		t.Fatalf("lock source must be the discovered index_root:\n%s", lk)
	}
	// A relative index_root is refused.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"index_root": "files/"}`))
	}))
	defer bad.Close()
	out, err := shErr(initApp(t, root, "app-bad"), nil, bin, "add", "-index", bad.URL, "pkg:sysand/acme/tools")
	if err == nil || !strings.Contains(out, "supplied a relative URL `files/` for `index_root`") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func bearerOrBasic(idx string, seen *[]string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = append(*seen, r.Header.Get("Authorization"))
		auth := r.Header.Get("Authorization")
		u, p, ok := r.BasicAuth()
		if auth != "Bearer t0k3n" && !(ok && u == "alice" && p == "s3cret") {
			w.Header().Set("WWW-Authenticate", `Bearer realm="idx"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		http.FileServer(http.Dir(idx)).ServeHTTP(w, r)
	})
}

func TestSysandEnvCredentials(t *testing.T) {
	bin := syspiBinary(t)
	root := t.TempDir()
	idx := emitIndex(t, root)
	var seen []string
	srv := httptest.NewServer(bearerOrBasic(idx, &seen))
	defer srv.Close()
	noNetrc := "NETRC=" + filepath.Join(root, "no-netrc")

	t.Run("bearer", func(t *testing.T) {
		seen = nil
		app := initApp(t, root, "app-bearer2")
		out, err := shErr(app, []string{noNetrc, "SYSAND_CRED_IDX=" + srv.URL + "/**", "SYSAND_CRED_IDX_BEARER_TOKEN=t0k3n"}, bin, "add", "-index", srv.URL, "pkg:sysand/acme/tools")
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		installed(t, app)
		if seen[0] != "" || seen[1] != "Bearer t0k3n" {
			t.Fatalf("anonymous first, then the bearer: %q", seen)
		}
		if strings.Contains(out, "t0k3n") {
			t.Fatal("token printed")
		}
	})
	t.Run("basic", func(t *testing.T) {
		seen = nil
		app := initApp(t, root, "app-basic")
		out, err := shErr(app, []string{noNetrc, "SYSAND_CRED_IDX=" + srv.URL + "/**", "SYSAND_CRED_IDX_BASIC_USER=alice", "SYSAND_CRED_IDX_BASIC_PASS=s3cret"}, bin, "add", "-index", srv.URL, "pkg:sysand/acme/tools")
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		installed(t, app)
		if seen[0] != "" || !strings.HasPrefix(seen[1], "Basic ") {
			t.Fatalf("anonymous first, then Basic: %q", seen)
		}
	})
	t.Run("glob does not match", func(t *testing.T) {
		seen = nil
		out, err := shErr(initApp(t, root, "app-nomatch"), []string{noNetrc, "SYSAND_CRED_IDX=https://elsewhere.example/**", "SYSAND_CRED_IDX_BEARER_TOKEN=t0k3n"}, bin, "add", "-index", srv.URL, "pkg:sysand/acme/tools")
		if err == nil || !strings.Contains(out, "HTTP 401") || !strings.Contains(out, "SYSAND_CRED_<LABEL>") || !strings.Contains(out, "netrc") {
			t.Fatalf("hint must name SYSAND_CRED_* and netrc: %v\n%s", err, out)
		}
		if len(seen) != 1 || seen[0] != "" {
			t.Fatalf("exactly one anonymous request: %q", seen)
		}
	})
	t.Run("malformed group", func(t *testing.T) {
		out, err := shErr(initApp(t, root, "app-malformed"), []string{"SYSAND_CRED_IDX_BEARER_TOKEN=t0k3n"}, bin, "add", "-index", srv.URL, "pkg:sysand/acme/tools")
		if err == nil || !strings.Contains(out, "please specify URL pattern SYSAND_CRED_IDX") || strings.Contains(out, "t0k3n") {
			t.Fatalf("%v\n%s", err, out)
		}
	})
}

func TestSysandCredentialDroppedOnCrossHostRedirect(t *testing.T) {
	bin := syspiBinary(t)
	root := t.TempDir()
	idx := emitIndex(t, root)
	var landed []string
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		landed = append(landed, r.Header.Get("Authorization"))
		http.FileServer(http.Dir(idx)).ServeHTTP(w, r)
	}))
	defer files.Close()
	// The front host demands the bearer, then redirects every file to a
	// different host name (localhost vs 127.0.0.1) that must not see it.
	other := strings.Replace(files.URL, "127.0.0.1", "localhost", 1)
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer t0k3n" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		http.Redirect(w, r, other+r.URL.Path, http.StatusFound)
	}))
	defer front.Close()
	app := initApp(t, root, "app-redirect")
	out, err := shErr(app, []string{"SYSAND_CRED_ALL=http://**", "SYSAND_CRED_ALL_BEARER_TOKEN=t0k3n"}, bin, "add", "-index", front.URL, "pkg:sysand/acme/tools")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	installed(t, app)
	if len(landed) == 0 {
		t.Fatal("nothing reached the redirect target")
	}
	for _, a := range landed {
		if a != "" {
			t.Fatalf("credential followed a cross-host redirect: %q", landed)
		}
	}
}

func TestSysandDefaultIndexEnv(t *testing.T) {
	bin := syspiBinary(t)
	root := t.TempDir()
	idx := emitIndex(t, root)
	srv := httptest.NewServer(http.FileServer(http.Dir(idx)))
	defer srv.Close()
	app := initApp(t, root, "app-default")
	// No -index, no SYSPI_INDEX, no SYSAND_INDEX: the default index chain.
	out, err := shErr(app, []string{"SYSPI_INDEX=", "SYSAND_DEFAULT_INDEX=" + srv.URL}, bin, "add", "pkg:sysand/acme/tools")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	installed(t, app)
	// SYSAND_INDEX is read too, ahead of the defaults.
	app2 := initApp(t, root, "app-sysand-index")
	if out, err := shErr(app2, []string{"SYSAND_INDEX=" + srv.URL}, bin, "add", "pkg:sysand/acme/tools"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	installed(t, app2)
	// An empty entry is sysand's error.
	out, err = shErr(initApp(t, root, "app-empty"), []string{"SYSAND_INDEX=" + srv.URL + ","}, bin, "add", "pkg:sysand/acme/tools")
	if err == nil || !strings.Contains(out, "invalid `SYSAND_INDEX` environment variable: index URL is empty") || !strings.Contains(out, "hint: check for a stray `,`") {
		t.Fatalf("%v\n%s", err, out)
	}
	// A rejected index value leaves .project.json unchanged.
	app3 := initApp(t, root, "app-empty-keeps-project")
	before, _ := os.ReadFile(filepath.Join(app3, ".project.json"))
	shErr(app3, []string{"SYSAND_INDEX=" + srv.URL + ","}, bin, "add", "pkg:sysand/acme/tools")
	if after, _ := os.ReadFile(filepath.Join(app3, ".project.json")); string(after) != string(before) {
		t.Fatalf(".project.json changed by a failed add:\n%s", after)
	}
	// An unreachable default index never blocks a configured index that has the package.
	app4 := initApp(t, root, "app-default-down")
	if out, err := shErr(app4, []string{"SYSAND_DEFAULT_INDEX=http://127.0.0.1:1/"}, bin, "add", "-index", idx, "pkg:sysand/acme/tools"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	installed(t, app4)
	// -no-index: nothing is consulted, not even the default.
	out, err = shErr(initApp(t, root, "app-noindex"), nil, bin, "add", "-no-index", "pkg:sysand/acme/tools")
	if err == nil || !strings.Contains(out, "no sysand index configured") {
		t.Fatalf("%v\n%s", err, out)
	}
}

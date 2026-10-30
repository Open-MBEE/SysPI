// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// basicAuth wraps a handler so every request must carry user:pw and counts
// the requests that did.
func basicAuth(user, pw string, seen *int32, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != user || p != pw {
			w.Header().Set("WWW-Authenticate", `Basic realm="pkg"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		atomic.AddInt32(seen, 1)
		next.ServeHTTP(w, r)
	})
}

// initApp makes a consuming project and returns its directory.
func initApp(t *testing.T, root, name string) string {
	t.Helper()
	bin := syspiBinary(t)
	app := filepath.Join(root, name)
	os.MkdirAll(app, 0o755)
	sh(t, app, bin, "init", "-name", "app")
	return app
}

func TestAuthGitCredentialHelper(t *testing.T) {
	bin := syspiBinary(t)
	root := t.TempDir()
	bare := gitFixture(t, root)
	sh(t, bare, "git", "update-server-info") // dumb HTTP needs info/refs
	var seen int32
	srv := httptest.NewServer(basicAuth("alice", "s3cret", &seen, http.StripPrefix("/widgets.git/", http.FileServer(http.Dir(bare)))))
	defer srv.Close()
	// The plain clone URL keeps the http transport; its identity is pkg:git/<host:port>/widgets.git.
	res := "git+" + srv.URL + "/widgets.git@1.0.0"

	// Without credentials: a clear hint, no prompt, no hang.
	app := initApp(t, root, "app-nocred")
	out, err := shErr(app, nil, bin, "add", res)
	if err == nil || !strings.Contains(out, "hint: git needs credentials") || !strings.Contains(out, "credential helper") {
		t.Fatalf("expected the git auth hint: %v\n%s", err, out)
	}
	// With a credential helper configured through the environment, as a user would.
	helper := filepath.Join(root, "helper.sh")
	os.WriteFile(helper, []byte("#!/bin/sh\necho username=alice\necho password=s3cret\n"), 0o755)
	env := []string{"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=credential.helper", "GIT_CONFIG_VALUE_0=" + filepath.ToSlash(helper)}
	app2 := initApp(t, root, "app-cred")
	if out, err := shErr(app2, env, bin, "add", res); err != nil {
		t.Fatalf("add with helper: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(app2, "lib", "acme-widgets_1.0.0", "Widgets.sysml")); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&seen) == 0 {
		t.Fatal("server never saw an Authorization header")
	}
	lk, _ := os.ReadFile(filepath.Join(app2, ".project.lock.json"))
	if strings.Contains(string(lk), "s3cret") || strings.Contains(string(lk), "alice") {
		t.Fatal("credentials leaked into the lock")
	}
}

func TestAuthHgAuthSection(t *testing.T) {
	requireOrSkip(t, "hg")
	bin := syspiBinary(t)
	root := t.TempDir()
	repo := hgFixture(t, root)
	var seen int32
	srv := httptest.NewServer(basicAuth("alice", "s3cret", &seen, http.StripPrefix("/repo/", http.FileServer(http.Dir(repo)))))
	defer srv.Close()
	hostport := strings.TrimPrefix(srv.URL, "http://")
	hgrc := filepath.Join(root, "hgrc")
	os.WriteFile(hgrc, []byte("[auth]\npkg.prefix = http://"+hostport+"/repo\npkg.username = alice\npkg.password = s3cret\n"), 0o600)
	app := initApp(t, root, "app-hg-nocred")
	out, err := shErr(app, []string{"HGRCPATH=" + filepath.Join(root, "empty-hgrc")}, bin, "add", "hg+static-http://"+hostport+"/repo@1.0.0")
	if err == nil || !strings.Contains(out, "hint: hg needs credentials") {
		t.Fatalf("expected the hg auth hint: %v\n%s", err, out)
	}
	app2 := initApp(t, root, "app-hg-cred")
	// HGPLAIN=1 is set by syspi itself; [auth] must still apply.
	if out, err := shErr(app2, []string{"HGRCPATH=" + hgrc, "HGPLAIN=1"}, bin, "add", "hg+static-http://"+hostport+"/repo@1.0.0"); err != nil {
		t.Fatalf("add with [auth]: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(app2, "lib", "acme-widgets_1.0.0", "Widgets.sysml")); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&seen) == 0 {
		t.Fatal("server never saw an Authorization header")
	}
}

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func TestAuthSvnNetrc(t *testing.T) {
	requireOrSkip(t, "svn", "svnadmin", "svnserve")
	bin := syspiBinary(t)
	root := t.TempDir()
	svnFixture(t, root)
	conf := filepath.Join(root, "widgets-svn", "conf")
	for name, content := range map[string]string{
		"svnserve.conf": "[general]\nanon-access = none\nauth-access = read\npassword-db = passwd\nrealm = pkg\n",
		"passwd":        "[users]\nalice = s3cret\n",
	} {
		if err := os.WriteFile(filepath.Join(conf, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	port := freePort(t)
	pid := filepath.Join(root, "svnserve.pid")
	cmd := exec.Command("svnserve", "-d", "--foreground", "--listen-host", "127.0.0.1", "--listen-port", strconv.Itoa(port), "-r", root, "--pid-file", pid)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	svnURL := "svn://127.0.0.1:" + strconv.Itoa(port) + "/widgets-svn"
	deadline := time.Now().Add(10 * time.Second)
	for {
		if c, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port)); err == nil {
			c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("svnserve did not start")
		}
		time.Sleep(50 * time.Millisecond)
	}
	// A fresh HOME: svn has no cached credentials and may write none.
	home := filepath.Join(root, "home")
	os.MkdirAll(home, 0o755)
	noNetrc := "NETRC=" + filepath.Join(root, "no-netrc")
	app := initApp(t, root, "app-svn-nocred")
	out, err := shErr(app, []string{"HOME=" + home, noNetrc}, bin, "add", svnURL+"@1.0.0")
	if err == nil || !strings.Contains(out, "hint: svn needs credentials") || !strings.Contains(out, "netrc") || !strings.Contains(out, "svn+ssh://") {
		t.Fatalf("expected the svn auth hint naming netrc: %v\n%s", err, out)
	}
	netrcFile := filepath.Join(root, "netrc")
	os.WriteFile(netrcFile, []byte("machine 127.0.0.1 login alice password s3cret\n"), 0o600)
	app2 := initApp(t, root, "app-svn-netrc")
	if out, err := shErr(app2, []string{"HOME=" + home, "NETRC=" + netrcFile}, bin, "add", svnURL+"@1.0.0"); err != nil {
		t.Fatalf("add with netrc: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(app2, "lib", "acme-widgets_1.0.0", "Widgets.sysml")); err != nil {
		t.Fatal(err)
	}
	if cached, _ := filepath.Glob(filepath.Join(home, ".subversion", "auth", "svn.simple", "*")); len(cached) != 0 {
		t.Fatalf("--no-auth-cache: svn must not cache the netrc credentials, found %v", cached)
	}
	for _, f := range []string{".project.json", ".project.lock.json"} {
		if b, _ := os.ReadFile(filepath.Join(app2, f)); strings.Contains(string(b), "s3cret") || strings.Contains(string(b), "alice") {
			t.Fatalf("credentials in %s", f)
		}
	}
	// A wrong password fails with the hint and never shows the secret.
	os.WriteFile(netrcFile, []byte("machine 127.0.0.1 login alice password wr0ng\n"), 0o600)
	out, err = shErr(initApp(t, root, "app-svn-wrong"), []string{"HOME=" + home, "NETRC=" + netrcFile}, bin, "add", svnURL+"@1.0.0")
	if err == nil || !strings.Contains(out, "hint: svn needs credentials") {
		t.Fatalf("%v\n%s", err, out)
	}
	if strings.Contains(out, "wr0ng") {
		t.Fatalf("password in the error text:\n%s", out)
	}
	if !strings.Contains(out, "--password-from-stdin") || strings.Contains(out, "--password ") {
		t.Fatalf("the password must travel on stdin, not argv:\n%s", out)
	}
}

func TestAuthHTTPNetrc(t *testing.T) {
	bin := syspiBinary(t)
	root := t.TempDir()
	pkg := filepath.Join(root, "tools")
	write(t, pkg, ".project.json", "{\n  \"name\": \"tools\",\n  \"publisher\": \"acme\",\n  \"version\": \"3.0.0\"\n}\n")
	write(t, pkg, "Tools.sysml", "package Tools;\n")
	files := filepath.Join(root, "files")
	os.MkdirAll(files, 0o755)
	sh(t, root, bin, "bundle", "-o", filepath.Join(files, "tools.kpar"), pkg)
	var seen int32
	srv := httptest.NewServer(basicAuth("alice", "s3cret", &seen, http.FileServer(http.Dir(files))))
	defer srv.Close()
	host, _, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))

	app := initApp(t, root, "app-http-nocred")
	out, err := shErr(app, []string{"NETRC=" + filepath.Join(root, "no-netrc")}, bin, "add", srv.URL+"/tools.kpar")
	if err == nil || !strings.Contains(out, "HTTP 401") || !strings.Contains(out, "hint: the server wants credentials") || !strings.Contains(out, "netrc") {
		t.Fatalf("expected the netrc hint: %v\n%s", err, out)
	}
	netrcFile := filepath.Join(root, "netrc")
	os.WriteFile(netrcFile, []byte("machine "+host+" login alice password s3cret\n"), 0o600)
	app2 := initApp(t, root, "app-http-cred")
	if out, err := shErr(app2, []string{"NETRC=" + netrcFile}, bin, "add", srv.URL+"/tools.kpar"); err != nil {
		t.Fatalf("add with netrc: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(app2, "lib", "acme-tools_3.0.0.kpar")); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&seen) < 1 {
		t.Fatal("expected the archive to be fetched with credentials")
	}
	lk, _ := os.ReadFile(filepath.Join(app2, ".project.lock.json"))
	if strings.Contains(string(lk), "s3cret") {
		t.Fatal("credentials leaked into the lock")
	}
}

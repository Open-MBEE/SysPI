// SPDX-License-Identifier: Apache-2.0

package netrc

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestParseAndTransport(t *testing.T) {
	f := Parse("# comment\nmachine idx.example.org login alice password s3cret\nmacdef init\nls\n\ndefault login bob password pw\n")
	if c, ok := f.Lookup("IDX.example.org"); !ok || c.Login != "alice" || c.Password != "s3cret" {
		t.Fatalf("lookup = %+v %v", c, ok)
	}
	if c, ok := f.Lookup("other"); !ok || c.Login != "bob" {
		t.Fatalf("default = %+v %v", c, ok)
	}
	if _, ok := Parse("machine a login x password y").Lookup("b"); ok {
		t.Fatal("no default matched")
	}

	var sawA, sawB string
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawB = r.Header.Get("Authorization")
		w.Write([]byte("b"))
	}))
	defer b.Close()
	a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawA = r.Header.Get("Authorization")
		http.Redirect(w, r, b.URL+"/x", http.StatusFound)
	}))
	defer a.Close()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "netrc"), []byte("machine 127.0.0.1 login alice password pw\n"), 0o600)
	t.Setenv("NETRC", filepath.Join(dir, "netrc"))
	c, err := Client()
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Get(a.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if sawA == "" || sawB == "" {
		t.Fatalf("same-host entry not applied: a=%q b=%q", sawA, sawB)
	}
	// A host without an entry gets nothing, also after a redirect from one that has.
	os.WriteFile(filepath.Join(dir, "netrc"), []byte("machine localhost login alice password pw\n"), 0o600)
	c, _ = Client()
	resp, err = c.Get(a.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if sawA != "" || sawB != "" {
		t.Fatalf("credentials sent to an unlisted host: a=%q b=%q", sawA, sawB)
	}
	// The default entry serves the first host but never a redirect target
	// on another origin, so a redirecting server cannot harvest it.
	os.WriteFile(filepath.Join(dir, "netrc"), []byte("default login alice password pw\n"), 0o600)
	c, _ = Client()
	resp, err = c.Get(a.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if sawA == "" || sawB != "" {
		t.Fatalf("default entry on a redirect: a=%q b=%q", sawA, sawB)
	}
	// Nor does the default entry return once the chain comes back to the first origin.
	var sawBack, bounceURL string
	back := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/start":
			http.Redirect(w, r, bounceURL+"/away", http.StatusFound)
		default:
			sawBack = r.Header.Get("Authorization")
		}
	}))
	defer back.Close()
	bounce := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, back.URL+"/home", http.StatusFound)
	}))
	defer bounce.Close()
	bounceURL = bounce.URL
	resp, err = c.Get(back.URL + "/start")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if sawBack != "" {
		t.Fatalf("default entry after a cross-origin chain: %q", sawBack)
	}
	for in, want := range map[string]string{
		"https://user:pw@example.org/x.kpar?token=abc": "https://example.org/x.kpar?...",
		"https://example.org/x.kpar":                   "https://example.org/x.kpar",
		"://bad":                                       "://bad",
	} {
		if got := RedactURL(in); got != want {
			t.Errorf("RedactURL(%q) = %q, want %q", in, got, want)
		}
	}
	t.Setenv("NETRC", filepath.Join(dir, "missing"))
	if f, err := Load(); err != nil || len(f.Machines) != 0 {
		t.Fatalf("missing netrc: %v %+v", err, f)
	}
}

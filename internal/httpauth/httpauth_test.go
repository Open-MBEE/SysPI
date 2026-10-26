// SPDX-License-Identifier: Apache-2.0

package httpauth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Open-MBEE/SysPI/internal/netrc"
)

func TestFromEnvGrammar(t *testing.T) {
	p, err := FromEnv([]string{"SYSAND_CRED_A=https://a.example/**", "SYSAND_CRED_A_BEARER_TOKEN=tok", "SYSAND_CRED_B=https://b.example/*", "SYSAND_CRED_B_BASIC_USER=u", "SYSAND_CRED_B_BASIC_PASS=p", "OTHER=1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Creds) != 2 || p.Creds[0].Label != "A" || p.Creds[0].Token != "tok" || p.Creds[1].User != "u" || p.Creds[1].Pass != "p" {
		t.Fatalf("%+v", p.Creds)
	}
	for _, tc := range []struct{ env, want string }{
		{"SYSAND_CRED_BASIC_PASS=x", "has no label"},
		{"SYSAND_CRED__BEARER_TOKEN=x", "has no label"},
		{"SYSAND_CRED_X_BEARER_TOKEN=x", "please specify URL pattern SYSAND_CRED_X"},
		{"SYSAND_CRED_X=https://x/**\x00SYSAND_CRED_X_BASIC_USER=u", "both (or neither)"},
		{"SYSAND_CRED_X=https://x/**", "either HTTP basic"},
		{"SYSAND_CRED_X=https://x/**\x00SYSAND_CRED_X_BASIC_USER=u\x00SYSAND_CRED_X_BASIC_PASS=p\x00SYSAND_CRED_X_BEARER_TOKEN=t", "only one of"},
	} {
		_, err := FromEnv(strings.Split(tc.env, "\x00"))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: got %v, want %q", tc.env, err, tc.want)
		}
	}
}

func TestGlob(t *testing.T) {
	for _, tc := range []struct {
		glob, url string
		want      bool
	}{
		{"https://idx.example/**", "https://idx.example/a/b/versions.json", true},
		{"https://idx.example/*", "https://idx.example/a/b", false},
		{"https://idx.example/*", "https://idx.example/a", true},
		{"https://*.example/**", "https://idx.example/x", true},
		{"https://{a,b}.example/**", "https://b.example/x", true},
		{"https://{a,b}.example/**", "https://c.example/x", false},
		{"https://idx.example/v?/**", "https://idx.example/v1/x", true},
		{"https://idx.example/**", "https://other.example/x", false},
	} {
		re, err := compileGlob(tc.glob)
		if err != nil {
			t.Fatal(err)
		}
		if got := re.MatchString(tc.url); got != tc.want {
			t.Errorf("%s ~ %s: %v", tc.glob, tc.url, got)
		}
	}
}

func TestTransportPolicy(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path+" "+r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/open":
			w.WriteHeader(200)
		case "/bearer":
			if r.Header.Get("Authorization") == "Bearer good" {
				w.WriteHeader(200)
			} else {
				w.WriteHeader(401)
			}
		case "/hop":
			http.Redirect(w, r, "http://127.0.0.1:1/elsewhere", http.StatusFound)
		case "/rate":
			w.WriteHeader(429)
		}
	}))
	defer srv.Close()
	env, _ := FromEnv([]string{"SYSAND_CRED_BAD=" + srv.URL + "/**", "SYSAND_CRED_BAD_BEARER_TOKEN=bad", "SYSAND_CRED_GOOD=" + srv.URL + "/b*", "SYSAND_CRED_GOOD_BEARER_TOKEN=good"})
	var warned string
	c := &http.Client{Transport: &Transport{Env: env, Netrc: netrc.Parse("machine 127.0.0.1 login n password p"), Warn: func(s string) { warned = s }}}
	get := func(path string) int {
		t.Helper()
		resp, err := c.Get(srv.URL + path)
		if err != nil {
			return -1
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if get("/open") != 200 || seen[0] != "/open " {
		t.Fatalf("matched URL must go out anonymously first: %v", seen)
	}
	seen = nil
	if get("/bearer") != 200 {
		t.Fatalf("bearer retry failed: %v", seen)
	}
	if len(seen) != 3 || seen[0] != "/bearer " || seen[1] != "/bearer Bearer bad" || seen[2] != "/bearer Bearer good" {
		t.Fatalf("anonymous, then each match in label order: %v", seen)
	}
	if !strings.Contains(warned, "matches multiple authentication URL globs") || !strings.Contains(warned, "SYSAND_CRED_BAD") || strings.Contains(warned, "good") {
		t.Fatalf("warning %q", warned)
	}
	seen = nil
	if get("/rate") != 429 || len(seen) != 1 {
		t.Fatalf("429 is not an auth verdict: %v", seen)
	}
	seen = nil
	get("/hop")
	if len(seen) != 1 || seen[0] != "/hop " {
		t.Fatalf("redirect hop: %v", seen)
	}
	// Unmatched URL on a netrc host carries the netrc Basic credential.
	plain := &http.Client{Transport: &Transport{Env: &Policy{}, Netrc: netrc.Parse("machine 127.0.0.1 login n password p")}}
	seen = nil
	resp, err := plain.Get(srv.URL + "/open")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !strings.HasPrefix(seen[0], "/open Basic ") {
		t.Fatalf("netrc fallback: %v", seen)
	}
}

func TestRedirectKeepsCredentialOnSameHostOnly(t *testing.T) {
	var other []string
	otherSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		other = append(other, r.Header.Get("Authorization"))
	}))
	defer otherSrv.Close()
	var same []string
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		same = append(same, r.URL.Path+" "+r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/away":
			if r.Header.Get("Authorization") == "" {
				w.WriteHeader(401)
				return
			}
			http.Redirect(w, r, otherSrv.URL+"/landed", http.StatusFound)
		case "/local":
			if r.Header.Get("Authorization") == "" {
				w.WriteHeader(401)
				return
			}
			http.Redirect(w, r, srv.URL+"/landed", http.StatusFound)
		case "/landed":
			if r.Header.Get("Authorization") == "" {
				w.WriteHeader(401)
			}
		}
	}))
	defer srv.Close()
	// localhost vs 127.0.0.1: same server, different host names, and the
	// glob matches both.
	alias := strings.Replace(otherSrv.URL, "127.0.0.1", "localhost", 1)
	env, _ := FromEnv([]string{"SYSAND_CRED_T=http://**", "SYSAND_CRED_T_BEARER_TOKEN=tok"})
	_ = alias
	c := &http.Client{Transport: &Transport{Env: env}}
	resp, err := c.Get(srv.URL + "/away")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(other) != 1 || other[0] != "" {
		t.Fatalf("cross-host redirect must drop the credential: %v", other)
	}
	same = nil
	resp, err = c.Get(srv.URL + "/local")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || same[len(same)-1] != "/landed Bearer tok" {
		t.Fatalf("same-host redirect keeps the credential after the anonymous try: %d %v", resp.StatusCode, same)
	}
}

// SPDX-License-Identifier: Apache-2.0

package index

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseLocation(t *testing.T) {
	loc, err := ParseLocation("https://idx.example/base")
	if err != nil || loc.URL("a/b/versions.json") != "https://idx.example/base/a/b/versions.json" {
		t.Fatalf("%v %v", loc, err)
	}
	tpl, err := ParseLocation("https://gl.example/api/v4/projects/1/repository/files/{path}/raw?ref=main")
	if err != nil || tpl.URL("acme/tools/1.0.0/project.kpar") != "https://gl.example/api/v4/projects/1/repository/files/acme%2Ftools%2F1.0.0%2Fproject.kpar/raw?ref=main" {
		t.Fatalf("%v %v", tpl, err)
	}
	raw, err := ParseLocation("https://gt.example/raw/{path_raw}?ref=main")
	if err != nil || raw.URL("acme/tools/1.0.0/project.kpar") != "https://gt.example/raw/acme/tools/1.0.0/project.kpar?ref=main" {
		t.Fatalf("%v %v", raw, err)
	}
	if d, err := ParseLocation("/tmp/idx"); err != nil || !d.Local() {
		t.Fatal(err)
	}
	for _, tc := range []struct{ in, want string }{
		{"", "index URL is empty"},
		{"https://u:p@idx.example/", "<redacted>@idx.example"},
		{"https://idx.example/x#f", "fragment"},
		{"https://idx.example/%7Bpath%7D", "percent-encoded braces"},
		{"https://idx.example/{file}", "unknown placeholder `{file}`"},
		{"https://idx.example/{path}/{path}", "exactly one placeholder"},
		{"ftp://idx.example/{path}", "unsupported scheme"},
	} {
		_, err := ParseLocation(tc.in)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: %v, want %q", tc.in, err, tc.want)
		}
	}
}

func TestDiscover(t *testing.T) {
	ctx := context.Background()
	flat := httptest.NewServer(http.NotFoundHandler())
	defer flat.Close()
	loc, _ := ParseLocation(flat.URL)
	ep, err := Discover(ctx, flat.Client(), loc)
	if err != nil || ep.IndexRoot != loc || ep.APIRoot != "" {
		t.Fatalf("absent document means flat root: %+v %v", ep, err)
	}
	doc := `{"index_root": "https://files.example/idx", "api_root": "https://api.example/"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/"+ConfigFile {
			w.Write([]byte(doc))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	loc, _ = ParseLocation(srv.URL)
	ep, err = Discover(ctx, srv.Client(), loc)
	if err != nil || ep.IndexRoot.URL("index.json") != "https://files.example/idx/index.json" || ep.APIRoot != "https://api.example/" {
		t.Fatalf("%+v %v", ep, err)
	}
	for _, tc := range []struct{ doc, want string }{
		{`{"index_root": "idx/"}`, "supplied a relative URL `idx/` for `index_root`"},
		{`{"api_root": "/api"}`, "relative URL `/api` for `api_root`"},
		{`{"index_root": "ftp://x.example/"}`, "non-HTTP(S) URL"},
		{`{"index_root": "https://u:p@x.example/"}`, "URL userinfo"},
		{`{"api_root": "https://x.example/{path}"}`, "`api_root` must be a plain URL"},
		{`nope`, "not valid JSON"},
	} {
		doc = tc.doc
		_, err := Discover(ctx, srv.Client(), loc)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", tc.doc, err, tc.want)
		}
	}
	doc = `{"index_root": "` + srv.URL + `/files/{path}"}`
	ep, err = Discover(ctx, srv.Client(), loc)
	if err != nil || ep.IndexRoot.URL("a/b") != srv.URL+"/files/a%2Fb" {
		t.Fatalf("templated index_root: %+v %v", ep, err)
	}
}

func TestReadVersionsRules(t *testing.T) {
	ctx := context.Background()
	body := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "versions.json") {
			w.Write([]byte(body))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	x, err := Open(ctx, srv.Client(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	ok := `{"version":"%s","usage":[],"kpar_size":1,"kpar_digest":"sha256:%s"}`
	h := strings.Repeat("ab", 32)
	entry := func(v string) string {
		return strings.ReplaceAll(strings.ReplaceAll(ok, "%s", v), v+`","usage":[],"kpar_size":1,"kpar_digest":"sha256:`+v, v+`","usage":[],"kpar_size":1,"kpar_digest":"sha256:`+h)
	}
	for _, tc := range []struct{ entries, want string }{
		{entry("2.0.0") + "," + entry("1.0.0"), ""},
		{entry("1.0.0") + "," + entry("2.0.0"), "not in descending semver precedence: `1.0.0` precedes `2.0.0` in wire order"},
		{entry("10.0.0-beta.1") + "," + entry("10.0.0"), "`10.0.0-beta.1` precedes `10.0.0`"},
		{entry("1.0.0") + "," + entry("1.0.0"), "lists version `1.0.0` more than once"},
		{entry("1.0"), "non-semver version `1.0`"},
		{entry("1.0.0+b1"), "carrying build metadata"},
		{`{"version":"1.0.0","usage":[],"kpar_size":1,"kpar_digest":"sha1:aa"}`, "malformed `kpar_digest` = `sha1:aa` (expected `sha256:<64-hex>`)"},
	} {
		body = `{"versions":[` + tc.entries + `]}`
		_, err := x.ReadVersions(ctx, "acme", "tools")
		if tc.want == "" && err != nil {
			t.Errorf("%s: %v", tc.entries, err)
		}
		if tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
			t.Errorf("%s: %v, want %q", tc.entries, err, tc.want)
		}
	}
	body = `{"versions":[{"version":"2.0.0","usage":[],"kpar_size":1,"kpar_digest":"sha256:` + h + `","status":"removed"},{"version":"1.5.0","usage":[],"kpar_size":1,"kpar_digest":"sha256:` + h + `","status":"yanked"},` + entry("1.0.0") + `]}`
	vs, err := x.ReadVersions(ctx, "acme", "tools")
	if err != nil {
		t.Fatal(err)
	}
	if a := vs.Available(); len(a) != 1 || a[0].Version != "1.0.0" {
		t.Fatalf("yanked and removed are not candidates: %+v", a)
	}
	if _, err := vs.Lookup("2.0.0"); err == nil || !strings.Contains(err.Error(), `removed upstream (versions.json marks it `+"`"+`status: "removed"`) {
		t.Fatalf("removed must hard-fail: %v", err)
	}
	if e, err := vs.Lookup("1.5.0"); err != nil || e.Status != "yanked" {
		t.Fatalf("yanked stays fetchable for a lock: %v %v", e, err)
	}
	if _, err := vs.Lookup("3.0.0"); err == nil || !strings.Contains(err.Error(), "does not list version `3.0.0`") {
		t.Fatal(err)
	}
}

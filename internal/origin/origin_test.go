// SPDX-License-Identifier: Apache-2.0

package origin

import (
	"path/filepath"
	"strings"
	"testing"
)

const ext = "pkg:generic/OpenSysML-Extensions-Library?vcs_url=git%2Bhttps:%2F%2Fgithub.com%2FOpen-MBEE%2FOpenSysML-Extensions-Library"

// Every accepted spelling normalises to one canonical purl and round-trips.
func TestCanonical(t *testing.T) {
	for _, tc := range []struct{ in, canonical, source string }{
		{ext + "%401.0.0", ext + "%401.0.0", "https://github.com/Open-MBEE/OpenSysML-Extensions-Library"},
		{"pkg:generic/OpenSysML-Extensions-Library?vcs_url=git%2Bhttps%3A%2F%2Fgithub.com%2FOpen-MBEE%2FOpenSysML-Extensions-Library%401.0.0", ext + "%401.0.0", "https://github.com/Open-MBEE/OpenSysML-Extensions-Library"},
		{"pkg:git/github.com/Open-MBEE/OpenSysML-Extensions-Library@1.0.0", ext + "%401.0.0", "https://github.com/Open-MBEE/OpenSysML-Extensions-Library"},
		{"pkg:generic/OpenSysML-Extensions-Library@1.0.0?vcs_url=git%2Bhttps:%2F%2Fgithub.com%2FOpen-MBEE%2FOpenSysML-Extensions-Library", ext + "%401.0.0", "https://github.com/Open-MBEE/OpenSysML-Extensions-Library"},
		// A .git suffix is part of the location and is cloned as written; only <name> drops it.
		{"pkg:git/github.com/Open-MBEE/OpenSysML-Extensions-Library.git", ext + ".git", "https://github.com/Open-MBEE/OpenSysML-Extensions-Library.git"},
		{"git+https://github.com/Open-MBEE/OpenSysML-Extensions-Library.git@1.0.0", ext + ".git%401.0.0", "https://github.com/Open-MBEE/OpenSysML-Extensions-Library.git"},
		{"git+ssh://git.example.org/team/repo@1.2.0#packages/core", "pkg:generic/repo?vcs_url=git%2Bssh:%2F%2Fgit.example.org%2Fteam%2Frepo%401.2.0%23packages%2Fcore", "ssh://git.example.org/team/repo"},
		{"git://git.example.org/team/repo", "pkg:generic/repo?vcs_url=git%2Bgit:%2F%2Fgit.example.org%2Fteam%2Frepo", "git://git.example.org/team/repo"},
		{"hg+https://hg.example.org/repo@2.0.0", "pkg:generic/repo?vcs_url=hg%2Bhttps:%2F%2Fhg.example.org%2Frepo%402.0.0", "https://hg.example.org/repo"},
		{"hg+static-http://127.0.0.1:8000/repo@1.0.0", "pkg:generic/repo?vcs_url=hg%2Bstatic-http:%2F%2F127.0.0.1:8000%2Frepo%401.0.0", "static-http://127.0.0.1:8000/repo"},
		{"svn://svn.example.org/pkg@1.0.0", "pkg:generic/pkg?vcs_url=svn%2Bsvn:%2F%2Fsvn.example.org%2Fpkg%401.0.0", "svn://svn.example.org/pkg"},
		{"svn+https://svn.example.org/repos/pkg", "pkg:generic/pkg?vcs_url=svn%2Bhttps:%2F%2Fsvn.example.org%2Frepos%2Fpkg", "https://svn.example.org/repos/pkg"},
		{"svn+ssh://svn.example.org/repos/pkg", "pkg:generic/pkg?vcs_url=svn%2Bssh:%2F%2Fsvn.example.org%2Frepos%2Fpkg", "svn+ssh://svn.example.org/repos/pkg"},
		{"git+file:///srv/git/widgets@1.0.0#packages/gears", "pkg:generic/widgets?vcs_url=git%2Bfile:%2F%2F%2Fsrv%2Fgit%2Fwidgets%401.0.0%23packages%2Fgears", filepath.FromSlash("/srv/git/widgets")},
		{"svn+file:///srv/svn/widgets", "pkg:generic/widgets?vcs_url=svn%2Bfile:%2F%2F%2Fsrv%2Fsvn%2Fwidgets", "file:///srv/svn/widgets"},
	} {
		o, err := Parse(tc.in)
		if err != nil {
			t.Errorf("%s: %v", tc.in, err)
			continue
		}
		if o.Canonical() != tc.canonical || o.Source != tc.source {
			t.Errorf("%s:\n canonical %s\n want      %s\n source %s", tc.in, o.Canonical(), tc.canonical, o.Source)
			continue
		}
		again, err := Parse(o.Canonical())
		if err != nil || again.Canonical() != o.Canonical() || again.Identity() != o.Identity() || again.Source != o.Source || again.Version != o.Version || again.Subpath != o.Subpath {
			t.Errorf("%s: canonical form does not round-trip: %v %+v", tc.in, err, again)
		}
		if strings.Contains(o.Identity(), "%40") {
			t.Errorf("%s: identity must be version-free: %s", tc.in, o.Identity())
		}
	}
}

func TestArchivesAndDirectories(t *testing.T) {
	for _, tc := range []struct {
		in   string
		kind Kind
	}{
		{"https://www.omg.org/spec/SysML/20250201/Systems-Library.kpar", Standard},
		{"https://example.org/dist/acme-tools_1.0.0.kpar", RemoteArchive},
		{"file:///srv/acme-tools_1.0.0.kpar", LocalArchive},
		{"./vendor/tools.kpar", LocalArchive},
		{"../tools", LocalDir},
		{"file:///srv/tools", LocalDir},
	} {
		o, err := Parse(tc.in)
		if err != nil || o.Kind != tc.kind {
			t.Errorf("%s: %v %v", tc.in, o, err)
		}
	}
}

// srvGitWidgets is /srv/git/widgets as a file-URI path on this machine (/D:/srv/git/widgets on Windows).
var srvGitWidgets = func() string { abs, _ := filepath.Abs(filepath.FromSlash("/srv/git/widgets")); return FileURIPath(abs) }()

func TestRefused(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"pkg:svn/svn.example.org/pkg@1.0.0", "pkg:svn is not a Package-URL type; write pkg:generic/<name>?vcs_url="},
		{"pkg:hg/hg.example.org/repo@1.0.0", "pkg:hg is not a Package-URL type"},
		{"pkg:sysand/acme/tools", "pkg:sysand is not supported; write pkg:generic"},
		{"pkg:github/acme/widgets@1.0.0", "write pkg:git/github.com/acme/widgets@1.0.0 or pkg:generic"},
		{"pkg:cvs/host/repo@1.0.0", "pkg:cvs is not supported"},
		{"https://github.com/acme/widgets@1.0.0", "only dereferenced as a project interchange file (.kpar); a repository is written as pkg:generic"},
		{"https://github.com/acme/widgets.git", "only dereferenced as a project interchange file"},
		{"git@github.com:acme/widgets.git@1.0.0", "write git+ssh://git@github.com/acme/widgets.git@1.0.0 or another of"},
		{"example.org:acme/widgets.git", "write git+ssh://example.org/acme/widgets or another of"},
		{"ssh://git@github.com/acme/widgets@1.0.0", "unsupported URL scheme; a repository is written as pkg:generic"},
		{"file:///srv/git/widgets@1.0.0", "a repository on this machine is git+file://" + srvGitWidgets + "@1.0.0"},
		{"/srv/git/widgets@1.0.0", "a repository on this machine is git+file://" + srvGitWidgets + "@1.0.0"},
		{"pkg:git//srv/git/widgets@1.0.0", "a repository on this machine is git+file://" + srvGitWidgets + "@1.0.0"},
		{"git+https://user:pw@github.com/acme/widgets", "a password in the host is not supported (ECMA-427 Annex B.3.5)"},
		{"git+ssh://git:pw@github.com/acme/widgets", "a password in the host is not supported (ECMA-427 Annex B.3.5)"},
		{"git+ssh://git:@github.com/acme/widgets", "a password in the host is not supported"},
		{"pkg:generic/widgets?vcs_url=hg%2Bssh:%2F%2Fhg:pw%40hg.example.org%2Fwidgets", "a password in the host is not supported"},
		{"git+https://git@github.com/acme/widgets@1.0.0", "a user name in the host is accepted only on git+ssh, where it is the SSH login; on git+https it would be a credential"},
		{"pkg:git/git@github.com/acme/widgets@1.0.0", "a user name in the host is accepted only on git+ssh, where it is the SSH login; on git+https it would be a credential"},
		{"git://git@github.com/acme/widgets", "on git+git it would be a credential"},
		{"hg+https://hg@hg.example.org/widgets", "accepted only on hg+ssh"},
		{"svn://svn@svn.example.org/widgets", "accepted only on svn+ssh"},
		{"git+file://git@/srv/git/widgets", "accepted only on git+ssh"},
		{"git+ssh://-oProxyCommand=x@github.com/acme/widgets", "starts with \"-\" and would be read as an option by ssh"},
		{"git+ssh://git@github.com", "repository URL has no path"},
		{"hg://hg.example.org/repo", "hg+hg is not a vcs_url scheme for hg (ECMA-427 Annex B.3.5 allows hg+http, hg+https, hg+static-http, hg+ssh, hg+file)"},
		{"git+ftp://host/repo", "git+ftp is not a vcs_url scheme for git"},
		{"bzr+https://host/repo", "version-control system \"bzr\" is not supported; supported: git, svn, hg"},
		{"pkg:generic/widgets", "pkg:generic needs a vcs_url qualifier"},
		{"pkg:generic/other?vcs_url=git%2Bhttps:%2F%2Fgithub.com%2Facme%2Fwidgets", "purl name \"other\" must be the repository's last path segment \"widgets\""},
		{"pkg:generic/widgets?vcs_url=git%2Bhttps:%2F%2Fgithub.com%2Facme%2Fwidgets&arch=x", "qualifier \"arch\" is not supported"},
		{"pkg:generic/widgets@1.0.0?vcs_url=git%2Bhttps:%2F%2Fgithub.com%2Facme%2Fwidgets%402.0.0", "version 1.0.0 in the purl and 2.0.0 in vcs_url disagree"},
		{"pkg:git/codeberg.org/forgejo/forgejo@a72d2c07cfca03b55371089de6aa230d8c951fa0#options", "looks like a commit id"},
		{"git+https://github.com/acme/widgets@latest", "\"latest\" after @ is not a semantic version"},
		{"https://example.org/x.kpar#sub", "a .kpar cannot have a #<subpath>"},
		{"https://user:pw@example.org/x.kpar", "credentials in an http(s) IRI are not supported; add a netrc entry for example.org"},
		{"pkg:generic/widgets?vcs_url=git%2Bhttps:%2F%2Fgithub.com%2Facme%2F..%2Fwidgets", "must not contain \"..\""},
	} {
		_, err := Parse(tc.in)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v\n want %q", tc.in, err, tc.want)
		}
	}
}

// A user name before the host of a <vcs>+ssh location is the SSH login: part of
// the identity, percent-encoded with the rest of vcs_url, and of the clone address.
func TestSSHUserName(t *testing.T) {
	for _, tc := range []struct{ in, user, canonical, identity, source string }{
		{"git+ssh://git@github.com/acme/widgets@1.0.0", "git",
			"pkg:generic/widgets?vcs_url=git%2Bssh:%2F%2Fgit%40github.com%2Facme%2Fwidgets%401.0.0",
			"pkg:generic/widgets?vcs_url=git%2Bssh:%2F%2Fgit%40github.com%2Facme%2Fwidgets",
			"ssh://git@github.com/acme/widgets"},
		{"git+ssh://git@GitHub.com/acme/widgets", "git",
			"pkg:generic/widgets?vcs_url=git%2Bssh:%2F%2Fgit%40github.com%2Facme%2Fwidgets",
			"pkg:generic/widgets?vcs_url=git%2Bssh:%2F%2Fgit%40github.com%2Facme%2Fwidgets",
			"ssh://git@github.com/acme/widgets"},
		{"pkg:generic/widgets@1.0.0?vcs_url=git%2Bssh:%2F%2Fgit%40github.com%2Facme%2Fwidgets", "git",
			"pkg:generic/widgets?vcs_url=git%2Bssh:%2F%2Fgit%40github.com%2Facme%2Fwidgets%401.0.0",
			"pkg:generic/widgets?vcs_url=git%2Bssh:%2F%2Fgit%40github.com%2Facme%2Fwidgets",
			"ssh://git@github.com/acme/widgets"},
		{"git+ssh://Deploy@git.example.org:2222/team/repo.git@1.2.0#packages/core", "Deploy",
			"pkg:generic/repo?vcs_url=git%2Bssh:%2F%2FDeploy%40git.example.org:2222%2Fteam%2Frepo.git%401.2.0%23packages%2Fcore",
			"pkg:generic/repo?vcs_url=git%2Bssh:%2F%2FDeploy%40git.example.org:2222%2Fteam%2Frepo.git%23packages%2Fcore",
			"ssh://Deploy@git.example.org:2222/team/repo.git"},
		{"hg+ssh://hg@hg.example.org/models/rover@2.0.0", "hg",
			"pkg:generic/rover?vcs_url=hg%2Bssh:%2F%2Fhg%40hg.example.org%2Fmodels%2Frover%402.0.0",
			"pkg:generic/rover?vcs_url=hg%2Bssh:%2F%2Fhg%40hg.example.org%2Fmodels%2Frover",
			"ssh://hg@hg.example.org/models/rover"},
		{"svn+ssh://svn@svn.example.org/repos/pkg", "svn",
			"pkg:generic/pkg?vcs_url=svn%2Bssh:%2F%2Fsvn%40svn.example.org%2Frepos%2Fpkg",
			"pkg:generic/pkg?vcs_url=svn%2Bssh:%2F%2Fsvn%40svn.example.org%2Frepos%2Fpkg",
			"svn+ssh://svn@svn.example.org/repos/pkg"},
		// A user name holding a reserved character stays escaped as userinfo inside vcs_url.
		{"git+ssh://first.last%40corp@git.example.org/team/repo", "first.last@corp",
			"pkg:generic/repo?vcs_url=git%2Bssh:%2F%2Ffirst.last%2540corp%40git.example.org%2Fteam%2Frepo",
			"pkg:generic/repo?vcs_url=git%2Bssh:%2F%2Ffirst.last%2540corp%40git.example.org%2Fteam%2Frepo",
			"ssh://first.last%40corp@git.example.org/team/repo"},
		// An empty user name is no user name.
		{"git+ssh://@github.com/acme/widgets@1.0.0", "",
			"pkg:generic/widgets?vcs_url=git%2Bssh:%2F%2Fgithub.com%2Facme%2Fwidgets%401.0.0",
			"pkg:generic/widgets?vcs_url=git%2Bssh:%2F%2Fgithub.com%2Facme%2Fwidgets",
			"ssh://github.com/acme/widgets"},
	} {
		o, err := Parse(tc.in)
		if err != nil {
			t.Errorf("%s: %v", tc.in, err)
			continue
		}
		if o.User != tc.user || o.Canonical() != tc.canonical || o.Identity() != tc.identity || o.Source != tc.source {
			t.Errorf("%s:\n user %q\n canonical %s\n identity  %s\n source %s", tc.in, o.User, o.Canonical(), o.Identity(), o.Source)
			continue
		}
		again, err := Parse(o.Canonical())
		if err != nil || again.Canonical() != o.Canonical() || again.Identity() != o.Identity() || again.Source != o.Source || again.User != o.User || again.Version != o.Version {
			t.Errorf("%s: canonical form does not round-trip: %v %+v", tc.in, err, again)
		}
	}
}

// The user name is part of the authority (RFC 3986 §3.2), so a location
// written with it and one written without are two identities and two keys.
func TestSSHUserNameIsIdentity(t *testing.T) {
	key := func(res string) string {
		o, err := Parse(res)
		if err != nil {
			t.Fatal(err)
		}
		return o.Key()
	}
	with, without := key("git+ssh://git@example.org/team/repo.git@1.0.0"), key("git+ssh://example.org/team/repo")
	if with == without {
		t.Fatalf("user name dropped from the key: %s", with)
	}
	if want := "pkg:generic/repo?vcs_url=git%2Bssh:%2F%2Fgit%40example.org%2Fteam%2Frepo"; with != want || key("git+ssh://git@example.org/team/repo") != want {
		t.Fatalf("key %s, want %s", with, want)
	}
	if key("git+ssh://Git@example.org/team/repo") == with {
		t.Fatal("user names are case-sensitive")
	}
}

func TestEncode(t *testing.T) {
	if got := Encode("git+https://h/p q@1.0.0#s/t"); got != "git%2Bhttps:%2F%2Fh%2Fp%20q%401.0.0%23s%2Ft" {
		t.Fatal(got)
	}
}

func TestKey(t *testing.T) {
	want := "pkg:generic/repo?vcs_url=git%2Bhttps:%2F%2Fexample.org%2Fteam%2Frepo"
	for _, res := range []string{
		"pkg:git/example.org/team/repo.git",
		"pkg:git/example.org/team/repo",
		"git+https://example.org/team/repo.git@1.0.0",
		"git+https://example.org/team/repo",
		want,
		"pkg:generic/repo?vcs_url=git%2Bhttps:%2F%2Fexample.org%2Fteam%2Frepo.git",
	} {
		o, err := Parse(res)
		if err != nil {
			t.Fatal(err)
		}
		if o.Key() != want {
			t.Errorf("%s: key %s, want %s", res, o.Key(), want)
		}
	}
	o, _ := Parse("https://example.org/x.kpar")
	if o.Key() != o.Identity() {
		t.Errorf("archive key %s != identity %s", o.Key(), o.Identity())
	}
}

// A drive letter is only a drive on Windows; elsewhere /C:/dir is a directory named C:.
func TestDriveLetterFileURIs(t *testing.T) {
	defer func(w bool) { windowsPaths = w }(windowsPaths)
	for _, tc := range []struct {
		windows       bool
		in, path, src string
	}{
		{true, "git+file:///C:/srv/widgets@1.0.0", "C:/srv/widgets", ""},
		{true, "git+file://C:/srv/widgets@1.0.0", "C:/srv/widgets", ""},
		{true, "svn+file:///C:/srv/widgets@1.0.0", "C:/srv/widgets", "file:///C:/srv/widgets"},
		{false, "git+file:///C:/srv/widgets@1.0.0", "/C:/srv/widgets", ""},
		{false, "svn+file:///C:/srv/widgets@1.0.0", "/C:/srv/widgets", "file:///C:/srv/widgets"},
	} {
		if tc.src == "" { // git: the path in this machine's separators, whichever machine is simulated
			tc.src = filepath.FromSlash(tc.path)
		}
		windowsPaths = tc.windows
		o, err := Parse(tc.in)
		if err != nil {
			t.Errorf("windows=%v %s: %v", tc.windows, tc.in, err)
			continue
		}
		if o.Path != tc.path || o.Source != tc.src {
			t.Errorf("windows=%v %s: path %q source %q, want %q %q", tc.windows, tc.in, o.Path, o.Source, tc.path, tc.src)
		}
		if o.Version != "1.0.0" {
			t.Errorf("windows=%v %s: version %q, want 1.0.0", tc.windows, tc.in, o.Version)
		}
		if _, err := Parse(o.Canonical()); err != nil {
			t.Errorf("windows=%v canonical %s: %v", tc.windows, o.Canonical(), err)
		}
	}
	// Backslashes after "://" leave no "/" to end an authority: the @ is still the version's.
	windowsPaths = true
	if o, err := Parse(`git+file://C:\srv\widgets@1.0.0`); err != nil || o.Version != "1.0.0" || strings.Contains(o.Source, "@") {
		t.Errorf(`git+file://C:\srv\widgets@1.0.0: %v %+v`, err, o)
	}
	windowsPaths = false
	if _, err := Parse("file://C:/tools.kpar"); err == nil {
		t.Error("file://C:/… must be refused where C: is a host, not a drive")
	}
	if o, err := Parse("file:///C:/tools.kpar"); err != nil || o.Source != "/C:/tools.kpar" {
		t.Errorf("file:///C:/tools.kpar: %v %+v", err, o)
	}
}

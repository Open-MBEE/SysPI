// SPDX-License-Identifier: Apache-2.0

package origin

import (
	"strings"
	"testing"
)

func TestParseCanonical(t *testing.T) {
	cases := []struct {
		in                  string
		kind                Kind
		vcs, host, path     string
		sub, ver, source    string
		identity, canonical string
	}{
		{"pkg:git/github.com/acme/widgets@1.0.0", VCS, "git", "github.com", "acme/widgets", "", "1.0.0", "https://github.com/acme/widgets.git", "pkg:git/github.com/acme/widgets", "pkg:git/github.com/acme/widgets@1.0.0"},
		{"pkg:git/github.com/acme/widgets.git", VCS, "git", "github.com", "acme/widgets", "", "", "https://github.com/acme/widgets.git", "pkg:git/github.com/acme/widgets", "pkg:git/github.com/acme/widgets"},
		{"pkg:git/git.example.org/group/sub/repo@2.1.0#packages/core", VCS, "git", "git.example.org", "group/sub/repo", "packages/core", "2.1.0", "https://git.example.org/group/sub/repo.git", "pkg:git/git.example.org/group/sub/repo#packages/core", "pkg:git/git.example.org/group/sub/repo@2.1.0#packages/core"},
		{"pkg:svn/svn.example.org/repos/widgets@1.0.0", VCS, "svn", "svn.example.org", "repos/widgets", "", "1.0.0", "https://svn.example.org/repos/widgets", "pkg:svn/svn.example.org/repos/widgets", "pkg:svn/svn.example.org/repos/widgets@1.0.0"},
		{"pkg:hg/hg.example.org/widgets", VCS, "hg", "hg.example.org", "widgets", "", "", "https://hg.example.org/widgets", "pkg:hg/hg.example.org/widgets", "pkg:hg/hg.example.org/widgets"},
		{"pkg:git//srv/git/widgets.git@1.0.0", VCS, "git", "", "/srv/git/widgets.git", "", "1.0.0", "/srv/git/widgets.git", "pkg:git//srv/git/widgets.git", "pkg:git//srv/git/widgets.git@1.0.0"},
		{"pkg:svn//srv/svn/widgets@1.0.0", VCS, "svn", "", "/srv/svn/widgets", "", "1.0.0", "file:///srv/svn/widgets", "pkg:svn//srv/svn/widgets", "pkg:svn//srv/svn/widgets@1.0.0"},
		{"pkg:hg//srv/hg/widgets@1.0.0#core", VCS, "hg", "", "/srv/hg/widgets", "core", "1.0.0", "/srv/hg/widgets", "pkg:hg//srv/hg/widgets#core", "pkg:hg//srv/hg/widgets@1.0.0#core"},
		{"https://github.com/acme/widgets.git@1.0.0", VCS, "git", "github.com", "acme/widgets", "", "1.0.0", "https://github.com/acme/widgets.git", "pkg:git/github.com/acme/widgets", "pkg:git/github.com/acme/widgets@1.0.0"},
		{"https://GitHub.com/acme/widgets", VCS, "git", "github.com", "acme/widgets", "", "", "https://GitHub.com/acme/widgets", "pkg:git/github.com/acme/widgets", "pkg:git/github.com/acme/widgets"},
		{"ssh://git@github.com/acme/widgets.git@1.0.0", VCS, "git", "github.com", "acme/widgets", "", "1.0.0", "ssh://git@github.com/acme/widgets.git", "pkg:git/github.com/acme/widgets", "pkg:git/github.com/acme/widgets@1.0.0"},
		{"ssh://git@github.com:2222/acme/widgets.git", VCS, "git", "github.com", "acme/widgets", "", "", "ssh://git@github.com:2222/acme/widgets.git", "pkg:git/github.com/acme/widgets", "pkg:git/github.com/acme/widgets"},
		{"git@github.com:acme/widgets.git@1.0.0", VCS, "git", "github.com", "acme/widgets", "", "1.0.0", "git@github.com:acme/widgets.git", "pkg:git/github.com/acme/widgets", "pkg:git/github.com/acme/widgets@1.0.0"},
		{"git@github.com:acme/widgets.git#sub/dir", VCS, "git", "github.com", "acme/widgets", "sub/dir", "", "git@github.com:acme/widgets.git", "pkg:git/github.com/acme/widgets#sub/dir", "pkg:git/github.com/acme/widgets#sub/dir"},
		{"svn+ssh://svn.example.org/repos/widgets@1.0.0", VCS, "svn", "svn.example.org", "repos/widgets", "", "1.0.0", "svn+ssh://svn.example.org/repos/widgets", "pkg:svn/svn.example.org/repos/widgets", "pkg:svn/svn.example.org/repos/widgets@1.0.0"},
		{"file:///srv/git/widgets@1.0.0", VCS, "git", "", "/srv/git/widgets", "", "1.0.0", "/srv/git/widgets", "pkg:git//srv/git/widgets", "pkg:git//srv/git/widgets@1.0.0"},
		{"/srv/git/widgets.git@1.0.0", VCS, "git", "", "/srv/git/widgets.git", "", "1.0.0", "/srv/git/widgets.git", "pkg:git//srv/git/widgets.git", "pkg:git//srv/git/widgets.git@1.0.0"},
		{"../widgets", LocalDir, "", "", "", "", "", "../widgets", "../widgets", "../widgets"},
		{"/abs/widgets#core", LocalDir, "", "", "", "", "", "/abs/widgets/core", "/abs/widgets/core", "/abs/widgets/core"},
		{"./dist/acme-widgets_1.0.0.kpar", LocalArchive, "", "", "", "", "", "./dist/acme-widgets_1.0.0.kpar", "./dist/acme-widgets_1.0.0.kpar", "./dist/acme-widgets_1.0.0.kpar"},
		{"file:///tmp/w.kpar", LocalArchive, "", "", "", "", "", "/tmp/w.kpar", "/tmp/w.kpar", "/tmp/w.kpar"},
		{"https://example.org/dl/w.kpar", RemoteArchive, "", "", "", "", "", "https://example.org/dl/w.kpar", "https://example.org/dl/w.kpar", "https://example.org/dl/w.kpar"},
		{"https://www.omg.org/spec/KerML/20250201/Kernel-Libraries.kpar", Standard, "", "", "", "", "", "https://www.omg.org/spec/KerML/20250201/Kernel-Libraries.kpar", "https://www.omg.org/spec/KerML/20250201/Kernel-Libraries.kpar", "https://www.omg.org/spec/KerML/20250201/Kernel-Libraries.kpar"},
		{"pkg:sysand/mbse4u/sysmod@5.1.4", Index, "", "", "", "", "5.1.4", "pkg:sysand/mbse4u/sysmod", "pkg:sysand/mbse4u/sysmod", "pkg:sysand/mbse4u/sysmod@5.1.4"},
	}
	for _, tc := range cases {
		o, err := Parse(tc.in)
		if err != nil {
			t.Errorf("Parse(%q): %v", tc.in, err)
			continue
		}
		if o.Kind != tc.kind || o.VCS != tc.vcs || o.Host != tc.host || o.Path != tc.path || o.Subpath != tc.sub || o.Version != tc.ver || o.Source != tc.source {
			t.Errorf("Parse(%q) = %+v", tc.in, *o)
		}
		if o.Identity() != tc.identity || o.Canonical() != tc.canonical {
			t.Errorf("Parse(%q): identity %q canonical %q", tc.in, o.Identity(), o.Canonical())
		}
	}
}

func TestParseRefusals(t *testing.T) {
	cases := []struct{ in, want string }{
		{"pkg:github/acme/widgets@1.0.0", "pkg:git/github.com/acme/widgets"},
		{"pkg:gitlab/group/repo", "pkg:git/"},
		{"pkg:cvs/host/path", "not supported; supported: git, svn, hg"},
		{"pkg:npm/left-pad", "not supported"},
		{"pkg:git/github.com", "expected pkg:git/<host>/<path>"},
		{"pkg:git/github.com/acme/widgets@latest", "not a semantic version"},
		{"pkg:git/github.com/acme/widgets@", "empty version"},
		{"pkg:git/github.com/acme/../etc", `".."`},
		{"pkg:sysand/mbse4u", "expected pkg:sysand/<publisher>/<name>"},
		{"https://user:pw@github.com/acme/widgets", "credentials"},
		{"ftp://example.org/w.kpar", "unsupported URL scheme"},
		{"file://host/share/w.kpar", "file:///path"},
		{"", "empty resource"},
		{"./w.kpar#core", "cannot have a #<subpath>"},
	}
	for _, tc := range cases {
		o, err := Parse(tc.in)
		if err == nil {
			t.Errorf("Parse(%q) accepted: %+v", tc.in, *o)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("Parse(%q) error %q lacks %q", tc.in, err, tc.want)
		}
	}
}

// SPDX-License-Identifier: Apache-2.0

package origin

import (
	"strings"
	"testing"
)

// The four examples of purl-spec types/git-definition.json (ECMA-427): the
// syntax is ours, only a commit id as version is refused, saying why.
func TestPurlGitExamples(t *testing.T) {
	for _, tc := range []struct{ in, id, want string }{
		{"pkg:git/projects.blender.org/blender/blender.git", "pkg:git/projects.blender.org/blender/blender", ""},
		{"pkg:git/gitlab.gnome.org/GNOME/adwaita-fonts", "pkg:git/gitlab.gnome.org/GNOME/adwaita-fonts", ""},
		{"pkg:git/cygwin.com/cgit/newlib-cygwin@6d049c54c3314da31d9ffac133a6a2f2dfecaac2", "", "looks like a commit id; a Package-URL git version may be any git reference, but SysPI resolves only a tag naming a semantic version"},
		{"pkg:git/codeberg.org/forgejo/forgejo@a72d2c07cfca03b55371089de6aa230d8c951fa0#options/locale_readme.md", "", "looks like a commit id"},
		{"pkg:git/codeberg.org/forgejo/forgejo@1.2.0#options/locale_readme.md", "pkg:git/codeberg.org/forgejo/forgejo#options/locale_readme.md", ""},
	} {
		o, err := Parse(tc.in)
		if tc.want != "" {
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("%s: %v, want %q", tc.in, err, tc.want)
			}
			continue
		}
		if err != nil || o.Identity() != tc.id {
			t.Errorf("%s: %v %v", tc.in, o, err)
		}
	}
}

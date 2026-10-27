// SPDX-License-Identifier: Apache-2.0

package version

import "testing"

func TestParseAndTags(t *testing.T) {
	for _, s := range []string{"1.0.0", "0.1.2-rc.1", "10.20.30+build.5"} {
		if _, err := Parse(s); err != nil {
			t.Errorf("Parse(%q): %v", s, err)
		}
	}
	for _, s := range []string{"1", "1.0", "v1.0.0", "01.0.0", "1.0.0.0", "latest", ""} {
		if _, err := Parse(s); err == nil {
			t.Errorf("Parse(%q) accepted", s)
		}
	}
	v, pref, err := ParseTag("v1.2.3")
	if err != nil || !pref || v.String() != "1.2.3" {
		t.Fatalf("ParseTag(v1.2.3) = %v %v %v", v, pref, err)
	}
	v, pref, err = ParseTag("1.2.3")
	if err != nil || pref || v.String() != "1.2.3" {
		t.Fatalf("ParseTag(1.2.3) = %v %v %v", v, pref, err)
	}
	if _, _, err := ParseTag("release-1.2.3"); err == nil {
		t.Fatal("ParseTag(release-1.2.3) accepted")
	}
}

func mustParse(t *testing.T, ss ...string) []Version {
	t.Helper()
	var vs []Version
	for _, s := range ss {
		v, err := Parse(s)
		if err != nil {
			t.Fatal(err)
		}
		vs = append(vs, v)
	}
	return vs
}

func TestConstraints(t *testing.T) {
	vs := mustParse(t, "0.9.0", "1.0.0", "1.2.0", "1.2.9", "1.3.0", "2.0.0", "2.1.0-beta.1")
	cases := []struct {
		c     string
		want  string
		exact bool
	}{
		{"1.2.0", "1.2.0", true},
		{"=1.2.0", "1.2.0", true},
		{"v1.2.0", "1.2.0", true},
		{"^1.2.0", "1.3.0", false},
		{"~1.2.0", "1.2.9", false},
		{">=1.0.0 <2.0.0", "1.3.0", false},
		{">=1.0.0, <2.0.0", "1.3.0", false},
		{">= 1.0.0 < 1.2.0", "1.0.0", false},
		{">1.3.0", "2.0.0", false},
		{"<=0.9.0", "0.9.0", false},
		{"^0.9.0", "0.9.0", false},
	}
	for _, tc := range cases {
		c, err := ParseConstraint(tc.c)
		if err != nil {
			t.Errorf("ParseConstraint(%q): %v", tc.c, err)
			continue
		}
		if c.IsExact() != tc.exact {
			t.Errorf("%q IsExact = %v", tc.c, c.IsExact())
		}
		got, ok := c.Latest(vs)
		if !ok || got.String() != tc.want {
			t.Errorf("%q Latest = %v %v, want %s", tc.c, got, ok, tc.want)
		}
	}
	for _, bad := range []string{"", "1.x", "*", "^1", "~1.2", ">=1", "1.0.0 || 2.0.0", "latest", "~>1.0.0"} {
		if _, err := ParseConstraint(bad); err == nil {
			t.Errorf("ParseConstraint(%q) accepted", bad)
		}
	}
	if _, ok := mustConstraint(t, "^3.0.0").Latest(vs); ok {
		t.Error("^3.0.0 matched")
	}
	if !(Constraint{}).Check(vs[0]) {
		t.Error("zero constraint rejects")
	}
	if ex := Exact(vs[1]); !ex.IsExact() || !ex.Check(vs[1]) || ex.Check(vs[2]) {
		t.Error("Exact misbehaves")
	}
}

func mustConstraint(t *testing.T, s string) Constraint {
	t.Helper()
	c, err := ParseConstraint(s)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

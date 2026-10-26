// SPDX-License-Identifier: Apache-2.0

package lock

import "testing"

func TestParseLegacyStringUsages(t *testing.T) {
	l, err := Parse([]byte(`{"lockVersion":1,"packages":[{"resource":"pkg:git/example.org/app","kind":"vcs","name":"app","version":"1.0.0","usage":["pkg:git/example.org/core@1.2.0",{"resource":"pkg:git/example.org/util","versionConstraint":"^2.0.0"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	u := l.Packages[0].Usage
	if len(u) != 2 || u[0] != (Usage{Resource: "pkg:git/example.org/core@1.2.0"}) || u[1] != (Usage{Resource: "pkg:git/example.org/util", VersionConstraint: "^2.0.0"}) {
		t.Fatalf("%+v", u)
	}
	if _, err := Parse(l.Marshal()); err != nil {
		t.Fatalf("migrated form must round-trip: %v", err)
	}
}

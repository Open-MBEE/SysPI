// SPDX-License-Identifier: Apache-2.0

package vcs

import (
	"io"
	"strings"
	"testing"

	"github.com/Open-MBEE/SysPI/internal/netrc"
)

func TestSvnAuthArgsFor(t *testing.T) {
	lookup := func(host string) (netrc.Credential, bool) {
		if host == "svn.example" {
			return netrc.Credential{Login: "alice", Password: "s3cret"}, true
		}
		return netrc.Credential{}, false
	}
	args, stdin := svnAuthArgsFor("svn://svn.example:3690/pkg", lookup, true)
	if strings.Join(args, " ") != "--username alice --password-from-stdin --no-auth-cache" {
		t.Fatalf("args %q", args)
	}
	if pw, _ := io.ReadAll(stdin); string(pw) != "s3cret" {
		t.Fatalf("stdin %q", pw)
	}
	for _, a := range args {
		if strings.Contains(a, "s3cret") {
			t.Fatal("password in argv")
		}
	}
	if a, in := svnAuthArgsFor("https://svn.example/repo", lookup, false); a != nil || in != nil {
		t.Fatal("svn without --password-from-stdin must run as before")
	}
	if a, _ := svnAuthArgsFor("svn://other.example/pkg", lookup, true); a != nil {
		t.Fatal("no netrc entry: no arguments")
	}
	if a, _ := svnAuthArgsFor("svn+ssh://svn.example/pkg", lookup, true); a != nil {
		t.Fatal("svn+ssh authenticates through ssh")
	}
	if a, _ := svnAuthArgsFor("file:///srv/svn/pkg", lookup, true); a != nil {
		t.Fatal("file repositories take no credentials")
	}
}

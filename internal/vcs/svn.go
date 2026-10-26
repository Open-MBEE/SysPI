// SPDX-License-Identifier: Apache-2.0

package vcs

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/Open-MBEE/SysPI/internal/netrc"
)

// svnSource is a Subversion repository (or project root within one) whose
// versions are the directories under <url>/tags/, named v<semver> or <semver>
// by the usual tags/ convention.
type svnSource struct{ url string }

func (s *svnSource) VCS() string { return "svn" }

func (s *svnSource) tagURL(v Version) string {
	return strings.TrimSuffix(s.url, "/") + "/tags/" + v.Tag
}

// svnCredentials is the netrc entry for the repository host, if any.
var svnCredentials = func(host string) (netrc.Credential, bool) {
	f, err := netrc.Load()
	if err != nil || f == nil {
		return netrc.Credential{}, false
	}
	return f.Lookup(host)
}

// svnPasswordFromStdin reports whether the user's svn is 1.14 or newer,
// the first release with --password-from-stdin.
var svnPasswordFromStdin = sync.OnceValue(func() bool {
	out, err := run(context.Background(), "", "svn", "--version", "--quiet")
	if err != nil {
		return false
	}
	parts := strings.SplitN(strings.TrimSpace(out), ".", 3)
	if len(parts) < 2 {
		return false
	}
	major, _ := strconv.Atoi(parts[0])
	minor, _ := strconv.Atoi(parts[1])
	return major > 1 || (major == 1 && minor >= 14)
})

// svnAuthArgs is svnAuthArgsFor the user's svn and netrc.
func (s *svnSource) svnAuthArgs() ([]string, io.Reader) {
	return svnAuthArgsFor(s.url, svnCredentials, svnPasswordFromStdin())
}

// svnAuthArgsFor adds `--username <login> --password-from-stdin
// --no-auth-cache` when the netrc has an entry for the repository host and
// svn can take the password on stdin; the password is never an argument,
// never in the environment and never cached on disk. Otherwise svn runs as
// before, on its own cached credentials.
func svnAuthArgsFor(repo string, lookup func(string) (netrc.Credential, bool), fromStdin bool) ([]string, io.Reader) {
	u, err := url.Parse(repo)
	if err != nil || u.Hostname() == "" || u.Scheme == "svn+ssh" || u.Scheme == "file" {
		return nil, nil
	}
	c, ok := lookup(u.Hostname())
	if !ok || c.Login == "" || !fromStdin {
		return nil, nil
	}
	return []string{"--username", c.Login, "--password-from-stdin", "--no-auth-cache"}, strings.NewReader(c.Password + "\n")
}

// svn runs the user's svn with the netrc credentials for the repository
// host when it has them.
func (s *svnSource) svn(ctx context.Context, args ...string) (string, error) {
	auth, stdin := s.svnAuthArgs()
	return runIn(ctx, "", stdin, "svn", append(append([]string{args[0], "--non-interactive"}, auth...), args[1:]...)...)
}

func (s *svnSource) ListVersions(ctx context.Context) ([]Version, error) {
	out, err := s.svn(ctx, "ls", "--", strings.TrimSuffix(s.url, "/")+"/tags")
	if err != nil {
		return nil, fmt.Errorf("listing %s/tags (svn packages are tagged by copying to tags/<version>): %w", strings.TrimSuffix(s.url, "/"), err)
	}
	var names []string
	for _, line := range strings.Split(out, "\n") {
		if n, ok := strings.CutSuffix(strings.TrimSpace(line), "/"); ok && n != "" {
			names = append(names, n)
		}
	}
	return versionsFromNames(names), nil
}

// Revision is the last-changed revision of the tag directory. It is local to
// the repository, not content-addressed; the lock's integrity for svn rests
// on the tree hash and this is the reproducibility hint.
func (s *svnSource) Revision(ctx context.Context, v Version) (string, error) {
	out, err := s.svn(ctx, "info", "--show-item", "last-changed-revision", "--", s.tagURL(v))
	if err != nil {
		return "", err
	}
	rev := strings.TrimSpace(out)
	if rev == "" {
		return "", fmt.Errorf("svn info for %s returned no last-changed revision", s.tagURL(v))
	}
	return rev, nil
}

// Export writes the tag directory with `svn export`, pinned at its
// last-changed revision so a later copy cannot change what is exported.
func (s *svnSource) Export(ctx context.Context, v Version, dst string) error {
	rev, err := s.Revision(ctx, v)
	if err != nil {
		return err
	}
	_, err = s.svn(ctx, "export", "--quiet", "--ignore-keywords", "--", s.tagURL(v)+"@"+rev, dst)
	return err
}

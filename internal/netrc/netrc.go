// SPDX-License-Identifier: Apache-2.0

// Package netrc supplies HTTP Basic credentials for index files and .kpar
// downloads from the user's netrc file: $NETRC when set, else ~/.netrc
// (%USERPROFILE%\_netrc on Windows). Credentials are matched by host and
// sent only to that host, so a redirect elsewhere never carries them.
package netrc

import (
	"bufio"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Credential is one machine entry.
type Credential struct{ Login, Password string }

// File is a parsed netrc: host → credential, plus the default entry.
type File struct {
	Machines map[string]Credential
	Default  *Credential
}

// Path is the netrc file consulted: $NETRC, else the home-directory file.
func Path() string {
	if p := os.Getenv("NETRC"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(home, "_netrc")
	}
	return filepath.Join(home, ".netrc")
}

// Load reads Path(); a missing file is an empty File, not an error.
func Load() (*File, error) {
	p := Path()
	if p == "" {
		return &File{Machines: map[string]Credential{}}, nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return &File{Machines: map[string]Credential{}}, nil
		}
		return nil, err
	}
	return Parse(string(data)), nil
}

// Parse reads the netrc token grammar: machine/default, login, password;
// macdef blocks are skipped.
func Parse(text string) *File {
	f := &File{Machines: map[string]Credential{}}
	sc := bufio.NewScanner(strings.NewReader(text))
	var tokens []string
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		tokens = append(tokens, strings.Fields(line)...)
	}
	var cur *Credential
	var host string
	flush := func() {
		if cur == nil {
			return
		}
		if host == "" {
			f.Default = cur
		} else {
			f.Machines[strings.ToLower(host)] = *cur
		}
		cur = nil
	}
	for i := 0; i < len(tokens); i++ {
		switch tokens[i] {
		case "machine":
			flush()
			if i+1 < len(tokens) {
				i++
				host = tokens[i]
				cur = &Credential{}
			}
		case "default":
			flush()
			host = ""
			cur = &Credential{}
		case "login", "user":
			if cur != nil && i+1 < len(tokens) {
				i++
				cur.Login = tokens[i]
			}
		case "password", "passwd":
			if cur != nil && i+1 < len(tokens) {
				i++
				cur.Password = tokens[i]
			}
		case "macdef":
			// A macro runs to the next blank line; tokens have lost blank lines,
			// so skip to the next machine/default keyword.
			for i+1 < len(tokens) && tokens[i+1] != "machine" && tokens[i+1] != "default" {
				i++
			}
		}
	}
	flush()
	return f
}

// Lookup returns the credential for a host (port ignored), falling back to
// the default entry.
func (f *File) Lookup(host string) (Credential, bool) {
	if f == nil {
		return Credential{}, false
	}
	if c, ok := f.Machines[strings.ToLower(host)]; ok {
		return c, true
	}
	if f.Default != nil {
		return *f.Default, true
	}
	return Credential{}, false
}

// Transport adds Basic credentials from the netrc to every request whose
// host has an entry and that carries no Authorization yet. Each request is
// matched on its own host, so a redirect to another host gets that host's
// credentials or none.
type Transport struct {
	Base http.RoundTripper
	File *File
}

// RoundTrip implements http.RoundTripper.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	if req.Header.Get("Authorization") == "" {
		if c, ok := t.credential(req); ok && c.Login != "" {
			req = req.Clone(req.Context())
			req.SetBasicAuth(c.Login, c.Password)
		}
	}
	return base.RoundTrip(req)
}

// credential picks the entry for a request; after a redirect to another
// origin only an explicit machine entry counts, never the default entry.
func (t *Transport) credential(req *http.Request) (Credential, bool) {
	first := req
	for first.Response != nil && first.Response.Request != nil {
		first = first.Response.Request
	}
	if first != req && (first.URL.Scheme != req.URL.Scheme || first.URL.Host != req.URL.Host) {
		c, ok := t.File.Machines[strings.ToLower(req.URL.Hostname())]
		return c, ok
	}
	return t.File.Lookup(req.URL.Hostname())
}

// RedactURL drops the user info and the query of a URL for diagnostics,
// so no token or password reaches output or logs.
func RedactURL(s string) string {
	u, err := url.Parse(s)
	if err != nil {
		return s
	}
	u.User = nil
	if u.RawQuery != "" || u.ForceQuery {
		u.RawQuery, u.ForceQuery = "", false
		return u.String() + "?..."
	}
	return u.String()
}

// Client is an HTTP client using the user's netrc; proxy settings come from
// the environment through http.DefaultTransport.
func Client() (*http.Client, error) {
	f, err := Load()
	if err != nil {
		return nil, err
	}
	return &http.Client{Transport: &Transport{File: f}}, nil
}

// Hint is the actionable line for an HTTP 401 or 403: a netrc entry for
// the host.
func Hint(status int, host string) string {
	if status != http.StatusUnauthorized && status != http.StatusForbidden {
		return ""
	}
	return fmt.Sprintf("\n  hint: the server wants credentials; add `machine %s login <user> password <secret>` to your netrc ($NETRC, else ~/.netrc, %%USERPROFILE%%\\_netrc on Windows)", host)
}

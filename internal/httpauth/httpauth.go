// SPDX-License-Identifier: Apache-2.0

// Package httpauth supplies credentials to syspi's own HTTP requests
// (index documents and .kpar downloads). Precedence per request URL:
// a matching SYSAND_CRED_<LABEL> environment credential (sysand's
// grammar), then a netrc entry for the host, then anonymous. Nothing here
// logs or stores a secret.
package httpauth

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/Open-MBEE/SysPI/internal/netrc"
)

// Prefix is the namespace of sysand's environment credentials:
// SYSAND_CRED_<LABEL> holds a URL glob, SYSAND_CRED_<LABEL>_BEARER_TOKEN a
// bearer token, SYSAND_CRED_<LABEL>_BASIC_USER and _BASIC_PASS a Basic pair.
const Prefix = "SYSAND_CRED_"

// Credential is one labelled environment credential.
type Credential struct {
	Label   string
	Pattern string
	Token   string // bearer
	User    string // basic
	Pass    string
	re      *regexp.Regexp
}

func (c *Credential) apply(req *http.Request) {
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
		return
	}
	req.SetBasicAuth(c.User, c.Pass)
}

// Policy is the ordered set of environment credentials.
type Policy struct {
	Creds []*Credential
}

// FromEnv reads and validates every SYSAND_CRED_* variable of environ (as
// os.Environ gives it). Malformed groups are an error naming the variable
// to fix, in sysand's words.
func FromEnv(environ []string) (*Policy, error) {
	patterns, users, passes, tokens := map[string]string{}, map[string]string{}, map[string]string{}, map[string]string{}
	var missing []string
	for _, kv := range environ {
		name, value, _ := strings.Cut(kv, "=")
		if name != "SYSAND_CRED" && !strings.HasPrefix(name, Prefix) {
			continue
		}
		rest := strings.TrimPrefix(name, Prefix)
		if name == "SYSAND_CRED" || rest == "" {
			missing = append(missing, name)
			continue
		}
		grouped := false
		for suffix, m := range map[string]map[string]string{"_BASIC_USER": users, "_BASIC_PASS": passes, "_BEARER_TOKEN": tokens} {
			if rest == suffix[1:] {
				missing = append(missing, name)
				grouped = true
				break
			}
			if stem, ok := strings.CutSuffix(rest, suffix); ok {
				if stem == "" {
					missing = append(missing, name)
				} else {
					m[stem] = value
				}
				grouped = true
				break
			}
		}
		if !grouped {
			patterns[rest] = value
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("%s has no label; env credentials need one, as in SYSAND_CRED_MYINDEX\n  with SYSAND_CRED_MYINDEX_BASIC_USER/SYSAND_CRED_MYINDEX_BASIC_PASS or\n  SYSAND_CRED_MYINDEX_BEARER_TOKEN", missing[0])
	}
	stems := map[string]bool{}
	for _, m := range []map[string]string{patterns, users, passes, tokens} {
		for k := range m {
			stems[k] = true
		}
	}
	labels := make([]string, 0, len(stems))
	for k := range stems {
		labels = append(labels, k)
	}
	sort.Strings(labels)
	p := &Policy{}
	for _, k := range labels {
		pattern, ok := patterns[k]
		if !ok {
			return nil, fmt.Errorf("please specify URL pattern SYSAND_CRED_%s for credential", k)
		}
		_, hasUser := users[k]
		_, hasPass := passes[k]
		if hasUser != hasPass {
			return nil, fmt.Errorf("please specify both (or neither) of SYSAND_CRED_%s_BASIC_USER and SYSAND_CRED_%s_BASIC_PASS", k, k)
		}
		token, hasToken := tokens[k]
		switch {
		case hasUser && hasToken:
			return nil, fmt.Errorf("please specify only one of HTTP basic (SYSAND_CRED_%s_BASIC_USER/SYSAND_CRED_%s_BASIC_PASS) or bearer token (SYSAND_CRED_%s_BEARER_TOKEN) for credential", k, k, k)
		case !hasUser && !hasToken:
			return nil, fmt.Errorf("please specify either HTTP basic (SYSAND_CRED_%s_BASIC_USER/SYSAND_CRED_%s_BASIC_PASS) or bearer token (SYSAND_CRED_%s_BEARER_TOKEN) for credential", k, k, k)
		}
		re, err := compileGlob(pattern)
		if err != nil {
			return nil, fmt.Errorf("SYSAND_CRED_%s: invalid URL glob `%s`: %v", k, pattern, err)
		}
		p.Creds = append(p.Creds, &Credential{Label: k, Pattern: pattern, Token: token, User: users[k], Pass: passes[k], re: re})
	}
	return p, nil
}

// compileGlob turns a URL glob into a regular expression with a literal
// separator: `*` and `?` never cross `/`, `**` does, `[...]` is a class,
// `{a,b}` an alternation, and `\` escapes the next character.
func compileGlob(glob string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	depth := 0
	for i := 0; i < len(glob); i++ {
		c := glob[i]
		switch c {
		case '*':
			if i+1 < len(glob) && glob[i+1] == '*' {
				i++
				if i+1 < len(glob) && glob[i+1] == '/' {
					i++
					b.WriteString("(?:.*/)?")
				} else {
					b.WriteString(".*")
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		case '[':
			j := strings.IndexByte(glob[i+1:], ']')
			if j < 0 {
				return nil, fmt.Errorf("unclosed `[`")
			}
			class := glob[i+1 : i+1+j]
			if strings.HasPrefix(class, "!") {
				class = "^" + class[1:]
			}
			b.WriteString("[" + strings.ReplaceAll(class, "\\", "\\\\") + "]")
			i += j + 1
		case '{':
			depth++
			b.WriteString("(?:")
		case '}':
			if depth == 0 {
				return nil, fmt.Errorf("unbalanced `}`")
			}
			depth--
			b.WriteString(")")
		case ',':
			if depth > 0 {
				b.WriteString("|")
			} else {
				b.WriteString(",")
			}
		case '\\':
			if i+1 < len(glob) {
				i++
				b.WriteString(regexp.QuoteMeta(string(glob[i])))
			}
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	if depth != 0 {
		return nil, fmt.Errorf("unclosed `{`")
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

// Match lists the credentials whose glob matches the URL, in label order.
func (p *Policy) Match(url string) []*Credential {
	if p == nil {
		return nil
	}
	var out []*Credential
	for _, c := range p.Creds {
		if c.re.MatchString(url) {
			out = append(out, c)
		}
	}
	return out
}

// Transport applies the credential policy to each request. A URL matched
// by SYSAND_CRED_* globs is first requested anonymously and, on a 4xx
// other than 429, retried with each matching credential in order until
// one answers non-4xx (an ambiguous match is reported through Warn). An
// unmatched URL carries the netrc entry for its host, if any. On a
// redirect, either credential follows only to the same scheme, host and
// port as the first request.
type Transport struct {
	Base  http.RoundTripper
	Env   *Policy
	Netrc *netrc.File
	Warn  func(string)
}

func (t *Transport) base() http.RoundTripper {
	if t.Base != nil {
		return t.Base
	}
	return http.DefaultTransport
}

// origin is scheme://host:port, the unit a credential is scoped to.
func origin(u *url.URL) string {
	port := u.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[u.Scheme]
	}
	return u.Scheme + "://" + u.Hostname() + ":" + port
}

// sameOrigin reports whether req is the first request of its redirect
// chain or a redirect to the same scheme, host and port as that first
// request; a credential never follows a redirect anywhere else (another
// host, another port, or an https → http downgrade).
func sameOrigin(req *http.Request) bool {
	first := req
	for first.Response != nil && first.Response.Request != nil {
		first = first.Response.Request
	}
	return origin(first.URL) == origin(req.URL)
}

// RedactURL hides the query of a URL in diagnostics: index locations and
// archive URLs may carry access tokens there.
func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if u.User != nil {
		u.User = url.User("<redacted>")
	}
	if u.RawQuery != "" {
		u.RawQuery = "<redacted>"
	}
	return u.String()
}

func clientError(resp *http.Response) bool {
	return resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests
}

// RoundTrip implements http.RoundTripper.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get("Authorization") != "" {
		return t.base().RoundTrip(req)
	}
	if !sameOrigin(req) {
		return t.base().RoundTrip(req)
	}
	matched := t.Env.Match(req.URL.String())
	if len(matched) == 0 {
		if t.Netrc != nil {
			if c, ok := t.Netrc.Lookup(req.URL.Hostname()); ok && c.Login != "" {
				req = req.Clone(req.Context())
				req.SetBasicAuth(c.Login, c.Password)
			}
		}
		return t.base().RoundTrip(req)
	}
	if len(matched) > 1 && t.Warn != nil {
		var pats []string
		for _, c := range matched {
			pats = append(pats, Prefix+c.Label+"="+c.Pattern)
		}
		t.Warn(fmt.Sprintf("URL %s matches multiple authentication URL globs: %s", RedactURL(req.URL.String()), strings.Join(pats, ", ")))
	}
	first, err := t.base().RoundTrip(req)
	if err != nil || !clientError(first) {
		return first, err
	}
	var withCred *http.Response
	for _, c := range matched {
		r := req.Clone(req.Context())
		c.apply(r)
		first.Body.Close()
		resp, err := t.base().RoundTrip(r)
		if err != nil {
			return nil, err
		}
		if !clientError(resp) {
			if withCred != nil {
				withCred.Body.Close()
			}
			return resp, nil
		}
		if withCred == nil {
			withCred = resp
		} else {
			resp.Body.Close()
		}
	}
	return withCred, nil
}

// Hint is the actionable line for an HTTP 401 or 403; the URL is shown
// with its query redacted.
func Hint(status int, rawURL, host string) string {
	if status != http.StatusUnauthorized && status != http.StatusForbidden {
		return ""
	}
	return fmt.Sprintf("\n  hint: the server wants credentials; set SYSAND_CRED_<LABEL>=<URL glob matching %s> with SYSAND_CRED_<LABEL>_BEARER_TOKEN or _BASIC_USER/_BASIC_PASS, or add `machine %s login <user> password <secret>` to your netrc ($NETRC, else ~/.netrc)", RedactURL(rawURL), host)
}

// Client is an HTTP client carrying the environment credentials of this
// process and the user's netrc; warn receives ambiguity notices.
func Client(warn func(string)) (*http.Client, error) {
	env, err := FromEnv(os.Environ())
	if err != nil {
		return nil, err
	}
	nf, err := netrc.Load()
	if err != nil {
		return nil, fmt.Errorf("reading %s: %v", netrc.Path(), err)
	}
	return &http.Client{Transport: &Transport{Env: env, Netrc: nf, Warn: warn}}, nil
}

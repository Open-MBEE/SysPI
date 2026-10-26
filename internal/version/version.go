// SPDX-License-Identifier: Apache-2.0

// Package version parses semantic versions, the tag names that carry them and
// the version constraints a project may place on a usage.
//
// Tags are v<semver> (preferred) or bare <semver>. Constraints are exact
// versions, caret and tilde ranges and comparison ranges (>=1.2.0 <2.0.0),
// the forms the KerML project-interchange clause recommends; the looser npm
// forms (1.x, *, ||) are refused so that a lock never depends on them.
package version

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
)

// Version is a parsed semantic version.
type Version struct {
	v *semver.Version
}

var strict = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

// Parse parses a strict semantic version (MAJOR.MINOR.PATCH with optional
// pre-release and build metadata).
func Parse(s string) (Version, error) {
	if !strict.MatchString(s) {
		return Version{}, fmt.Errorf("%q is not a semantic version (expected MAJOR.MINOR.PATCH)", s)
	}
	v, err := semver.StrictNewVersion(s)
	if err != nil {
		return Version{}, fmt.Errorf("%q is not a semantic version: %v", s, err)
	}
	return Version{v: v}, nil
}

// ParseTag parses a tag or directory name carrying a version: v<semver> or
// bare <semver>. The second result reports whether it had the v prefix.
func ParseTag(tag string) (Version, bool, error) {
	if rest, ok := strings.CutPrefix(tag, "v"); ok {
		v, err := Parse(rest)
		return v, true, err
	}
	v, err := Parse(tag)
	return v, false, err
}

// String is the canonical version text without a v prefix.
func (v Version) String() string {
	if v.v == nil {
		return ""
	}
	return v.v.String()
}

// Equal reports whether the two versions are the same release (build metadata ignored).
func (v Version) Equal(o Version) bool { return v.v.Equal(o.v) }

// Less orders versions by semver precedence.
func (v Version) Less(o Version) bool { return v.v.LessThan(o.v) }

// Compare returns -1, 0 or 1.
func (v Version) Compare(o Version) int { return v.v.Compare(o.v) }

// Sort orders versions ascending by precedence.
func Sort(vs []Version) {
	sort.Slice(vs, func(i, j int) bool { return vs[i].Less(vs[j]) })
}

// Constraint is a parsed version constraint.
type Constraint struct {
	text string
	c    *semver.Constraints
}

var (
	versionText = `v?(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?`
	exactRe     = regexp.MustCompile(`^=?` + versionText + `$`)
	rangeRe     = regexp.MustCompile(`^(\^|~|>=|<=|>|<|=)?\s*` + versionText + `$`)
)

// ParseConstraint parses exact (1.2.3, =1.2.3), caret (^1.2.3), tilde (~1.2.3)
// and comparison-range (>=1.2.0 <2.0.0, comma or space separated) constraints.
func ParseConstraint(text string) (Constraint, error) {
	t := strings.TrimSpace(text)
	if t == "" {
		return Constraint{}, fmt.Errorf("empty version constraint")
	}
	if strings.Contains(t, "||") {
		return Constraint{}, fmt.Errorf("version constraint %q: alternatives (||) are not supported", text)
	}
	parts := strings.FieldsFunc(t, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' })
	// Re-attach an operator that was separated from its version by a space.
	var terms []string
	for i := 0; i < len(parts); i++ {
		p := parts[i]
		if (p == "^" || p == "~" || p == ">=" || p == "<=" || p == ">" || p == "<" || p == "=") && i+1 < len(parts) {
			p += parts[i+1]
			i++
		}
		if !rangeRe.MatchString(p) {
			return Constraint{}, fmt.Errorf("version constraint %q: %q is not an exact version, ^ or ~ range, or comparison against a full MAJOR.MINOR.PATCH version", text, p)
		}
		terms = append(terms, p)
	}
	c, err := semver.NewConstraint(strings.Join(terms, ", "))
	if err != nil {
		return Constraint{}, fmt.Errorf("version constraint %q: %v", text, err)
	}
	return Constraint{text: t, c: c}, nil
}

// Exact returns the constraint satisfied by exactly v.
func Exact(v Version) Constraint {
	c, _ := semver.NewConstraint("=" + v.String())
	return Constraint{text: v.String(), c: c}
}

// IsExact reports whether the constraint names a single version.
func (c Constraint) IsExact() bool { return exactRe.MatchString(c.text) }

// IsZero reports whether the constraint is unset (matches everything).
func (c Constraint) IsZero() bool { return c.c == nil }

// String is the constraint as written (trimmed).
func (c Constraint) String() string { return c.text }

// Check reports whether v satisfies the constraint. The zero constraint
// matches every version, including pre-releases.
func (c Constraint) Check(v Version) bool {
	if c.c == nil {
		return true
	}
	return c.c.Check(v.v)
}

// Latest returns the highest version in vs satisfying c and true, or false
// when none does.
func (c Constraint) Latest(vs []Version) (Version, bool) {
	var best Version
	found := false
	for _, v := range vs {
		if !c.Check(v) {
			continue
		}
		if !found || best.Less(v) {
			best, found = v, true
		}
	}
	return best, found
}

// SPDX-License-Identifier: Apache-2.0

package project

import (
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

type tokenKind int

const (
	tokSymbol  tokenKind = iota
	tokWord              // a basic name or a reserved keyword
	tokQuoted            // an unrestricted name, escapes resolved
	tokComment           // a regular comment, the body of an annotation
)

type token struct {
	kind tokenKind
	text string
}

// notation is what the scanner knows of one textual notation: its reserved
// keywords (KerML 8.2.2.6, SysML 8.2.2.1.2) and the roles a few of them play.
type notation struct {
	reserved map[string]bool
	// declaring keywords may stand before the Identification of a declaration.
	declaring map[string]bool
	// leading keywords are followed by a reference unless a declaring keyword comes next.
	leading map[string]bool
}

func words(lists ...string) map[string]bool {
	set := map[string]bool{}
	for _, list := range lists {
		for _, w := range strings.Fields(list) {
			set[w] = true
		}
	}
	return set
}

var kermlNotation = notation{
	reserved: words(`about abstract alias all and as assoc behavior binding bool by chains class
		classifier comment composite conjugate conjugates conjugation connector const
		crosses datatype default dependency derived differences disjoining disjoint doc
		else end expr false feature featured featuring filter first flow for from
		function hastype if implies import in inout interaction intersects inv inverse
		inverting istype language library locale member meta metaclass metadata
		multiplicity namespace nonunique not null of or ordered out package portion
		predicate private protected public redefines redefinition references rep return
		specialization specializes standard step struct subclassifier subset subsets
		subtype succession then to true type typed typing unions var xor`),
	declaring: words(`public private protected abstract standard library package namespace
		type classifier class struct assoc datatype behavior function predicate interaction
		metaclass feature step expr bool inv true false connector binding succession flow
		in out inout derived composite portion var const end all multiplicity metadata
		specialization conjugation disjoining inverting featuring dependency comment doc rep`),
	leading: words(),
}

var sysmlNotation = notation{
	reserved: words(`about abstract accept action actor after alias all allocate allocation analysis
		and as assert assign assume at attribute bind binding by calc case comment
		concern connect connection constant constraint crosses decide def default
		defined dependency derived do doc else end entry enum event exhibit exit expose
		false filter first flow for fork frame from hastype if implies import in include
		individual inout interface istype item join language library locale loop merge
		message meta metadata new nonunique not null objective occurrence of or ordered
		out package parallel part perform port private protected public redefines ref
		references render rendering rep require requirement return satisfy send snapshot
		specializes stakeholder standard state subject subsets succession terminate then
		timeslice to transition true until use variant variation verification verify via
		view viewpoint when while xor`),
	declaring: words(`public private protected abstract variation standard library package def
		attribute enum occurrence individual item part connection flow interface port action
		calc state constraint requirement concern case analysis verification use view
		viewpoint rendering metadata allocation message succession binding in out inout
		derived constant ref end snapshot timeslice dependency comment doc rep`),
	leading: words(`perform exhibit include assert not satisfy event`),
}

// nameFollowers are the tokens that must follow the Identification after a
// keyword whose next name is otherwise a reference.
var nameFollowers = map[string][]string{
	"dependency": {"from"},
	"featuring":  {"of"},
	"metadata":   {":", "typed", "defined"},
	"@":          {":", "typed", "defined"},
}

// annotating keywords open a comment, documentation or textual
// representation, which ends at its regular comment body.
var annotating = words(`comment doc rep locale language`)

// referenceFollowers mark a name as a reference to another element: a qualified
// name, a feature chain, or the first end of a connector, succession or flow.
var referenceFollowers = words(`:: . , to then`)

// topLevelNames scans a textual model file for the names and short names its
// root namespace declares, in source order. JSON and XMI files are not indexed.
func topLevelNames(rel string, data []byte) []string {
	var lang *notation
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".sysml":
		lang = &sysmlNotation
	case ".kerml":
		lang = &kermlNotation
	default:
		return nil
	}
	var names []string
	var head []token
	depth := 0
	flush := func() {
		names = append(names, lang.declaredNames(head)...)
		head = head[:0]
	}
	for _, tok := range tokenize(string(data)) {
		if tok.kind == tokComment {
			if depth == 0 && isAnnotation(head) {
				flush()
			}
			continue
		}
		if tok.kind == tokSymbol {
			switch tok.text {
			case "{":
				depth++
				continue
			case "}":
				if depth > 0 {
					depth--
				}
				if depth == 0 {
					flush()
				}
				continue
			case ";":
				if depth == 0 {
					flush()
				}
				continue
			}
		}
		if depth == 0 {
			head = append(head, tok)
		}
	}
	flush()
	return names
}

func isAnnotation(head []token) bool {
	for _, tok := range head {
		if tok.kind == tokWord && annotating[tok.text] {
			return true
		}
	}
	return false
}

// declaredNames reads the Identification of one root member from the tokens
// before its body: ( '<' shortName '>' )? name?, after its keywords and
// prefix metadata.
func (n *notation) declaredNames(head []token) []string {
	i, last := 0, ""
	for i < len(head) {
		tok := head[i]
		switch {
		case tok.kind == tokSymbol && tok.text == "#":
			i = skipQualifiedName(head, i+1)
			continue
		case tok.kind == tokSymbol && tok.text == "@":
		case tok.kind == tokWord && n.declaring[tok.text]:
		case tok.kind == tokWord && n.leading[tok.text]:
			if i+1 >= len(head) || head[i+1].kind != tokWord || !(n.declaring[head[i+1].text] || n.leading[head[i+1].text]) {
				return nil
			}
		default:
			return n.identification(head[i:], last)
		}
		last = tok.text
		i++
	}
	return nil
}

func (n *notation) isName(tok token) bool {
	return tok.kind == tokQuoted || tok.kind == tokWord && !n.reserved[tok.text]
}

func (n *notation) identification(rest []token, keyword string) []string {
	var names []string
	i := 0
	if len(rest) >= 3 && rest[0] == (token{tokSymbol, "<"}) && n.isName(rest[1]) && rest[2] == (token{tokSymbol, ">"}) {
		names = append(names, rest[1].text)
		i = 3
	}
	if i < len(rest) && n.isName(rest[i]) {
		names = append(names, rest[i].text)
		i++
	}
	if len(names) == 0 {
		return nil
	}
	next := ""
	if i < len(rest) && rest[i].kind != tokQuoted {
		next = rest[i].text
	}
	if referenceFollowers[next] || keyword == "binding" && next == "=" {
		return nil
	}
	if required, ok := nameFollowers[keyword]; ok && !contains(required, next) {
		return nil
	}
	out := names[:0]
	for _, name := range names {
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// skipQualifiedName returns the index after NAME ( '::' NAME )* starting at i.
func skipQualifiedName(toks []token, i int) int {
	for i < len(toks) && toks[i].kind != tokSymbol {
		i++
		if i >= len(toks) || toks[i] != (token{tokSymbol, "::"}) {
			break
		}
		i++
	}
	return i
}

// symbols are the multi-character symbols the scanner must not split.
var symbols = []string{"::>", ":>>", "::", ":>", "<=", ">=", "=>", "->", "==", "!=", ".."}

// tokenize splits source text into names, symbols and regular comments,
// dropping notes, string values and numbers (KerML 8.2.2).
func tokenize(src string) []token {
	var toks []token
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case strings.HasPrefix(src[i:], "//*"), strings.HasPrefix(src[i:], "/*"):
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				return toks
			}
			if src[i+1] == '*' {
				toks = append(toks, token{kind: tokComment})
			}
			i += 2 + end + 2
		case strings.HasPrefix(src[i:], "//"):
			end := strings.IndexAny(src[i:], "\r\n")
			if end < 0 {
				return toks
			}
			i += end
		case c == '"':
			_, i = quoted(src, i)
		case c == '\'':
			var text string
			text, i = quoted(src, i)
			toks = append(toks, token{tokQuoted, text})
		default:
			r, size := utf8.DecodeRuneInString(src[i:])
			switch {
			case unicode.IsSpace(r):
				i += size
			case r == '_' || unicode.IsLetter(r):
				start := i
				for i < len(src) {
					r, size = utf8.DecodeRuneInString(src[i:])
					if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
						break
					}
					i += size
				}
				toks = append(toks, token{tokWord, src[start:i]})
			case unicode.IsDigit(r):
				for i < len(src) {
					r, size = utf8.DecodeRuneInString(src[i:])
					if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
						break
					}
					i += size
				}
			default:
				text := src[i : i+size]
				for _, s := range symbols {
					if strings.HasPrefix(src[i:], s) {
						text = s
						break
					}
				}
				toks = append(toks, token{tokSymbol, text})
				i += len(text)
			}
		}
	}
	return toks
}

var escapes = map[byte]string{'\'': "'", '"': `"`, 'b': "\b", 'f': "\f", 't': "\t", 'n': "\n", '\\': `\`}

// quoted reads the quoted text opening at src[i], resolving the escape
// sequences of KerML Table 4, and returns it with the index after the closing quote.
func quoted(src string, i int) (string, int) {
	quote := src[i]
	var b strings.Builder
	for i++; i < len(src); i++ {
		switch c := src[i]; {
		case c == quote:
			return b.String(), i + 1
		case c == '\\' && i+1 < len(src):
			i++
			if meaning, ok := escapes[src[i]]; ok {
				b.WriteString(meaning)
			} else {
				b.WriteByte('\\')
				b.WriteByte(src[i])
			}
		default:
			b.WriteByte(c)
		}
	}
	return b.String(), len(src)
}

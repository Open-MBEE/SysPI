// SPDX-License-Identifier: Apache-2.0

package project

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

type indexToken struct {
	text string
	kind byte
	line int
}

func indexTokens(src string) ([]indexToken, error) {
	var tokens []indexToken
	line := 1
	for len(src) > 0 {
		start := line
		r, n := utf8.DecodeRuneInString(src)
		switch {
		case unicode.IsSpace(r) || (len(tokens) == 0 && r == '\ufeff'):
			if r == '\n' {
				line++
			}
			src = src[n:]
		case strings.HasPrefix(src, "//"):
			end := strings.IndexByte(src, '\n')
			if end < 0 {
				src = ""
			} else {
				src = src[end:]
			}
		case strings.HasPrefix(src, "/*"):
			end := strings.Index(src[2:], "*/")
			if end < 0 {
				return nil, fmt.Errorf("line %d: unterminated comment", start)
			}
			end += 4
			tokens = append(tokens, indexToken{kind: 'c', line: start})
			line += strings.Count(src[:end], "\n")
			src = src[end:]
		case r == '\'' || r == '"':
			var value strings.Builder
			i := 1
			for i < len(src) && src[i] != byte(r) && src[i] != '\n' && src[i] != '\r' {
				ch := src[i]
				i++
				if ch == '\\' && i < len(src) {
					ch = src[i]
					i++
					switch ch {
					case 'n':
						ch = '\n'
					case 'r':
						ch = '\r'
					case 't':
						ch = '\t'
					case 'v':
						ch = '\v'
					case 'f':
						ch = '\f'
					case '\\', '\'', '"':
					default:
						return nil, fmt.Errorf("line %d: unsupported escape \\%c", start, ch)
					}
				}
				value.WriteByte(ch)
			}
			if i == len(src) || src[i] != byte(r) {
				return nil, fmt.Errorf("line %d: unterminated quoted text", start)
			}
			tokens = append(tokens, indexToken{text: value.String(), kind: byte(r), line: start})
			src = src[i+1:]
		case unicode.IsLetter(r) || r == '_':
			end := n
			for end < len(src) {
				next, size := utf8.DecodeRuneInString(src[end:])
				if !unicode.IsLetter(next) && !unicode.IsDigit(next) && next != '_' {
					break
				}
				end += size
			}
			tokens = append(tokens, indexToken{text: src[:end], kind: 'w', line: start})
			src = src[end:]
		case strings.HasPrefix(src, "::"):
			tokens = append(tokens, indexToken{text: "::", kind: 'p', line: start})
			src = src[2:]
		default:
			tokens = append(tokens, indexToken{text: src[:n], kind: 'p', line: start})
			src = src[n:]
		}
	}
	return tokens, nil
}

type indexParser struct {
	tokens []indexToken
	pos    int
}

func (p *indexParser) peek() indexToken {
	if p.pos == len(p.tokens) {
		return indexToken{}
	}
	return p.tokens[p.pos]
}

func (p *indexParser) take(text string) bool {
	t := p.peek()
	if (t.kind == 'p' || t.kind == 'w') && t.text == text {
		p.pos++
		return true
	}
	return false
}

func indexWord(t indexToken, choices string) bool {
	return t.kind == 'w' && strings.Contains(" "+choices+" ", " "+t.text+" ")
}

func (p *indexParser) name() string {
	t := p.peek()
	if t.kind == '\'' || (t.kind == 'w' && !indexWord(t,
		"about accept all as assign bind by connect conjugates crosses default disjoint differences do else end equals false "+
			"first for from if intersects language locale of ordered perform redefines references return send "+
			"specializes subsets then to true typed unions nonunique via when")) {
		p.pos++
		return t.text
	}
	return ""
}

// skipDeclaration stops at a top-level terminator, body, or annotation comment.
func (p *indexParser) skipDeclaration(annotation bool) bool {
	var closing []string
	for p.pos < len(p.tokens) {
		t := p.peek()
		p.pos++
		if annotation && t.kind == 'c' && len(closing) == 0 {
			return true
		}
		if t.kind != 'p' {
			continue
		}
		switch t.text {
		case "{":
			closing = append(closing, "}")
		case "(":
			closing = append(closing, ")")
		case "[":
			closing = append(closing, "]")
		case "}", ")", "]":
			if len(closing) == 0 || closing[len(closing)-1] != t.text {
				return false
			}
			closing = closing[:len(closing)-1]
			if t.text == "}" && len(closing) == 0 {
				return true
			}
		case ";":
			if len(closing) == 0 {
				return true
			}
		}
	}
	return false
}

// textualIndexNames reads root declaration headers without interpreting model bodies.
func textualIndexNames(data []byte) ([]string, error) {
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("textual model is not valid UTF-8")
	}
	tokens, err := indexTokens(string(data))
	if err != nil {
		return nil, err
	}
	p := indexParser{tokens: tokens}
	var names []string
	for p.pos < len(tokens) {
		start := p.peek()
		if start.kind == 'c' || p.take(";") {
			if start.kind == 'c' {
				p.pos++
			}
			continue
		}
		for indexWord(p.peek(), "abstract composite constant derived end in inout library nonunique ordered out private protected public readonly standard variation") {
			p.pos++
		}
		head := p.peek()
		switch {
		case indexWord(head, "import filter"):
			p.pos++
		case indexWord(head, "package namespace type classifier datatype class struct metaclass assoc behavior function predicate multiplicity "+
			"feature step expr bool inv connector binding succession alias comment doc rep "+
			"attribute enum occurrence individual ref item part connection flow interface port action calc state constraint requirement concern case use analysis verification view viewpoint rendering metadata"):
			p.pos++
			if indexWord(head, "use analysis verification") && !p.take("case") {
				return nil, fmt.Errorf("line %d: unsupported %q declaration", start.line, head.text)
			}
			if indexWord(head, "assoc") {
				p.take("struct")
			}
			if indexWord(head, "succession") {
				p.take("flow")
			}
			p.take("def")
			p.take("all")
			if next := p.peek(); next.kind == 'p' && (next.text == "#" || next.text == "@") {
				return nil, fmt.Errorf("line %d: unsupported annotation in %q declaration header", start.line, head.text)
			}
			if p.take("<") {
				short := p.name()
				if short == "" || !p.take(">") {
					return nil, fmt.Errorf("line %d: invalid short name", start.line)
				}
				names = append(names, short)
			}
			if name := p.name(); name != "" {
				next := p.peek()
				if (next.kind == 'p' && (next.text == "::" || (head.text == "binding" && next.text == "="))) ||
					indexWord(next, "to references") {
					return nil, fmt.Errorf("line %d: cannot distinguish a name from a reference in %q", start.line, head.text)
				}
				names = append(names, name)
			}
		default:
			return nil, fmt.Errorf("line %d: unsupported top-level declaration beginning with %q", start.line, start.text)
		}
		if !p.skipDeclaration(indexWord(head, "comment doc rep")) {
			return nil, fmt.Errorf("line %d: unterminated or unbalanced declaration", start.line)
		}
	}
	return names, nil
}

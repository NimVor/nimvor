// Package nginx parses Nginx configuration (read-only) and extracts
// virtual hosts.
package nginx

import (
	"fmt"
	"strings"
	"unicode"
)

// Directive is one parsed Nginx directive, optionally with a block.
type Directive struct {
	Name  string      `json:"name"`
	Args  []string    `json:"args"`
	Block []Directive `json:"block,omitempty"`
	File  string      `json:"file"`
	Line  int         `json:"line"`
	// HasBlock distinguishes "name {}" from "name;".
	HasBlock bool `json:"-"`
}

// ParseError reports a syntax error with its location.
type ParseError struct {
	File string
	Line int
	Msg  string
}

func (e *ParseError) Error() string { return fmt.Sprintf("%s:%d: %s", e.File, e.Line, e.Msg) }

type tokenKind int

const (
	tokWord tokenKind = iota
	tokSemicolon
	tokOpen
	tokClose
)

type token struct {
	kind tokenKind
	text string
	line int
}

// maxTokens guards against pathological input.
const maxTokens = 500000

func tokenize(src, file string) ([]token, error) {
	var toks []token
	line := 1
	i := 0
	n := len(src)
	for i < n {
		if len(toks) > maxTokens {
			return nil, &ParseError{file, line, "configuration too large"}
		}
		c := src[i]
		switch {
		case c == '\n':
			line++
			i++
		case c == ' ' || c == '\t' || c == '\r':
			i++
		case c == '#':
			for i < n && src[i] != '\n' {
				i++
			}
		case c == ';':
			toks = append(toks, token{tokSemicolon, ";", line})
			i++
		case c == '{':
			toks = append(toks, token{tokOpen, "{", line})
			i++
		case c == '}':
			toks = append(toks, token{tokClose, "}", line})
			i++
		case c == '"' || c == '\'':
			quote := c
			start := line
			i++
			var sb strings.Builder
			closed := false
			for i < n {
				ch := src[i]
				if ch == '\\' && i+1 < n {
					next := src[i+1]
					if next == quote || next == '\\' {
						sb.WriteByte(next)
					} else {
						sb.WriteByte(ch)
						sb.WriteByte(next)
					}
					if next == '\n' {
						line++
					}
					i += 2
					continue
				}
				if ch == quote {
					closed = true
					i++
					break
				}
				if ch == '\n' {
					line++
				}
				sb.WriteByte(ch)
				i++
			}
			if !closed {
				return nil, &ParseError{file, start, "unterminated quoted string"}
			}
			toks = append(toks, token{tokWord, sb.String(), start})
		default:
			var sb strings.Builder
			for i < n {
				ch := src[i]
				if ch == ';' || ch == '{' || ch == '}' || ch == '#' || unicode.IsSpace(rune(ch)) {
					break
				}
				// ${var} inside a word must not be split at the brace.
				if ch == '$' && i+1 < n && src[i+1] == '{' {
					end := strings.IndexByte(src[i:], '}')
					if end > 0 {
						sb.WriteString(src[i : i+end+1])
						i += end + 1
						continue
					}
				}
				if ch == '\\' && i+1 < n {
					sb.WriteByte(ch)
					sb.WriteByte(src[i+1])
					i += 2
					continue
				}
				sb.WriteByte(ch)
				i++
			}
			toks = append(toks, token{tokWord, sb.String(), line})
		}
	}
	return toks, nil
}

// Parse parses configuration text. file is used for error messages and
// recorded on each directive.
func Parse(src, file string) ([]Directive, error) {
	toks, err := tokenize(src, file)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks, file: file}
	dirs, err := p.parseBlock(0)
	if err != nil {
		return nil, err
	}
	if p.pos < len(p.toks) {
		return nil, &ParseError{file, p.toks[p.pos].line, "unexpected '}'"}
	}
	return dirs, nil
}

type parser struct {
	toks []token
	pos  int
	file string
}

const maxDepth = 50

func (p *parser) parseBlock(depth int) ([]Directive, error) {
	if depth > maxDepth {
		return nil, &ParseError{p.file, p.line(), "blocks nested too deeply"}
	}
	var out []Directive
	for p.pos < len(p.toks) {
		t := p.toks[p.pos]
		switch t.kind {
		case tokClose:
			return out, nil
		case tokSemicolon:
			// stray semicolon; nginx rejects it, we tolerate it.
			p.pos++
			continue
		case tokOpen:
			return nil, &ParseError{p.file, t.line, "unexpected '{'"}
		}
		d := Directive{Name: t.text, File: p.file, Line: t.line, Args: []string{}}
		p.pos++
		for {
			if p.pos >= len(p.toks) {
				return nil, &ParseError{p.file, d.Line, fmt.Sprintf("directive %q is not terminated", d.Name)}
			}
			a := p.toks[p.pos]
			if a.kind == tokWord {
				d.Args = append(d.Args, a.text)
				p.pos++
				continue
			}
			if a.kind == tokSemicolon {
				p.pos++
				break
			}
			if a.kind == tokOpen {
				p.pos++
				inner, err := p.parseBlock(depth + 1)
				if err != nil {
					return nil, err
				}
				if p.pos >= len(p.toks) || p.toks[p.pos].kind != tokClose {
					return nil, &ParseError{p.file, d.Line, fmt.Sprintf("block %q is not closed", d.Name)}
				}
				p.pos++
				d.Block = inner
				d.HasBlock = true
				break
			}
			return nil, &ParseError{p.file, a.line, fmt.Sprintf("unexpected '}' in directive %q", d.Name)}
		}
		out = append(out, d)
	}
	return out, nil
}

func (p *parser) line() int {
	if p.pos < len(p.toks) {
		return p.toks[p.pos].line
	}
	return 0
}

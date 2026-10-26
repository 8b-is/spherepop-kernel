package term

import (
	"fmt"
	"strings"
)

// Defs maps names to terms. A defined name is replaced by its term when it
// appears in source text, which is plain textual abbreviation and not
// binding: there are no binders in circle terms.
type Defs map[string]*Term

// SyntaxError describes a failure to parse source text.
type SyntaxError struct {
	Offset int
	Msg    string
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("term: offset %d: %s", e.Offset, e.Msg)
}

// Constants is the set of constant names the parser recognises without a
// definition. A definition of one of these names takes precedence.
var Constants = []string{"S", "K", "I", "B", "C", "W"}

// Limits on source text, so that hostile input fails with a SyntaxError
// instead of exhausting memory or the goroutine stack. Both are variables so
// a host with unusual needs can raise them deliberately.
var (
	MaxSourceBytes = 1 << 22
	MaxNesting     = 100000
)

// Parse reads a term in the concrete syntax of SPEC.md.
func Parse(src string) (*Term, error) { return ParseWith(src, nil) }

// ParseWith reads a term, replacing names found in defs by their terms.
func ParseWith(src string, defs Defs) (*Term, error) {
	if len(src) > MaxSourceBytes {
		return nil, &SyntaxError{Offset: 0, Msg: fmt.Sprintf("source of %d bytes exceeds the limit of %d", len(src), MaxSourceBytes)}
	}
	p := &parser{src: src, defs: defs}
	t, err := p.term()
	if err != nil {
		return nil, err
	}
	p.space()
	if p.pos < len(p.src) {
		return nil, p.errorf("unexpected %q", p.src[p.pos])
	}
	return t, nil
}

// MustParse is Parse for source text known to be valid. It panics otherwise.
func MustParse(src string) *Term {
	t, err := Parse(src)
	if err != nil {
		panic(err)
	}
	return t
}

type parser struct {
	src   string
	pos   int
	defs  Defs
	depth int
}

func (p *parser) errorf(format string, args ...any) error {
	return &SyntaxError{Offset: p.pos, Msg: fmt.Sprintf(format, args...)}
}

func (p *parser) space() {
	for p.pos < len(p.src) {
		switch p.src[p.pos] {
		case ' ', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}

func isAlpha(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isAlnum(c byte) bool { return isAlpha(c) || isDigit(c) }
func isIdent(c byte) bool { return isAlnum(c) || c == '_' || c == '\'' }

// term parses a juxtaposition of atoms, associating to the left.
func (p *parser) term() (*Term, error) {
	var acc *Term
	for {
		p.space()
		if p.pos >= len(p.src) || p.src[p.pos] == ')' || p.src[p.pos] == '>' {
			break
		}
		a, err := p.atom()
		if err != nil {
			return nil, err
		}
		if acc == nil {
			acc = a
		} else {
			acc = App(acc, a)
		}
	}
	if acc == nil {
		return nil, p.errorf("expected a term")
	}
	return acc, nil
}

func (p *parser) atom() (*Term, error) {
	c := p.src[p.pos]
	if c == '(' || c == '<' {
		if p.depth++; p.depth > MaxNesting {
			return nil, p.errorf("nesting deeper than %d", MaxNesting)
		}
		defer func() { p.depth-- }()
	}
	switch {
	case c == '(':
		p.pos++
		t, err := p.term()
		if err != nil {
			return nil, err
		}
		p.space()
		if p.pos >= len(p.src) || p.src[p.pos] != ')' {
			return nil, p.errorf("expected ')'")
		}
		p.pos++
		return t, nil
	case c == '<':
		p.pos++
		body, err := p.term()
		if err != nil {
			return nil, err
		}
		p.space()
		if p.pos >= len(p.src) || p.src[p.pos] != '>' {
			return nil, p.errorf("expected '>'")
		}
		p.pos++
		if p.pos >= len(p.src) || p.src[p.pos] != '_' {
			return nil, p.errorf("expected '_' and a label after '>'")
		}
		p.pos++
		label, err := p.label()
		if err != nil {
			return nil, err
		}
		return Bracket(label, body), nil
	case isAlpha(c):
		return p.ident()
	case isDigit(c):
		return p.numeral()
	}
	return nil, p.errorf("unexpected %q", c)
}

// numeral reads a run of decimal digits as an atom. A numeral is inert data
// like any other atom: it has no value to the reducer, and a host that wants
// to read one as an integer does so at its own boundary. Letters directly
// after the digits are an error, so `3a` is not silently two atoms.
func (p *parser) numeral() (*Term, error) {
	start := p.pos
	for p.pos < len(p.src) && isDigit(p.src[p.pos]) {
		p.pos++
	}
	if p.pos < len(p.src) && isIdent(p.src[p.pos]) {
		return nil, p.errorf("numeral %q is followed by %q; separate them with a space", p.src[start:p.pos], p.src[p.pos])
	}
	return Atom(p.src[start:p.pos]), nil
}

func (p *parser) label() (string, error) {
	start := p.pos
	for p.pos < len(p.src) && isAlnum(p.src[p.pos]) {
		p.pos++
	}
	if p.pos == start {
		return "", p.errorf("expected a label of letters and digits")
	}
	return p.src[start:p.pos], nil
}

func (p *parser) ident() (*Term, error) {
	start := p.pos
	for p.pos < len(p.src) && isIdent(p.src[p.pos]) {
		p.pos++
	}
	name := p.src[start:p.pos]
	if d, ok := p.defs[name]; ok {
		return d, nil
	}
	for _, k := range Constants {
		if name == k {
			return Const(name), nil
		}
	}
	if strings.HasPrefix(name, "sc_") {
		label := name[3:]
		if label == "" {
			return nil, &SyntaxError{Offset: start, Msg: "scope constructor sc_ needs a label"}
		}
		for i := 0; i < len(label); i++ {
			if !isAlnum(label[i]) {
				return nil, &SyntaxError{Offset: start, Msg: fmt.Sprintf("scope label %q must use letters and digits only", label)}
			}
		}
		return Scope(label), nil
	}
	if name[0] >= 'a' && name[0] <= 'z' {
		return Atom(name), nil
	}
	return nil, &SyntaxError{Offset: start, Msg: fmt.Sprintf("unknown constant %q (constants are %s; atoms start with a lower-case letter)", name, strings.Join(Constants, " "))}
}

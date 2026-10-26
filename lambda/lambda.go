// Package lambda is a small front end: it reads programs written with named
// parameters and compiles them to combinator terms by bracket abstraction.
// The result is an ordinary term for the reducer. Nothing here is part of the
// calculus; a program written directly in combinators is exactly as valid.
//
// Syntax:
//
//	\x y. body        a function of x then y; the body extends as far as possible
//	f a b             application, associating to the left
//	( ... )           grouping
//	any other name    an atom or constant, exactly as in package term
//	S K I B C W       the constants
//
// A name bound by an enclosing \ is a variable. Every other lower-case name is
// an inert atom, which is how effect vocabulary such as `effects` and `door`
// is written. Brackets and scope constructors are not part of this language.
package lambda

import (
	"errors"
	"fmt"
	"strings"

	"github.com/standardgalactic/spherepop-kernel/term"
)

// SyntaxError reports a failure to parse a program.
type SyntaxError struct {
	Offset int
	Msg    string
}

func (e *SyntaxError) Error() string { return fmt.Sprintf("lambda: offset %d: %s", e.Offset, e.Msg) }

// ErrTooDeep reports nesting beyond MaxDepth.
var ErrTooDeep = errors.New("lambda: nesting too deep")

// Limits against hostile or accidental input.
var (
	MaxSourceBytes = 1 << 20
	MaxDepth       = 10000
)

// expr is an application tree whose leaves are either variables or finished
// terms. Abstraction removes variables one at a time.
type expr struct {
	v    string     // variable name, if this is a variable
	leaf *term.Term // finished term, if this is a leaf
	f, x *expr      // application
}

func (e *expr) isVar() bool { return e.v != "" }

func (e *expr) occurs(x string) bool {
	switch {
	case e.isVar():
		return e.v == x
	case e.leaf != nil:
		return false
	}
	return e.f.occurs(x) || e.x.occurs(x)
}

var (
	cI = &expr{leaf: term.Const("I")}
	cK = &expr{leaf: term.Const("K")}
	cB = &expr{leaf: term.Const("B")}
	cC = &expr{leaf: term.Const("C")}
	cS = &expr{leaf: term.Const("S")}
)

func app(f, x *expr) *expr { return &expr{f: f, x: x} }

// abstract removes variable x from e, returning an expression that, applied to
// an argument, behaves as e with that argument for x. These are the classical
// rules with the B and C optimisations and eta contraction.
func abstract(x string, e *expr) *expr {
	if !e.occurs(x) {
		return app(cK, e)
	}
	if e.isVar() { // e is x itself
		return cI
	}
	switch fx, gx := e.f.occurs(x), e.x.occurs(x); {
	case !fx && e.x.isVar(): // eta: \x. f x = f
		return e.f
	case !fx:
		return app(app(cB, e.f), abstract(x, e.x))
	case !gx:
		return app(app(cC, abstract(x, e.f)), e.x)
	default:
		return app(app(cS, abstract(x, e.f)), abstract(x, e.x))
	}
}

func (e *expr) term() (*term.Term, error) {
	switch {
	case e.isVar():
		return nil, fmt.Errorf("lambda: unbound variable %q escaped compilation", e.v)
	case e.leaf != nil:
		return e.leaf, nil
	}
	f, err := e.f.term()
	if err != nil {
		return nil, err
	}
	x, err := e.x.term()
	if err != nil {
		return nil, err
	}
	return term.App(f, x), nil
}

// Compile reads a program and returns its combinator term. Names in defs are
// replaced by their terms, as in term.ParseWith.
func Compile(src string, defs term.Defs) (*term.Term, error) {
	if len(src) > MaxSourceBytes {
		return nil, &SyntaxError{Msg: fmt.Sprintf("source of %d bytes exceeds the limit of %d", len(src), MaxSourceBytes)}
	}
	p := &parser{src: src, defs: defs}
	e, err := p.expr(nil)
	if err != nil {
		return nil, err
	}
	p.space()
	if p.pos < len(p.src) {
		return nil, p.errorf("unexpected %q", p.src[p.pos])
	}
	return e.term()
}

type parser struct {
	src   string
	pos   int
	defs  term.Defs
	depth int
}

func (p *parser) errorf(format string, args ...any) error {
	return &SyntaxError{Offset: p.pos, Msg: fmt.Sprintf(format, args...)}
}

func (p *parser) space() {
	for p.pos < len(p.src) && strings.IndexByte(" \t\n\r", p.src[p.pos]) >= 0 {
		p.pos++
	}
}

func isIdentStart(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isIdentChar(c byte) bool {
	return isIdentStart(c) || c >= '0' && c <= '9' || c == '_' || c == '\''
}

func (p *parser) enter() error {
	if p.depth++; p.depth > MaxDepth {
		return ErrTooDeep
	}
	return nil
}

// expr parses a sequence of operands, the last of which may be a lambda.
func (p *parser) expr(scope []string) (*expr, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer func() { p.depth-- }()
	var acc *expr
	for {
		p.space()
		if p.pos >= len(p.src) || p.src[p.pos] == ')' {
			break
		}
		var x *expr
		var err error
		if p.src[p.pos] == '\\' {
			x, err = p.lambda(scope)
			if err != nil {
				return nil, err
			}
			// A lambda extends as far as possible, so it ends the sequence.
			if acc == nil {
				return x, nil
			}
			return app(acc, x), nil
		}
		x, err = p.operand(scope)
		if err != nil {
			return nil, err
		}
		if acc == nil {
			acc = x
		} else {
			acc = app(acc, x)
		}
	}
	if acc == nil {
		return nil, p.errorf("expected an expression")
	}
	return acc, nil
}

func (p *parser) lambda(scope []string) (*expr, error) {
	p.pos++ // the backslash
	var params []string
	for {
		p.space()
		if p.pos >= len(p.src) {
			return nil, p.errorf("expected '.' after the parameters")
		}
		if p.src[p.pos] == '.' {
			p.pos++
			break
		}
		if !isIdentStart(p.src[p.pos]) || p.src[p.pos] < 'a' || p.src[p.pos] > 'z' {
			return nil, p.errorf("a parameter is a lower-case name")
		}
		start := p.pos
		for p.pos < len(p.src) && isIdentChar(p.src[p.pos]) {
			p.pos++
		}
		params = append(params, p.src[start:p.pos])
	}
	if len(params) == 0 {
		return nil, p.errorf("a function needs at least one parameter")
	}
	inner := append(append([]string(nil), scope...), params...)
	body, err := p.expr(inner)
	if err != nil {
		return nil, err
	}
	for i := len(params) - 1; i >= 0; i-- {
		body = abstract(params[i], body)
	}
	return body, nil
}

func (p *parser) operand(scope []string) (*expr, error) {
	c := p.src[p.pos]
	switch {
	case c == '(':
		p.pos++
		e, err := p.expr(scope)
		if err != nil {
			return nil, err
		}
		p.space()
		if p.pos >= len(p.src) || p.src[p.pos] != ')' {
			return nil, p.errorf("expected ')'")
		}
		p.pos++
		return e, nil
	case c >= '0' && c <= '9':
		start := p.pos
		for p.pos < len(p.src) && c >= '0' && p.src[p.pos] >= '0' && p.src[p.pos] <= '9' {
			p.pos++
		}
		if p.pos < len(p.src) && isIdentChar(p.src[p.pos]) {
			return nil, p.errorf("numeral %q is followed by %q; separate them with a space", p.src[start:p.pos], p.src[p.pos])
		}
		return &expr{leaf: term.Atom(p.src[start:p.pos])}, nil
	case isIdentStart(c):
		start := p.pos
		for p.pos < len(p.src) && isIdentChar(p.src[p.pos]) {
			p.pos++
		}
		name := p.src[start:p.pos]
		for _, b := range scope {
			if b == name {
				return &expr{v: name}, nil
			}
		}
		t, err := term.ParseWith(name, p.defs)
		if err != nil {
			return nil, &SyntaxError{Offset: start, Msg: fmt.Sprintf("%v", err)}
		}
		if t.Kind() == term.KindScope || t.Kind() == term.KindBracket {
			return nil, &SyntaxError{Offset: start, Msg: "scope constructors and brackets are not part of this language"}
		}
		return &expr{leaf: t}, nil
	}
	return nil, p.errorf("unexpected %q", c)
}

// Package encode builds the data a combinator program can actually use.
// Atoms are inert: no combinator can inspect one, so a program cannot branch
// on an atom. It can branch on a function, so observations are given to
// programs as Church-style functions. This package is the host's side of that
// contract and knows nothing about reduction beyond the shape of the terms.
package encode

import (
	"errors"
	"fmt"

	"github.com/standardgalactic/spherepop-kernel/term"
)

// MaxNat bounds numerals, which are built in unary and grow with their value.
const MaxNat = 10000

// ErrTooLarge reports a numeral above MaxNat.
var ErrTooLarge = errors.New("encode: numeral too large")

// Bool encodes true as K and false as K I, so that a Boolean b applied to two
// terms x y reduces to x if b is true and to y if it is false. It relies on
// K being permitted where the program runs.
func Bool(b bool) *term.Term {
	if b {
		return term.Const("K")
	}
	return term.App(term.Const("K"), term.Const("I"))
}

// Nat encodes n as the Church numeral that applies its first argument n times
// to its second: Nat(2) f x reduces to f (f x). Zero is K I, and n+1 is
// S B n.
func Nat(n uint32) (*term.Term, error) {
	if n > MaxNat {
		return nil, fmt.Errorf("%w: %d", ErrTooLarge, n)
	}
	t := term.App(term.Const("K"), term.Const("I"))
	for i := uint32(0); i < n; i++ {
		t = term.Apply(term.Const("S"), term.Const("B"), t)
	}
	return t, nil
}

// Sym passes an identifier through as an atom. A program cannot look inside
// it, but it can place it in an effect it proposes, which is how a program
// names an entity or a target it was told about.
func Sym(name string) (*term.Term, error) {
	t, err := term.Parse(name)
	if err != nil || t.Kind() != term.KindAtom || t.Name() != name {
		return nil, fmt.Errorf("encode: %q is not an atom", name)
	}
	return t, nil
}

package encode

import (
	"errors"
	"testing"

	"github.com/standardgalactic/spherepop-kernel/reduce"
	"github.com/standardgalactic/spherepop-kernel/term"
)

func nf(t *testing.T, x *term.Term) string {
	t.Helper()
	res, err := reduce.Reduce(x, reduce.LeftmostOutermost, 100000)
	if err != nil || res.Exhausted {
		t.Fatalf("%v exhausted=%v", err, res.Exhausted)
	}
	return res.Term.String()
}

func TestBoolSelects(t *testing.T) {
	a, b := term.Atom("a"), term.Atom("b")
	if got := nf(t, term.Apply(Bool(true), a, b)); got != "a" {
		t.Errorf("true: %s", got)
	}
	if got := nf(t, term.Apply(Bool(false), a, b)); got != "b" {
		t.Errorf("false: %s", got)
	}
}

func TestNatIsChurch(t *testing.T) {
	f, x := term.Atom("f"), term.Atom("x")
	want := []string{"x", "f x", "f (f x)", "f (f (f x))"}
	for n, w := range want {
		c, err := Nat(uint32(n))
		if err != nil {
			t.Fatal(err)
		}
		if got := nf(t, term.Apply(c, f, x)); got != w {
			t.Errorf("Nat(%d) f x = %s, want %s", n, got, w)
		}
	}
	if _, err := Nat(MaxNat + 1); !errors.Is(err, ErrTooLarge) {
		t.Errorf("oversize numeral: %v", err)
	}
	// A numeral can be tested for zero: n (K false) true.
	zero := func(n uint32) string {
		c, _ := Nat(n)
		return nf(t, term.Apply(c, term.App(term.Const("K"), Bool(false)), Bool(true), term.Atom("yes"), term.Atom("no")))
	}
	if zero(0) != "yes" || zero(3) != "no" {
		t.Errorf("zero test: %s %s", zero(0), zero(3))
	}
}

func TestSym(t *testing.T) {
	if s, err := Sym("door"); err != nil || s.Kind() != term.KindAtom {
		t.Fatalf("%v %v", s, err)
	}
	for _, bad := range []string{"", "K", "a b", "(a)", "sc_x"} {
		if _, err := Sym(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

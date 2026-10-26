// Package vectors holds the conformance vectors taken from the SpherePOP
// essays. The same list backs `go test` and the `spherepop test` command, so
// the shipped binary can check itself against the source calculus.
package vectors

import (
	"fmt"
	"io"

	"github.com/standardgalactic/spherepop-kernel/reduce"
	"github.com/standardgalactic/spherepop-kernel/term"
	"github.com/standardgalactic/spherepop-kernel/trace"
)

// Vector is one named check with a pointer to where the expectation comes from.
type Vector struct {
	Name string
	Ref  string
	Run  func() error
}

var (
	lo = reduce.LeftmostOutermost
	li = reduce.LeftmostInnermost
	ri = reduce.RightmostInnermost
	ro = reduce.RightmostOutermost

	allStrategies = []reduce.Strategy{lo, li, ri, ro}
)

func parse(src string) *term.Term {
	t, err := term.ParseWith(src, defs())
	if err != nil {
		panic(fmt.Sprintf("vectors: %q: %v", src, err))
	}
	return t
}

// defs are the abbreviations of the duplication essay: I = S K K, W = S I I,
// Omega = W W, and M = S (S I I) I.
func defs() term.Defs {
	i := term.MustParse("S K K")
	w := term.Apply(term.Const("S"), i, i)
	m := term.Apply(term.Const("S"), w, i)
	return term.Defs{"id": i, "w": w, "om": term.App(w, w), "m": m}
}

// checked reduces, then records and independently checks the trace.
func checked(cfg reduce.Config, start *term.Term, s reduce.Strategy, fuel uint64) (reduce.Result, error) {
	res, doc, err := trace.Record(cfg, start, s, fuel)
	if err != nil {
		return res, err
	}
	if _, err := trace.Check(doc, trace.Options{RequireStrategy: true}); err != nil {
		return res, fmt.Errorf("trace of this run fails its own check: %w", err)
	}
	return res, nil
}

// anySteps skips the step-count check where the source gives only the result.
const anySteps = ^uint64(0)

func normalForm(src, want string, s reduce.Strategy, steps uint64) func() error {
	return func() error {
		res, err := checked(reduce.DefaultConfig(), parse(src), s, 1000)
		if err != nil {
			return err
		}
		if res.Exhausted {
			return fmt.Errorf("%s exhausted fuel at %s", src, res.Term)
		}
		if !term.Equal(res.Term, parse(want)) {
			return fmt.Errorf("%s reduced to %s, want %s", src, res.Term, want)
		}
		if steps != anySteps && res.Steps != steps {
			return fmt.Errorf("%s took %d steps, want %d", src, res.Steps, steps)
		}
		return nil
	}
}

// period returns the length of the cycle a run enters, or 0 if it repeats no term.
func period(cfg reduce.Config, start *term.Term, s reduce.Strategy, fuel uint64) (int, error) {
	res, err := cfg.Reduce(start, s, fuel)
	if err != nil {
		return 0, err
	}
	seen := map[term.Digest]int{start.Digest(): 0}
	cur := start
	for i, st := range res.Trace {
		if cur, err = cfg.Apply(cur, st.Path, st.Rule); err != nil {
			return 0, err
		}
		if j, ok := seen[cur.Digest()]; ok {
			return i + 1 - j, nil
		}
		seen[cur.Digest()] = i + 1
	}
	return 0, nil
}

// All returns every vector in a fixed order.
func All() []Vector {
	var vs []Vector
	add := func(name, ref string, run func() error) { vs = append(vs, Vector{name, ref, run}) }

	add("S K K x -> x", "Lemma 5.1", normalForm("S K K x", "x", lo, 2))
	add("I a b -> a b in two steps", "Lemma 5.1", normalForm("id a b", "a b", lo, 2))
	add("I a -> a in two steps", "Lemma 5.1", normalForm("id a", "a", lo, 2))
	add("W f x -> f x x", "Section 2", normalForm("W f x", "f x x", lo, 1))
	add("B f g x -> f (g x)", "Section 2", normalForm("B f g x", "f (g x)", lo, 1))
	add("C f x y -> f y x", "Section 2", normalForm("C f x y", "f y x", lo, 1))
	add("S f g x -> f x (g x)", "Section 2", normalForm("S f g x", "f x (g x)", lo, 1))
	add("W = C S I on arguments", "Section 2", normalForm("C S I f x", "f x x", lo, 3))
	add("M x -> x x x", "Section 5.4", normalForm("m x", "x x x", lo, anySteps))

	for _, s := range []reduce.Strategy{lo, ro} {
		s := s
		add("K a Omega -> a under "+s.String(), "Section 5.3 table", normalForm("K a om", "a", s, 1))
	}
	for _, s := range []reduce.Strategy{li, ri} {
		s := s
		add("K a Omega has no normal form under "+s.String(), "Section 5.3 table", func() error {
			res, err := checked(reduce.DefaultConfig(), parse("K a om"), s, 400)
			if err != nil {
				return err
			}
			if !res.Exhausted {
				return fmt.Errorf("terminated at %s", res.Term)
			}
			if p, _ := period(reduce.DefaultConfig(), parse("K a om"), s, 400); p != 5 {
				return fmt.Errorf("cycle period %d, want 5", p)
			}
			return nil
		})
	}

	add("Omega: lo never repeats within 400 steps", "Lemma 5.2", func() error {
		res, err := checked(reduce.DefaultConfig(), parse("om"), lo, 400)
		if err != nil {
			return err
		}
		if !res.Exhausted {
			return fmt.Errorf("terminated")
		}
		if p, _ := period(reduce.DefaultConfig(), parse("om"), lo, 400); p != 0 {
			return fmt.Errorf("repeated with period %d", p)
		}
		return nil
	})
	for _, s := range []reduce.Strategy{li, ri, ro} {
		s := s
		add("Omega: "+s.String()+" cycles with period 5", "Section 5.3", func() error {
			p, err := period(reduce.DefaultConfig(), parse("om"), s, 100)
			if err != nil {
				return err
			}
			if p != 5 {
				return fmt.Errorf("period %d, want 5", p)
			}
			return nil
		})
	}

	add("W (I^n W): 2n+3 lo steps to W (I^(n+1) W), 13+3n circles", "Lemma 5.2", func() error {
		w := parse("w")
		idn := w
		for n := 0; n <= 5; n++ {
			start := term.App(w, idn)
			if start.Size() != 13+3*n {
				return fmt.Errorf("n=%d: size %d, want %d", n, start.Size(), 13+3*n)
			}
			next := term.App(w, term.App(parse("id"), idn))
			cfg := reduce.DefaultConfig()
			res, err := cfg.Reduce(start, lo, uint64(2*n+3))
			if err != nil {
				return err
			}
			if !term.Equal(res.Term, next) {
				return fmt.Errorf("n=%d: after %d steps got %s", n, 2*n+3, res.Term)
			}
			if short, _ := cfg.Reduce(start, lo, uint64(2*n+2)); term.Equal(short.Term, next) {
				return fmt.Errorf("n=%d: target reached early", n)
			}
			idn = term.App(parse("id"), idn)
		}
		return nil
	})

	add("M M grows without repeating", "Section 5.4", func() error {
		start := parse("m m")
		res, err := checked(reduce.DefaultConfig(), start, lo, 300)
		if err != nil {
			return err
		}
		if !res.Exhausted || res.Term.Size() <= start.Size() {
			return fmt.Errorf("no growth: %d -> %d", start.Size(), res.Term.Size())
		}
		return nil
	})

	add("size laws: K drops 2+|y|, S adds |z|", "Section 5", func() error {
		cfg := reduce.DefaultConfig()
		x := parse("K a (f b c)")
		r, _ := cfg.Reduce(x, lo, 1)
		if x.Size()-r.Term.Size() != 2+2 {
			return fmt.Errorf("K changed size by %d", r.Term.Size()-x.Size())
		}
		y := parse("S f g (h i j)")
		r, _ = cfg.Reduce(y, lo, 1)
		if r.Term.Size()-y.Size() != 2 {
			return fmt.Errorf("S changed size by %d", r.Term.Size()-y.Size())
		}
		return nil
	})

	// Theorem 4.2: pi(root) not contained in pi(l) gives two normal forms
	// for <a>_l u with u built from a root-permitted constant.
	for _, c := range []struct{ name, src string }{
		{"K", "<a>_l (K b c)"},
		{"W", "<a>_l (W g y)"},
		{"C", "<a>_l (C g y z)"},
	} {
		c := c
		add("Theorem 4.2 counterexample with "+c.name+" under unrestricted Abs", "Theorem 4.2", func() error {
			cfg := reduce.DefaultConfig()
			cfg.Perm = map[string]reduce.PermSet{"l": 0}
			cfg.Absorb = reduce.AbsAny
			nfs := map[string]bool{}
			for _, s := range allStrategies {
				res, err := cfg.Reduce(parse(c.src), s, 50)
				if err != nil || res.Exhausted {
					return fmt.Errorf("%s: %v", s, err)
				}
				nfs[res.Term.String()] = true
			}
			if len(nfs) != 2 {
				return fmt.Errorf("want exactly two normal forms, got %v", nfs)
			}
			return nil
		})
		add("Theorem 4.9 repair with "+c.name+" under AbsU", "Theorem 4.9", func() error {
			cfg := reduce.DefaultConfig()
			cfg.Perm = map[string]reduce.PermSet{"l": 0}
			nfs := map[string]bool{}
			for _, s := range allStrategies {
				res, err := checked(cfg, parse(c.src), s, 50)
				if err != nil {
					return err
				}
				nfs[res.Term.String()] = true
			}
			if len(nfs) != 1 {
				return fmt.Errorf("want one normal form, got %v", nfs)
			}
			return nil
		})
	}

	add("Theorem 4.2 for Sc: sc_l (K b c)", "Theorem 4.2", func() error {
		cfg := reduce.DefaultConfig()
		cfg.Perm = map[string]reduce.PermSet{"l": 0}
		nfs := map[string]bool{}
		for _, s := range allStrategies {
			res, err := cfg.Reduce(parse("sc_l (K b c)"), s, 50)
			if err != nil {
				return err
			}
			nfs[res.Term.String()] = true
		}
		if len(nfs) != 2 {
			return fmt.Errorf("want two normal forms, got %v", nfs)
		}
		return nil
	})

	add("Theorem 4.1: core system is confluent for any permission structure", "Theorem 4.1", func() error {
		cfg := reduce.DefaultConfig()
		cfg.Perm = map[string]reduce.PermSet{"l": reduce.PermK, "m": 0}
		cfg.Absorb = reduce.AbsOff
		src := "<S (K a) K b (I c)>_l <W K (K d e)>_m (B f g x)"
		nfs := map[string]bool{}
		for _, s := range allStrategies {
			res, err := checked(cfg, parse(src), s, 200)
			if err != nil || res.Exhausted {
				return fmt.Errorf("%s: %v", s, err)
			}
			nfs[res.Term.String()] = true
		}
		if len(nfs) != 1 {
			return fmt.Errorf("normal forms %v", nfs)
		}
		return nil
	})

	return vs
}

// Run executes every vector, writes one line per vector to w, and returns the
// number that passed and failed.
func Run(w io.Writer) (passed, failed int) {
	for _, v := range All() {
		if err := safely(v.Run); err != nil {
			failed++
			fmt.Fprintf(w, "FAIL  %-62s [%s]\n      %v\n", v.Name, v.Ref, err)
			continue
		}
		passed++
		fmt.Fprintf(w, "ok    %-62s [%s]\n", v.Name, v.Ref)
	}
	return passed, failed
}

func safely(f func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return f()
}

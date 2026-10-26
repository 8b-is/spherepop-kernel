package reduce

import (
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/standardgalactic/spherepop-kernel/term"
)

// This file replaces sampling with enumeration. For every term up to a given
// number of leaves and brackets, and every assignment of permissions to the
// labels it uses, it explores every reduction path and collects the normal
// forms reached. Two distinct normal forms are a genuine counterexample to
// confluence whether or not the system terminates. When exploration is cut
// off by the cap the term is skipped and counted, never passed.

type searchSpec struct {
	leaves, brackets int
	labels           []string
	symbols          []func() *term.Term
}

func sym(name string) func() *term.Term {
	switch name {
	case "a", "b":
		return func() *term.Term { return term.Atom(name) }
	case "sc_x":
		return func() *term.Term { return term.Scope("x") }
	case "sc_y":
		return func() *term.Term { return term.Scope("y") }
	}
	return func() *term.Term { return term.Const(name) }
}

// enumerate streams every term with exactly n leaves and at most br brackets.
func (s searchSpec) enumerate(n, br int, emit func(*term.Term)) {
	if n == 1 {
		for _, f := range s.symbols {
			emit(f())
		}
	}
	for i := 1; i < n; i++ {
		for b1 := 0; b1 <= br; b1++ {
			s.enumerate(i, b1, func(l *term.Term) {
				s.enumerate(n-i, br-b1, func(r *term.Term) { emit(term.App(l, r)) })
			})
		}
	}
	if br > 0 {
		for _, l := range s.labels {
			s.enumerate(n, br-1, func(x *term.Term) { emit(term.Bracket(l, x)) })
		}
	}
}

// normalForms explores all reduction paths from t. ok is false if the cap on
// states or term size was hit.
func allNormalForms(c *Config, t *term.Term, maxStates, maxSize int) (nfs map[term.Digest]*term.Term, ok bool) {
	nfs = map[term.Digest]*term.Term{}
	seen := map[term.Digest]bool{t.Digest(): true}
	stack := []*term.Term{t}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		rs := c.Redexes(cur, LeftmostOutermost)
		if len(rs) == 0 {
			nfs[cur.Digest()] = cur
			continue
		}
		for _, r := range rs {
			next, err := c.Apply(cur, r.Path, r.Rule)
			if err != nil {
				panic(err)
			}
			if next.Size() > maxSize {
				return nfs, false
			}
			if !seen[next.Digest()] {
				if len(seen) >= maxStates {
					return nfs, false
				}
				seen[next.Digest()] = true
				stack = append(stack, next)
			}
		}
	}
	return nfs, true
}

// permutations of permission structures over the labels a term uses.
func structures(labels []string, f func(map[string]PermSet)) {
	m := map[string]PermSet{}
	var rec func(i int)
	rec = func(i int) {
		if i == len(labels) {
			f(m)
			return
		}
		for p := PermSet(0); p < 8; p++ {
			m[labels[i]] = p // subsets of {W,K,C}
			rec(i + 1)
		}
	}
	rec(0)
}

type searchResult struct {
	terms, explored, capped int
	counter                 []string
}

func search(spec searchSpec, mode Absorption, sc ScopeMode, limit int) searchResult {
	var res searchResult
	for n := 1; n <= spec.leaves; n++ {
		spec.enumerate(n, spec.brackets, func(t *term.Term) {
			if len(res.counter) >= limit || len(t.Labels()) == 0 {
				return
			}
			res.terms++
			structures(t.Labels(), func(perm map[string]PermSet) {
				if len(res.counter) >= limit {
					return
				}
				c := DefaultConfig()
				c.Trace = false
				c.Root = PermW | PermK | PermC
				c.Perm = map[string]PermSet{}
				for k, v := range perm {
					c.Perm[k] = v
				}
				c.Absorb, c.Sc = mode, sc
				nfs, ok := allNormalForms(&c, t, 600, 40)
				res.explored++
				if !ok {
					res.capped++
				}
				if len(nfs) > 1 {
					res.counter = append(res.counter, t.String()+"  under "+describe(c))
				}
			})
		})
	}
	return res
}

// controlSpec is just large enough to contain the essay's counterexamples,
// which have four leaves: <a>_l (K b c) and sc_l (K b c).
func controlSpec() searchSpec {
	spec := searchSpec{leaves: 4, brackets: 1, labels: []string{"x"}}
	for _, n := range []string{"a", "b", "K", "sc_x"} {
		spec.symbols = append(spec.symbols, sym(n))
	}
	return spec
}

func defaultSpec() searchSpec {
	spec := searchSpec{leaves: 4, brackets: 1, labels: []string{"x"}}
	for _, n := range []string{"a", "K", "C", "W", "I", "sc_x"} {
		spec.symbols = append(spec.symbols, sym(n))
	}
	if v, err := strconv.Atoi(os.Getenv("SPHEREPOP_SEARCH_LEAVES")); err == nil {
		spec.leaves = v
	}
	if v, err := strconv.Atoi(os.Getenv("SPHEREPOP_SEARCH_BRACKETS")); err == nil {
		spec.brackets = v
	}
	if os.Getenv("SPHEREPOP_SEARCH_LABELS") == "2" {
		spec.labels = []string{"x", "y"}
		spec.symbols = append(spec.symbols, sym("sc_y"))
	}
	return spec
}

// Positive control: the essay's Sc must be found non-confluent, or the
// enumeration proves nothing.
func TestExhaustiveFindsEssayScDivergence(t *testing.T) {
	r := search(controlSpec(), AbsU, ScAny, 1)
	if len(r.counter) == 0 {
		t.Fatalf("enumeration missed the known failure (terms=%d)", r.terms)
	}
	t.Logf("smallest counterexample for Sc as stated: %s", r.counter[0])
}

func TestExhaustiveFindsUnrestrictedAbsDivergence(t *testing.T) {
	r := search(controlSpec(), AbsAny, ScSafe, 1)
	if len(r.counter) == 0 {
		t.Fatal("enumeration missed unrestricted absorption divergence")
	}
	t.Logf("smallest counterexample for Abs=any: %s", r.counter[0])
}

func TestExhaustiveSafeModes(t *testing.T) {
	for _, tc := range []struct {
		abs Absorption
		sc  ScopeMode
	}{{AbsU, ScSafe}, {AbsS, ScSafe}, {AbsOff, ScSafe}, {AbsU, ScAny}} {
		if tc.sc == ScAny {
			continue // covered by the positive control
		}
		start := time.Now()
		r := search(defaultSpec(), tc.abs, tc.sc, 3)
		t.Logf("abs=%s sc=%s: terms=%d explored=%d capped=%d in %s", tc.abs, tc.sc, r.terms, r.explored, r.capped, time.Since(start).Round(time.Millisecond))
		for _, c := range r.counter {
			t.Errorf("COUNTEREXAMPLE abs=%s sc=%s: %s", tc.abs, tc.sc, c)
		}
	}
}

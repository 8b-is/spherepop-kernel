package reduce

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/standardgalactic/spherepop-kernel/term"
)

type gen struct{ r *rand.Rand }

var (
	genAtoms  = []string{"a", "b", "c"}
	genConsts = []string{"I", "B", "C", "K", "W"}
	genLabels = []string{"x", "y"}
)

func (g gen) term(depth int) *term.Term {
	r := g.r
	if depth <= 0 || r.Intn(5) == 0 {
		switch r.Intn(7) {
		case 0, 1, 2:
			return term.Atom(genAtoms[r.Intn(len(genAtoms))])
		case 3, 4, 5:
			return term.Const(genConsts[r.Intn(len(genConsts))])
		}
		return term.Scope(genLabels[r.Intn(len(genLabels))])
	}
	if r.Intn(5) == 0 {
		return term.Bracket(genLabels[r.Intn(len(genLabels))], g.term(depth-1))
	}
	return term.App(g.term(depth-1), g.term(depth-1))
}

func (g gen) config(mode Absorption, withScope bool) Config {
	c := DefaultConfig()
	c.Trace = false
	c.Absorb = mode
	c.Perm = map[string]PermSet{}
	for _, l := range genLabels {
		c.Perm[l] = PermSet(g.r.Intn(8)) & (PermW | PermK | PermC)
	}
	if g.r.Intn(2) == 0 {
		c.Root = PermW | PermK | PermC
	} else {
		c.Root = PermSet(g.r.Intn(8))
	}
	return c
}

// normalForms runs the four strategies and several random redex choices and
// returns the set of distinct normal forms reached within the budget.
func normalForms(t *testing.T, r *rand.Rand, c Config, start *term.Term) map[string]bool {
	t.Helper()
	nfs := map[string]bool{}
	for _, s := range []Strategy{LeftmostOutermost, LeftmostInnermost, RightmostInnermost, RightmostOutermost} {
		res, err := c.Reduce(start, s, 300)
		if err != nil {
			t.Fatal(err)
		}
		if !res.Exhausted {
			nfs[res.Term.String()] = true
		}
	}
	for k := 0; k < 4; k++ {
		cur := start
		done := false
		for i := 0; i < 300; i++ {
			rs := c.Redexes(cur, LeftmostOutermost)
			if len(rs) == 0 {
				done = true
				break
			}
			pick := rs[r.Intn(len(rs))]
			next, err := c.Apply(cur, pick.Path, pick.Rule)
			if err != nil {
				t.Fatalf("Redexes offered a step Apply rejects: %v", err)
			}
			cur = next
			if cur.Size() > 400 {
				break
			}
		}
		if done {
			nfs[cur.String()] = true
		}
	}
	return nfs
}

func describe(c Config) string {
	return fmt.Sprintf("root=%s x=%s y=%s absorb=%s", c.Root, c.Perm["x"], c.Perm["y"], c.Absorb)
}

// Theorem 4.1: with no absorption (and no Sc) the core system is confluent
// for every permission structure.
func TestRandomConfluenceCore(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	g := gen{r}
	for i := 0; i < 1500; i++ {
		c := g.config(AbsOff, false)
		start := g.term(5)
		if len(start.Labels()) == 0 || containsScope(start) {
			continue
		}
		if nfs := normalForms(t, r, c, start); len(nfs) > 1 {
			t.Fatalf("core system diverged on %s under %s: %v", start, describe(c), nfs)
		}
	}
}

func containsScope(t *term.Term) bool {
	switch t.Kind() {
	case term.KindScope:
		return true
	case term.KindApp:
		return containsScope(t.Fun()) || containsScope(t.Arg())
	case term.KindBracket:
		return containsScope(t.Body())
	}
	return false
}

// Theorem 4.9 and Theorem 4.7: AbsU and AbsS preserve confluence, here
// without Sc, which the essays cover.
func TestRandomConfluenceRestrictedAbsorption(t *testing.T) {
	for _, mode := range []Absorption{AbsU, AbsS} {
		r := rand.New(rand.NewSource(2))
		g := gen{r}
		checked := 0
		for i := 0; i < 3000; i++ {
			c := g.config(mode, false)
			start := g.term(5)
			if containsScope(start) {
				continue
			}
			checked++
			if nfs := normalForms(t, r, c, start); len(nfs) > 1 {
				t.Fatalf("%s diverged on %s under %s: %v", mode, start, describe(c), nfs)
			}
		}
		if checked < 500 {
			t.Fatalf("generator produced too few scope-free terms: %d", checked)
		}
	}
}

// Theorem 4.2 applies to Sc as well: with the rule as the essay states it,
// restricting Abs does not restore confluence, because sc_l u moves u into
// the chain of l whatever u contains. The random search must find this.
func TestRandomScAnyDiverges(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	g := gen{r}
	for i := 0; i < 3000; i++ {
		c := g.config(AbsU, true)
		c.Sc = ScAny
		if len(normalForms(t, r, c, g.term(5))) > 1 {
			return
		}
	}
	t.Fatal("expected a divergence with unrestricted Sc")
}

// ScSafe is this module's own restriction, not the essays'. This test is the
// only evidence for it: randomized, not proved.
func TestRandomConfluenceWithSafeScope(t *testing.T) {
	for _, mode := range []Absorption{AbsU, AbsS} {
		r := rand.New(rand.NewSource(3))
		g := gen{r}
		for i := 0; i < 3000; i++ {
			c := g.config(mode, true)
			c.Sc = ScSafe
			start := g.term(5)
			if nfs := normalForms(t, r, c, start); len(nfs) > 1 {
				t.Fatalf("%s with Sc diverged on %s under %s: %v", mode, start, describe(c), nfs)
			}
		}
	}
}

// Sanity check on the generator: unrestricted absorption must be able to
// diverge on random input, otherwise the tests above prove nothing.
func TestGeneratorCanFindDivergence(t *testing.T) {
	r := rand.New(rand.NewSource(4))
	g := gen{r}
	found := 0
	for i := 0; i < 3000 && found == 0; i++ {
		c := g.config(AbsAny, false)
		start := g.term(5)
		if containsScope(start) {
			continue
		}
		if len(normalForms(t, r, c, start)) > 1 {
			found++
		}
	}
	if found == 0 {
		t.Fatal("no divergence found under AbsAny; the confluence tests are vacuous")
	}
}

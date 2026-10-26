package reduce

import (
	"testing"

	"github.com/standardgalactic/spherepop-kernel/term"
)

func settledConfig(sc ScopeMode, abs Absorption) *Config {
	c := DefaultConfig()
	c.Absorb, c.Sc = abs, sc
	c.Perm = map[string]PermSet{"x": PermK}
	return &c
}

// The settled mode refuses an argument with a pending boundary step even when
// the argument is insensitive; the safe mode accepts it.
func TestSettledScopeWaitsForPendingBoundarySteps(t *testing.T) {
	tm := term.MustParse("sc_x (<I>_x a)")
	safe := settledConfig(ScSafe, AbsS).Redexes(tm, LeftmostOutermost)
	if len(safe) != 2 {
		t.Fatalf("safe mode should offer Sc and the inner Abs, got %v", safe)
	}
	settled := settledConfig(ScSettled, AbsS).Redexes(tm, LeftmostOutermost)
	if len(settled) != 1 || settled[0].Rule != RuleAbs || settled[0].Path != "R" {
		t.Fatalf("settled mode should offer only the inner Abs, got %v", settled)
	}
}

func TestSettledScopeStillReachesTheSameNormalForm(t *testing.T) {
	tm := term.MustParse("sc_x (<I>_x a)")
	for _, sc := range []ScopeMode{ScSafe, ScSettled} {
		c := settledConfig(sc, AbsS)
		nfs, ok := allNormalForms(c, tm, 100, 40)
		if !ok || len(nfs) != 1 {
			t.Fatalf("%s: normal forms %v ok=%v", sc, nfs, ok)
		}
		for _, nf := range nfs {
			if nf.String() != "<<a>_x>_x" {
				t.Fatalf("%s: got %s", sc, nf)
			}
		}
	}
}

// Settledness is the property the confluence proof uses: a settled term has no
// step of any kind inside it, at its own chain or at any smaller one.
func TestSettledTermsHaveNoStepsAtAnySmallerChain(t *testing.T) {
	spec := searchSpec{leaves: 3, brackets: 2, labels: []string{"x", "y"}}
	for _, n := range []string{"a", "K", "C", "W", "I", "sc_x", "sc_y"} {
		spec.symbols = append(spec.symbols, sym(n))
	}
	checked := 0
	for n := 1; n <= spec.leaves; n++ {
		spec.enumerate(n, spec.brackets, func(v *term.Term) {
			structures([]string{"x", "y"}, func(perm map[string]PermSet) {
				c := DefaultConfig()
				c.Absorb, c.Sc = AbsAny, ScAny // the widest set of steps
				c.Perm = map[string]PermSet{"x": perm["x"], "y": perm["y"]}
				s := newSession(&c)
				for m := PermSet(0); m < 8; m++ {
					if !s.isSettled(v, m) {
						continue
					}
					for sub := PermSet(0); sub < 8; sub++ {
						if sub&m != sub {
							continue
						}
						c.Root = sub
						if rs := c.Redexes(v, LeftmostOutermost); len(rs) != 0 {
							t.Fatalf("%s is settled at %s but has steps %v at %s", v, m, rs, sub)
						}
						checked++
					}
				}
			})
		})
	}
	if checked == 0 {
		t.Fatal("no settled term was checked")
	}
}

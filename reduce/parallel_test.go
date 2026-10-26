package reduce

import (
	"fmt"
	"os"
	"strconv"
	"testing"

	"github.com/standardgalactic/spherepop-kernel/term"
)

// Parallel reduction in the sense of Tait and Martin-Lof, adapted to the
// gated rules. t => u contracts any set of non-overlapping redexes of t at
// once, nested ones included: the arguments of a contracted redex are first
// reduced in parallel as well. Every side condition (permission, absorption,
// scope) is evaluated on t, the term before the step. Then
//
//	-> is contained in =>, and => is contained in ->*,
//
// so if => has the diamond property, -> is confluent, whether or not terms
// terminate. The tests check the diamond on every small term, under every
// permission structure, for every pair of parallel successors.
//
// Findings recorded here (see SPEC.md section 17):
//
//	Abs settled, Sc settled: the diamond holds on everything searched.
//	Abs off, Sc settled: the same.
//	Sc safe or Abs u: the diamond fails at terms such as sc_x (<I>_x a) and
//	<a>_x (<I>_x a), where a pending boundary step inside an insensitive
//	argument creates a contraction redex that blocks the outer step. This is
//	a limit of the single-round parallel step as a proof method. Local
//	confluence of the full relation still holds there (peaks_test.go).
//
// Two things in this file are deliberately not part of the evidence: the
// diamond check cannot see termination, and it is a bounded search.

type parKey struct {
	d    term.Digest
	mask PermSet
}

type parCtx struct {
	c    *Config
	s    *session
	memo map[parKey]map[term.Digest]*term.Term
	cap  int
	over bool
}

func newParCtx(c *Config, cap int) *parCtx {
	return &parCtx{c: c, s: newSession(c), memo: map[parKey]map[term.Digest]*term.Term{}, cap: cap}
}

// par returns every t' with t => t' at the chain whose permission is mask.
func (p *parCtx) par(t *term.Term, mask PermSet) map[term.Digest]*term.Term {
	k := parKey{t.Digest(), mask}
	if m, ok := p.memo[k]; ok {
		return m
	}
	out := map[term.Digest]*term.Term{}
	add := func(x *term.Term) {
		if len(out) >= p.cap {
			p.over = true
			return
		}
		out[x.Digest()] = x
	}
	switch t.Kind() {
	case term.KindApp:
		for _, f := range p.par(t.Fun(), mask) {
			for _, x := range p.par(t.Arg(), mask) {
				add(term.App(f, x))
			}
		}
		if id, ok := p.s.redexAt(t, mask); ok {
			switch id {
			case RuleSc:
				inner := mask & p.c.perm(t.Fun().Label())
				for _, u := range p.par(t.Arg(), inner) {
					add(term.Bracket(t.Fun().Label(), u))
				}
			case RuleAbs:
				br := t.Fun()
				inner := mask & p.c.perm(br.Label())
				for _, u := range p.par(br.Body(), inner) {
					for _, v := range p.par(t.Arg(), inner) {
						add(term.Bracket(br.Label(), term.App(u, v)))
					}
				}
			default:
				rule, _ := Lookup(id)
				_, args := t.Spine()
				sets := make([][]*term.Term, len(args))
				for i, a := range args {
					for _, x := range p.par(a, mask) {
						sets[i] = append(sets[i], x)
					}
				}
				var rec func(i int, cur []*term.Term)
				rec = func(i int, cur []*term.Term) {
					if i == len(sets) {
						add(rule.contract(append([]*term.Term(nil), cur...)))
						return
					}
					for _, x := range sets[i] {
						rec(i+1, append(cur, x))
					}
				}
				rec(0, nil)
			}
		}
	case term.KindBracket:
		for _, b := range p.par(t.Body(), mask&p.c.perm(t.Label())) {
			add(term.Bracket(t.Label(), b))
		}
	default:
		add(t)
	}
	p.memo[k] = out
	return out
}

type diamondStats struct {
	terms, checked, capped, failed int
	failing                        map[string]bool // terms with at least one failing pair, up to a limit
	examples                       []string
}

func scanDiamond(spec searchSpec, mode Absorption, sc ScopeMode) diamondStats {
	st := diamondStats{failing: map[string]bool{}}
	for n := 1; n <= spec.leaves; n++ {
		spec.enumerate(n, spec.brackets, func(t *term.Term) {
			if len(t.Labels()) == 0 {
				return
			}
			st.terms++
			structures(t.Labels(), func(perm map[string]PermSet) {
				c := DefaultConfig()
				c.Trace = false
				c.Root = PermW | PermK | PermC
				c.Perm = map[string]PermSet{}
				for k, v := range perm {
					c.Perm[k] = v
				}
				c.Absorb, c.Sc = mode, sc
				p := newParCtx(&c, 2000)
				ts := p.par(t, c.Root)
				if p.over {
					st.capped++
					return
				}
				all := make([]*term.Term, 0, len(ts))
				for _, x := range ts {
					all = append(all, x)
				}
				for i := 0; i < len(all); i++ {
					for j := i + 1; j < len(all); j++ {
						st.checked++
						a, b := p.par(all[i], c.Root), p.par(all[j], c.Root)
						if p.over {
							st.capped++
							p.over = false
							continue
						}
						ok := false
						for d := range a {
							if _, hit := b[d]; hit {
								ok = true
								break
							}
						}
						if !ok {
							st.failed++
							if len(st.failing) < 1000 {
								st.failing[t.String()] = true
							}
							if len(st.examples) < 6 {
								st.examples = append(st.examples, fmt.Sprintf("%s  =>  %s  and  %s  under %s", t, all[i], all[j], describe(c)))
							}
						}
					}
				}
			})
		})
	}
	return st
}

func (st diamondStats) log(t *testing.T, name string) {
	t.Helper()
	t.Logf("%s: terms=%d pairs=%d capped=%d DIAMOND FAILURES=%d", name, st.terms, st.checked, st.capped, st.failed)
	for _, e := range st.examples {
		t.Logf("    %s", e)
	}
}

// scopeFree is the spec without scope constructors, with the given bounds.
func scopeFree(leaves, brackets int) searchSpec {
	spec := searchSpec{leaves: leaves, brackets: brackets, labels: []string{"x"}}
	for _, n := range []string{"a", "K", "C", "W", "I"} {
		spec.symbols = append(spec.symbols, sym(n))
	}
	return spec
}

// settledSpec reads the bounds the long runs set in the environment and
// otherwise uses a size that finishes in a few seconds.
func settledSpec() searchSpec {
	spec := defaultSpec()
	if os.Getenv("SPHEREPOP_SEARCH_LEAVES") == "" {
		spec.leaves = 4
	}
	if os.Getenv("SPHEREPOP_SEARCH_BRACKETS") == "" {
		spec.brackets = 1
	}
	return spec
}

// Positive control: the check must fail where the calculus is known to fail.
// (Weakening the settled condition to plain R-normality, by editing scopes,
// produces 37440 failures at four leaves and two brackets; that mutation was
// run by hand and is not kept as code.)
func TestParallelDiamondControls(t *testing.T) {
	if st := scanDiamond(controlSpec(), AbsU, ScAny); st.failed == 0 {
		t.Fatalf("the diamond check missed the essay's Sc: %+v", st)
	}
}

// The theorem: both boundary rules require a settled argument. The search
// is the evidence; SPEC.md section 17 has the proof.
func TestParallelDiamondSettled(t *testing.T) {
	spec := settledSpec()
	for _, m := range []struct {
		name string
		abs  Absorption
	}{{"s", AbsS}, {"off", AbsOff}} {
		st := scanDiamond(spec, m.abs, ScSettled)
		st.log(t, "abs="+m.name+" sc=settled")
		if st.failed != 0 || st.capped != 0 {
			t.Errorf("abs=%s sc=settled: failures=%d capped=%d", m.name, st.failed, st.capped)
		}
	}
}

// Without Sc, the settled Abs satisfies the diamond and the "settled or
// insensitive" Abs does not once brackets can nest.
func TestParallelDiamondAbsorptionAlone(t *testing.T) {
	spec := scopeFree(3, 2)
	if v, err := strconv.Atoi(os.Getenv("SPHEREPOP_SEARCH_LEAVES")); err == nil {
		spec.leaves = v
	}
	s := scanDiamond(spec, AbsS, ScSafe)
	s.log(t, "no sc, abs=s")
	if s.failed != 0 || s.capped != 0 {
		t.Errorf("abs=s without scope: failures=%d capped=%d", s.failed, s.capped)
	}
	u := scanDiamond(spec, AbsU, ScSafe)
	u.log(t, "no sc, abs=u")
	if !u.failing["<a>_x (<I>_x a)"] {
		t.Error("the known failing pattern of abs=u was not found")
	}
}

// Recorded limitation: with Sc in its safe form the single-round parallel
// step has no diamond, and the failures are the documented pattern.
func TestParallelDiamondSafeScopeLimitation(t *testing.T) {
	spec := settledSpec()
	st := scanDiamond(spec, AbsS, ScSafe)
	st.log(t, "abs=s sc=safe")
	if !st.failing["sc_x (<I>_x a)"] {
		t.Error("the known failing pattern of safe Sc was not found")
	}
	off := scanDiamond(spec, AbsOff, ScSafe)
	off.log(t, "abs=off sc=safe")
	if off.failed != 0 {
		t.Errorf("abs=off sc=safe should satisfy the diamond, got %d failures", off.failed)
	}
}

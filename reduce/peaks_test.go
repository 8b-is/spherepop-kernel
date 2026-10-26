package reduce

import (
	"fmt"
	"os"
	"strconv"
	"testing"

	"github.com/standardgalactic/spherepop-kernel/term"
)

// Local peaks. A peak is a term t with two distinct steps t -> u and t -> v.
// The normal-form search in exhaustive_test.go can only see a failure of
// confluence if both branches terminate. This file checks the local diagrams
// themselves, which also covers terms whose reduction does not terminate.
//
// Steps are split into R (the contractions) and B (Abs and Sc). For each peak
// the checker looks for the join that a proof would need:
//
//	R/R: u ->R* w and v ->R* w
//	B/B: u ->* w and v ->* w, with either kind of step; B alone is not
//	     confluent because a boundary step can wait for a contraction
//	R/B: with t -R-> u and t -B-> v, u ->B* w and v ->R* w   (commutation)
//
// and records how long the shortest join is. A join is searched to a depth and
// a state cap. If both sides close off finitely without meeting, the peak is a
// definitive failure of the diagram. If a side is still growing, the peak is
// reported as undetermined, never as a pass.

type kind uint8

const (
	kindR kind = iota
	kindB
)

func kindOf(id RuleID) kind {
	if id == RuleAbs || id == RuleSc {
		return kindB
	}
	return kindR
}

// reach returns terms reachable from t using only steps whose kind is allowed,
// by distance. complete is true if the whole reachable set was enumerated.
func reach(c *Config, t *term.Term, allowed func(kind) bool, depth, maxStates, maxSize int) (dist map[term.Digest]int, terms map[term.Digest]*term.Term, complete bool) {
	dist = map[term.Digest]int{t.Digest(): 0}
	terms = map[term.Digest]*term.Term{t.Digest(): t}
	frontier := []*term.Term{t}
	complete = true
	for d := 0; d < depth && len(frontier) > 0; d++ {
		var next []*term.Term
		for _, cur := range frontier {
			for _, r := range c.Redexes(cur, LeftmostOutermost) {
				if !allowed(kindOf(r.Rule)) {
					continue
				}
				n, err := c.Apply(cur, r.Path, r.Rule)
				if err != nil {
					panic(err)
				}
				if n.Size() > maxSize || len(dist) >= maxStates {
					complete = false
					continue
				}
				if _, ok := dist[n.Digest()]; !ok {
					dist[n.Digest()] = d + 1
					terms[n.Digest()] = n
					next = append(next, n)
				}
			}
		}
		frontier = next
	}
	if len(frontier) > 0 {
		complete = false
	}
	return
}

type joinResult struct {
	joined     bool
	du, dv     int  // shortest lengths of the two legs, minimizing du+dv
	definitive bool // both sides fully enumerated, no common term
}

func joinWith(c *Config, u, v *term.Term, allowU, allowV func(kind) bool) joinResult {
	const depth, states, size = 8, 400, 40
	du, tu, cu := reach(c, u, allowU, depth, states, size)
	dv, _, cv := reach(c, v, allowV, depth, states, size)
	best := joinResult{}
	for d, a := range du {
		if b, ok := dv[d]; ok && (!best.joined || a+b < best.du+best.dv) {
			best = joinResult{joined: true, du: a, dv: b}
		}
	}
	_ = tu
	if !best.joined && cu && cv {
		best.definitive = true
	}
	return best
}

func isR(k kind) bool  { return k == kindR }
func isB(k kind) bool  { return k == kindB }
func either(kind) bool { return true }

type peakStats struct {
	peaks, joined, undetermined, failed int
	maxLeg                              [2]int // largest shortest-join legs seen: [0]=leg on the first term, [1]=on the second
	maxVLegR                            int    // R/B: longest v ->R* leg needed (min over joins)
	examples                            []string
}

func (s *peakStats) note(msg string) {
	if len(s.examples) < 6 {
		s.examples = append(s.examples, msg)
	}
}

// scanPeaks checks every peak of every term in the spec under every
// permission structure, for one configuration of the modes.
func scanPeaks(spec searchSpec, mode Absorption, sc ScopeMode) map[string]*peakStats {
	stats := map[string]*peakStats{"R/R": {}, "B/B": {}, "R/B": {}}
	for n := 1; n <= spec.leaves; n++ {
		spec.enumerate(n, spec.brackets, func(t *term.Term) {
			if len(t.Labels()) == 0 {
				return
			}
			structures(t.Labels(), func(perm map[string]PermSet) {
				c := DefaultConfig()
				c.Trace = false
				c.Root = PermW | PermK | PermC
				c.Perm = map[string]PermSet{}
				for k, v := range perm {
					c.Perm[k] = v
				}
				c.Absorb, c.Sc = mode, sc
				rs := c.Redexes(t, LeftmostOutermost)
				for i := 0; i < len(rs); i++ {
					for j := i + 1; j < len(rs); j++ {
						ui, _ := c.Apply(t, rs[i].Path, rs[i].Rule)
						vj, _ := c.Apply(t, rs[j].Path, rs[j].Rule)
						if term.Equal(ui, vj) {
							continue
						}
						ki, kj := kindOf(rs[i].Rule), kindOf(rs[j].Rule)
						var key string
						var res joinResult
						switch {
						case ki == kindR && kj == kindR:
							key, res = "R/R", joinWith(&c, ui, vj, isR, isR)
						case ki == kindB && kj == kindB:
							key, res = "B/B", joinWith(&c, ui, vj, either, either)
						default:
							// order so the first step is R and the second is B
							ru, bv := ui, vj
							if ki == kindB {
								ru, bv = vj, ui
							}
							key, res = "R/B", joinWith(&c, ru, bv, isB, isR)
						}
						st := stats[key]
						st.peaks++
						switch {
						case res.joined:
							st.joined++
							if res.du > st.maxLeg[0] {
								st.maxLeg[0] = res.du
							}
							if res.dv > st.maxLeg[1] {
								st.maxLeg[1] = res.dv
							}
						case res.definitive:
							st.failed++
							st.note(fmt.Sprintf("%s  [%s %s | %s %s]  under %s", t, rs[i].Rule, rs[i].Path, rs[j].Rule, rs[j].Path, describe(c)))
						default:
							st.undetermined++
							st.note("undetermined: " + t.String() + "  under " + describe(c))
						}
					}
				}
			})
		})
	}
	return stats
}

func peakSpec() searchSpec {
	spec := defaultSpec()
	if os.Getenv("SPHEREPOP_SEARCH_LEAVES") == "" {
		spec.leaves = 4
	}
	return spec
}

// Positive control: with the essay's Sc the R/B diagram must fail definitively.
func TestPeaksFindEssayScFailure(t *testing.T) {
	st := scanPeaks(controlSpec(), AbsU, ScAny)
	if st["R/B"].failed == 0 {
		t.Fatalf("the essay's Sc peak sc_x (K a a) was not reported: %+v", st["R/B"])
	}
}

// Local confluence of the full relation, for the settled combination and for
// the two modes that the parallel diamond does not cover. A peak that does not
// join within the bounds, or that cannot be decided, fails the test.
func TestPeaksFullRelation(t *testing.T) {
	spec := settledSpec()
	for _, m := range []struct {
		name string
		abs  Absorption
		sc   ScopeMode
	}{{"s", AbsS, ScSettled}, {"u", AbsU, ScSafe}, {"u", AbsU, ScSettled}, {"s", AbsS, ScSafe}} {
		st := scanPeaks(spec, m.abs, m.sc)
		for _, key := range []string{"R/R", "B/B", "R/B"} {
			s := st[key]
			t.Logf("abs=%s sc=%s %s: peaks=%d joined=%d undetermined=%d FAILED=%d maxlegs=%v", m.name, m.sc, key, s.peaks, s.joined, s.undetermined, s.failed, s.maxLeg)
			if s.failed != 0 || s.undetermined != 0 {
				t.Errorf("abs=%s sc=%s %s: failed=%d undetermined=%d, e.g. %v", m.name, m.sc, key, s.failed, s.undetermined, s.examples)
			}
		}
	}
}

func TestPeaksSafeModes(t *testing.T) {
	limit := 0
	if v, err := strconv.Atoi(os.Getenv("SPHEREPOP_PEAKS_FAIL_OK")); err == nil {
		limit = v
	}
	for _, m := range []struct {
		name string
		abs  Absorption
	}{{"u", AbsU}, {"s", AbsS}, {"off", AbsOff}} {
		st := scanPeaks(peakSpec(), m.abs, ScSafe)
		for _, key := range []string{"R/R", "B/B", "R/B"} {
			s := st[key]
			t.Logf("abs=%s sc=safe %s: peaks=%d joined=%d undetermined=%d FAILED=%d maxlegs=%v", m.name, key, s.peaks, s.joined, s.undetermined, s.failed, s.maxLeg)
			for _, e := range s.examples {
				t.Logf("    %s", e)
			}
			if s.failed > limit {
				t.Errorf("abs=%s %s: %d peaks with no join", m.name, key, s.failed)
			}
		}
	}
}

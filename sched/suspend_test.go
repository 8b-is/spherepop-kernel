package sched

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/standardgalactic/spherepop-kernel/reduce"
	"github.com/standardgalactic/spherepop-kernel/term"
	"github.com/standardgalactic/spherepop-kernel/trace"
)

var allStrategies = []reduce.Strategy{reduce.LeftmostOutermost, reduce.LeftmostInnermost, reduce.RightmostInnermost, reduce.RightmostOutermost}

func runUntilDone(t *testing.T, cfg reduce.Config, strat reduce.Strategy, budget uint64, src string, maxTicks int) *World {
	t.Helper()
	w, err := New(cfg, Policy{Strategy: strat, Budget: budget}, []Init{{ID: "e", Term: term.MustParse(src)}})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxTicks && !w.Done(); i++ {
		if err := w.Tick(); err != nil {
			t.Fatal(err)
		}
	}
	return w
}

func state(w *World) EntityState { return w.States()[0] }

func TestSuspensionIsAnOuterBoundaryAndNotAFuelStatus(t *testing.T) {
	cfg := HostConfig()
	w := runUntilDone(t, cfg, reduce.LeftmostOutermost, 10, "yield (K a b)", 1)
	st := state(w)
	if !st.Suspended || st.Normal || st.Term != "yield (K a b)" {
		t.Fatalf("after tick 1: %+v", st)
	}
	ev := w.Log().Events[0]
	if len(ev.Slice.Steps) != 0 || ev.Slice.Exhausted || ev.Resumed {
		t.Fatalf("suspending at the start took steps or claimed exhaustion: %+v", ev)
	}
	if err := w.Tick(); err != nil {
		t.Fatal(err)
	}
	st = state(w)
	if !st.Normal || st.Suspended || st.Term != "a" {
		t.Fatalf("after resuming: %+v", st)
	}
	if ev := w.Log().Events[1]; !ev.Resumed || ev.Slice.Start != "K a b" {
		t.Fatalf("resume event: %+v", ev)
	}
	if err := CheckLog(w.Log()); err != nil {
		t.Fatal(err)
	}
}

func TestYieldInsideAnArgumentThatIsDiscardedNeverSuspends(t *testing.T) {
	// K x (yield y) discards the yield. An eager reading of "yield anywhere"
	// would stop here; the root-only boundary does not, under any strategy.
	for _, s := range allStrategies {
		w := runUntilDone(t, HostConfig(), s, 10, "K x (yield y)", 3)
		st := state(w)
		if !st.Normal || st.Suspended || st.Term != "x" {
			t.Fatalf("%s: %+v", s, st)
		}
		for _, ev := range w.Log().Events {
			if ev.Resumed {
				t.Fatalf("%s: resumed without a suspension", s)
			}
		}
	}
}

func TestSuspensionAtTheRootReachedByReduction(t *testing.T) {
	// I (yield (K a b)): the root becomes (yield ..) after one step under lo,
	// leaving the argument unreduced; innermost strategies reduce it first.
	w := runUntilDone(t, HostConfig(), reduce.LeftmostOutermost, 10, "I (yield (K a b))", 1)
	if st := state(w); !st.Suspended || st.Term != "yield (K a b)" {
		t.Fatalf("lo: %+v", st)
	}
	w = runUntilDone(t, HostConfig(), reduce.LeftmostInnermost, 10, "I (yield (K a b))", 1)
	if st := state(w); !st.Suspended || st.Term != "yield a" {
		t.Fatalf("li: %+v", st)
	}
	for _, w := range []*World{w} {
		if err := CheckLog(w.Log()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFuelExhaustionAndSuspensionAreDifferentOutcomes(t *testing.T) {
	w := runUntilDone(t, HostConfig(), reduce.LeftmostOutermost, 1, "I (I (I (yield z)))", 1)
	st := state(w)
	if st.Suspended || st.Normal || !w.Log().Events[0].Slice.Exhausted {
		t.Fatalf("one step of three: %+v", st)
	}
	w = runUntilDone(t, HostConfig(), reduce.LeftmostOutermost, 3, "I (I (I (yield z)))", 1)
	st = state(w)
	if !st.Suspended || w.Log().Events[0].Slice.Exhausted {
		t.Fatalf("budget spent exactly as the root became a yield must suspend, not exhaust: %+v", st)
	}
}

func TestRearmIsHostInputAndResumeIsNot(t *testing.T) {
	w := runUntilDone(t, HostConfig(), reduce.LeftmostOutermost, 10, "yield (K a b)", 1)
	if err := w.Rearm("e", term.MustParse("K c d")); err != nil {
		t.Fatal(err)
	}
	if err := w.Tick(); err != nil {
		t.Fatal(err)
	}
	ev := w.Log().Events[1]
	if ev.Resumed || ev.Rearm == nil || !ev.Rearm.Preempted || state(w).Term != "c" {
		t.Fatalf("a re-armed suspended entity must be a preempting rearm, not a resume: %+v", ev)
	}
	if err := CheckLog(w.Log()); err != nil {
		t.Fatal(err)
	}
}

func TestLogRejectsForgedResumption(t *testing.T) {
	good := runUntilDone(t, HostConfig(), reduce.LeftmostOutermost, 10, "yield (K a b)", 2).Log()
	if err := CheckLog(good); err != nil {
		t.Fatal(err)
	}
	mutants := map[string]func(*Log){
		"resumed flag dropped": func(l *Log) { l.Events[1].Resumed = false },
		"resume of a running entity": func(l *Log) {
			l.Events[0].Resumed = true
		},
		"slice starts at the yield, not its continuation": func(l *Log) {
			s := l.Events[1].Slice
			s.Start, s.StartDigest = good.Events[0].Slice.Final, good.Events[0].Slice.FinalDigest
		},
		"final claims suspended after resuming to normal": func(l *Log) { l.Final[0].Suspended = true },
		"initial carries suspended":                       func(l *Log) { l.Initial[0].Suspended = true },
	}
	for name, mutate := range mutants {
		l := good
		l.Events = append([]Event(nil), good.Events...)
		for i := range l.Events {
			if l.Events[i].Slice != nil {
				c := *l.Events[i].Slice
				l.Events[i].Slice = &c
			}
		}
		l.Initial = append([]EntityState(nil), good.Initial...)
		l.Final = append([]EntityState(nil), good.Final...)
		mutate(&l)
		if err := CheckLog(l); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	pending := runUntilDone(t, HostConfig(), reduce.LeftmostOutermost, 10, "yield (K a b)", 1).Log()
	if err := CheckLog(pending); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Log){
		"final suspended flag dropped": func(l *Log) { l.Final[0].Suspended = false },
		"final claims normal as well":  func(l *Log) { l.Final[0].Normal = true },
		"final normal instead":         func(l *Log) { l.Final[0].Suspended, l.Final[0].Normal = false, true },
	} {
		l := pending
		l.Final = append([]EntityState(nil), pending.Final...)
		mutate(&l)
		if err := CheckLog(l); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// A suspension that is not honoured: the first slice carries on past the yield.
	w, _ := New(HostConfig(), Policy{Strategy: reduce.LeftmostOutermost, Budget: 10}, []Init{{ID: "e", Term: term.MustParse("yield (K a b)")}})
	w.cfg.Trace = true
	_, ignored, _ := recordIgnoringBoundary(w, "yield (K a b)")
	forged := w.Log()
	forged.Events = []Event{{Tick: 0, Entity: "e", Decision: Run, Budget: 10, Slice: &ignored}}
	forged.Final = []EntityState{{ID: "e", Term: ignored.Final, Digest: ignored.FinalDigest, Normal: true}}
	if err := CheckLog(forged); err == nil {
		t.Fatal("a slice that reduced past a root yield was accepted")
	}
}

// Suspending and resuming preserves the result of uninterrupted evaluation,
// under every strategy and every budget, for terms in which yield occurs at
// random positions. Under the settled configuration the calculus is confluent,
// so the uninterrupted normal form, with root yields stripped, is the answer
// every schedule must reach.
func TestSuspendResumeEqualsUninterruptedEvaluation(t *testing.T) {
	cfg := HostConfig()
	rng := rand.New(rand.NewSource(7))
	leaves := []string{"a", "b", "x", "S", "K", "I", "B", "C", "W", "yield", "yield", "K", "I"}
	var gen func(d int) string
	gen = func(d int) string {
		if d == 0 || rng.Intn(5) == 0 {
			return leaves[rng.Intn(len(leaves))]
		}
		return fmt.Sprintf("%s (%s)", gen(d-1), gen(d-1))
	}
	checked, suspended := 0, 0
	for n := 0; n < 600; n++ {
		src := gen(1 + rng.Intn(5))
		start := term.MustParse(src)
		res, err := cfg.Reduce(start, reduce.LeftmostOutermost, 300)
		if err != nil || res.Exhausted {
			continue
		}
		want := res.Term
		for {
			k, ok := Continuation(want)
			if !ok {
				break
			}
			want = k
		}
		for _, s := range allStrategies {
			for _, budget := range []uint64{1, 2, 5, 400} {
				w := runUntilDone(t, cfg, s, budget, src, 2000)
				if !w.Done() {
					t.Fatalf("%s %s budget %d did not finish", src, s, budget)
				}
				got := state(w).Term
				if got != want.String() {
					t.Fatalf("%s under %s with budget %d: chain gives %s, uninterrupted gives %s", src, s, budget, got, want)
				}
				if err := CheckLog(w.Log()); err != nil {
					t.Fatalf("%s %s %d: %v", src, s, budget, err)
				}
				for _, ev := range w.Log().Events {
					if ev.Resumed {
						suspended++
						break
					}
				}
			}
		}
		checked++
	}
	t.Logf("%d terms reached a normal form; %d scheduler runs (%d per term) of which %d resumed at least once",
		checked, checked*len(allStrategies)*4, len(allStrategies)*4, suspended)
	if checked < 100 || suspended < 50 {
		t.Fatalf("the search did not exercise suspension: %d terms, %d runs that resumed", checked, suspended)
	}
}

// recordIgnoringBoundary records a slice that does not stop at a root yield.
func recordIgnoringBoundary(w *World, src string) (reduce.Result, trace.Document, error) {
	return trace.Record(w.cfg, term.MustParse(src), w.pol.Strategy, w.pol.Budget)
}

// Result equivalence is not history equivalence. Two strategies reach the same
// final term from the same start, yet their scheduler histories differ: they
// suspend at different moments with different residual continuations, and they
// spend different amounts of fuel before and after the boundary.
func TestSameResultDifferentSuspensionHistory(t *testing.T) {
	const src = "I (yield (K a b))"
	type hist struct {
		residual string
		before   int
		after    int
		final    string
	}
	run := func(s reduce.Strategy) hist {
		w := runUntilDone(t, HostConfig(), s, 10, src, 5)
		evs := w.Log().Events
		if len(evs) < 2 || !evs[1].Resumed {
			t.Fatalf("%s: no suspension: %+v", s, evs)
		}
		return hist{evs[0].Slice.Final, len(evs[0].Slice.Steps), len(evs[1].Slice.Steps), state(w).Term}
	}
	lo, li := run(reduce.LeftmostOutermost), run(reduce.LeftmostInnermost)
	if lo.final != li.final || lo.final != "a" {
		t.Fatalf("results differ: %+v %+v", lo, li)
	}
	if lo.residual == li.residual || lo.before == li.before || lo.after == li.after {
		t.Fatalf("expected different histories: %+v %+v", lo, li)
	}
	if lo.before+lo.after != li.before+li.after {
		t.Logf("total steps differ as well: %d against %d", lo.before+lo.after, li.before+li.after)
	}
}

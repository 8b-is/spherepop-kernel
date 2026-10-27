package sched

import (
	"errors"
	"reflect"
	"sort"
	"testing"

	"github.com/standardgalactic/spherepop-kernel/reduce"
	"github.com/standardgalactic/spherepop-kernel/term"
	"github.com/standardgalactic/spherepop-kernel/trace"
)

const omega = "S (S K K) (S K K) (S (S K K) (S K K))"

func inits(t *testing.T, kv ...string) []Init {
	t.Helper()
	var out []Init
	for i := 0; i < len(kv); i += 2 {
		tm, err := term.Parse(kv[i+1])
		if err != nil {
			t.Fatalf("%s: %v", kv[i], err)
		}
		out = append(out, Init{ID: kv[i], Term: tm})
	}
	return out
}

func world(t *testing.T, cfg reduce.Config, budget uint64, in []Init) *World {
	t.Helper()
	w, err := New(cfg, Policy{Strategy: reduce.LeftmostOutermost, Budget: budget}, in)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestHostDefaultsToSettledBoundaryRulesLanguageDoesNot(t *testing.T) {
	h := HostConfig()
	if h.Absorb != reduce.AbsS || h.Sc != reduce.ScSettled {
		t.Errorf("host should use settled absorption and settled scope, got absorb=%s sc=%s", h.Absorb, h.Sc)
	}
	c := reduce.DefaultConfig()
	if c.Sc == reduce.ScSettled || c.Absorb == reduce.AbsS {
		t.Error("the language default must not change with the host default")
	}
	if got := c.Spec().Sc; got != "any" {
		t.Errorf("language default serialises as %q", got)
	}
	if got := h.Spec(); got.Sc != "settled" || got.Absorb != "s" {
		t.Errorf("host default serialises as %+v", got)
	}
}

func TestNewRejectsBadWorlds(t *testing.T) {
	x := term.MustParse("K a b")
	cfg := HostConfig()
	pol := Policy{Strategy: reduce.LeftmostOutermost, Budget: 5}
	if _, err := New(cfg, Policy{Strategy: reduce.LeftmostOutermost}, nil); !errors.Is(err, ErrNoBudget) {
		t.Errorf("zero budget: %v", err)
	}
	for name, in := range map[string][]Init{
		"empty id":  {{ID: "", Term: x}},
		"nil term":  {{ID: "a"}},
		"duplicate": {{ID: "a", Term: x}, {ID: "a", Term: x}},
		"bad label": {{ID: "a", Term: term.MustParse("<K a b>_q")}},
	} {
		if _, err := New(cfg, pol, in); !errors.Is(err, ErrBadEntity) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := New(cfg, Policy{Budget: 5}, nil); err == nil {
		t.Error("zero strategy accepted")
	}
}

func TestOrderIsByIdentifierNotSupplyOrder(t *testing.T) {
	a := world(t, HostConfig(), 5, inits(t, "b", "K x y", "a", "K x y", "c", "K x y"))
	b := world(t, HostConfig(), 5, inits(t, "c", "K x y", "a", "K x y", "b", "K x y"))
	for _, w := range []*World{a, b} {
		if err := w.Tick(); err != nil {
			t.Fatal(err)
		}
	}
	var ids []string
	for _, ev := range a.Log().Events {
		ids = append(ids, ev.Entity)
	}
	if !reflect.DeepEqual(ids, []string{"a", "b", "c"}) {
		t.Fatalf("order %v", ids)
	}
	la, lb := a.Log(), b.Log()
	ja, _ := Marshal(la)
	jb, _ := Marshal(lb)
	if string(ja) != string(jb) {
		t.Fatal("supply order changed the log")
	}
}

func TestBudgetBoundsEachEntityAndContinuationCarriesOver(t *testing.T) {
	w := world(t, HostConfig(), 3, inits(t, "loop", omega, "quick", "S K K x"))
	if err := w.Tick(); err != nil {
		t.Fatal(err)
	}
	st := w.States()
	if !st[1].Normal || st[1].Term != "x" {
		t.Fatalf("quick should have finished despite the looper: %+v", st[1])
	}
	if st[0].Normal {
		t.Fatal("omega cannot be normal")
	}
	ev := w.Log().Events
	if len(ev[0].Slice.Steps) != 3 {
		t.Fatalf("looper got %d steps, want exactly the budget", len(ev[0].Slice.Steps))
	}
	// Continuation: the next tick starts where the last ended.
	if err := w.Tick(); err != nil {
		t.Fatal(err)
	}
	ev = w.Log().Events
	if ev[2].Slice.StartDigest != ev[0].Slice.FinalDigest {
		t.Fatal("second slice did not start where the first stopped")
	}
	if ev[3].Decision != Skip || ev[3].Slice != nil {
		t.Fatalf("finished entity should be skipped: %+v", ev[3])
	}
}

// The central experiment. Total fuel per entity is held constant while the
// frame subdivision changes. With no events in the language, a difference in
// final state could only come from scheduling or strategy, and scheduling
// turns out to make none: the strategy is a function of the current term, so
// slicing a run and resuming it reproduces the unsliced run.
func TestFrameSubdivisionDoesNotChangeFinalState(t *testing.T) {
	cfg := HostConfig()
	cfg.Perm = map[string]reduce.PermSet{"x": reduce.PermK, "y": 0}
	programs := inits(t,
		"omega", omega,
		"growth", "S (S (S K K) (S K K)) (S K K) (S (S (S K K) (S K K)) (S K K))",
		"early", "S K K x",
		"ka", "K a ("+omega+")",
		"scoped", "<K a b>_x sc_y (K c d) (W K e)",
		"nested", "<I (<K a b>_y) (K c d)>_x (C f g h)",
		"w", "W (C K) (B I I z)",
	)
	const total = 60
	var reference []EntityState
	for _, budget := range []uint64{1, 2, 3, 4, 5, 6, 10, 12, 15, 20, 30, 60} {
		w := world(t, cfg, budget, programs)
		if err := w.Run(int(total / budget)); err != nil {
			t.Fatal(err)
		}
		if err := CheckLog(w.Log()); err != nil {
			t.Fatalf("budget %d: %v", budget, err)
		}
		got := w.States()
		if reference == nil {
			reference = got
			continue
		}
		if !reflect.DeepEqual(got, reference) {
			t.Fatalf("budget %d changed the final state:\n got %+v\nwant %+v", budget, got, reference)
		}
	}
	// And the common answer is the one single-shot reduction gives.
	sorted := append([]Init(nil), programs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	for i, p := range sorted {
		res, err := cfg.Reduce(p.Term, reduce.LeftmostOutermost, total)
		if err != nil {
			t.Fatal(err)
		}
		if res.Term.String() != reference[i].Term {
			t.Errorf("%s: sliced %q, unsliced %q", p.ID, reference[i].Term, res.Term)
		}
	}
}

func TestEntityOrderDoesNotChangeEntityOutcomes(t *testing.T) {
	progs := [][2]string{{"p", omega}, {"q", "S K K x"}, {"r", "K a b"}}
	perms := [][]int{{0, 1, 2}, {2, 1, 0}, {1, 2, 0}}
	var ref []EntityState
	for _, p := range perms {
		var kv []string
		for _, i := range p {
			kv = append(kv, progs[i][0], progs[i][1])
		}
		w := world(t, HostConfig(), 4, inits(t, kv...))
		if err := w.Run(5); err != nil {
			t.Fatal(err)
		}
		if ref == nil {
			ref = w.States()
		} else if !reflect.DeepEqual(ref, w.States()) {
			t.Fatalf("permutation %v changed outcomes", p)
		}
	}
}

// Where outcomes can differ is the reduction strategy, and the log keeps that
// visible: the same world under two strategies disagrees, while one strategy
// under many subdivisions does not.
func TestDifferenceIsAttributedToStrategyNotSchedule(t *testing.T) {
	cfg := reduce.DefaultConfig()
	cfg.Perm = map[string]reduce.PermSet{"l": 0}
	cfg.Absorb = reduce.AbsAny // research mode: known non-confluent
	cfg.Sc = reduce.ScAny
	in := inits(t, "t", "<a>_l (K b c)")
	final := func(s reduce.Strategy, budget uint64, ticks int) string {
		w, err := New(cfg, Policy{Strategy: s, Budget: budget}, in)
		if err != nil {
			t.Fatal(err)
		}
		if err := w.Run(ticks); err != nil {
			t.Fatal(err)
		}
		if err := CheckLog(w.Log()); err != nil {
			t.Fatal(err)
		}
		return w.States()[0].Term
	}
	lo1, lo2, lo5 := final(reduce.LeftmostOutermost, 1, 10), final(reduce.LeftmostOutermost, 2, 5), final(reduce.LeftmostOutermost, 5, 2)
	if lo1 != lo2 || lo2 != lo5 {
		t.Fatalf("subdivision changed the answer: %q %q %q", lo1, lo2, lo5)
	}
	if li := final(reduce.LeftmostInnermost, 1, 10); li == lo1 {
		t.Fatalf("expected the strategies to disagree on a non-confluent configuration, both gave %q", li)
	}
}

func recordedWorld(t *testing.T) *World {
	t.Helper()
	w := world(t, HostConfig(), 2, inits(t, "a", "S K K x", "b", omega, "c", "K p q"))
	if err := w.Run(3); err != nil {
		t.Fatal(err)
	}
	return w
}

func TestCheckLogAcceptsHonestLogAndSurvivesJSON(t *testing.T) {
	w := recordedWorld(t)
	if err := CheckLog(w.Log()); err != nil {
		t.Fatal(err)
	}
	b, err := Marshal(w.Log())
	if err != nil {
		t.Fatal(err)
	}
	back, err := Unmarshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckLog(back); err != nil {
		t.Fatal(err)
	}
	if _, err := Unmarshal([]byte(`{"format":"x","extra":1}`)); !errors.Is(err, ErrLogRejected) {
		t.Errorf("unknown field accepted: %v", err)
	}
	if err := CheckLog(Log{Format: Format, Config: reduce.DefaultConfig().Spec(), Policy: PolicySpec{Strategy: "lo", Budget: 1}}); err != nil {
		t.Errorf("empty world: %v", err)
	}
}

func TestCheckLogRejectsSchedulerMisbehaviour(t *testing.T) {
	mut := func(f func(*Log)) Log {
		l := recordedWorld(t).Log()
		f(&l)
		return l
	}
	// Events: a(run,skip..), b, c per tick; a and c finish in tick 0.
	cases := map[string]Log{
		"reordered events": mut(func(l *Log) { l.Events[0], l.Events[1] = l.Events[1], l.Events[0] }),
		"truncated tick":   mut(func(l *Log) { l.Events = l.Events[:len(l.Events)-1] }),
		"extra budget": mut(func(l *Log) {
			l.Events[1].Budget++
		}),
		"policy rewritten after the fact": mut(func(l *Log) { l.Policy.Budget = 3 }),
		"finished entity run again": mut(func(l *Log) {
			l.Events[3].Decision = Run
			l.Events[3].Budget = l.Policy.Budget
			l.Events[3].Slice = l.Events[0].Slice
		}),
		"unfinished entity skipped": mut(func(l *Log) {
			l.Events[4] = Event{Tick: 1, Entity: "b", Decision: Skip}
		}),
		"slices swapped between entities": mut(func(l *Log) {
			l.Events[0].Slice, l.Events[1].Slice = l.Events[1].Slice, l.Events[0].Slice
		}),
		"slice replayed from another tick": mut(func(l *Log) {
			l.Events[7].Slice = l.Events[1].Slice
		}),
		"final state forged": mut(func(l *Log) { l.Final[1].Digest = l.Final[0].Digest }),
		"final text forged":  mut(func(l *Log) { l.Final[0].Term = "y" }),
		"normal flag forged": mut(func(l *Log) { l.Final[1].Normal = true }),
		"initial state forged": mut(func(l *Log) {
			l.Initial[0].Digest = l.Initial[1].Digest
		}),
		"wrong format": mut(func(l *Log) { l.Format = "other" }),
	}
	for name, l := range cases {
		if err := CheckLog(l); !errors.Is(err, ErrLogRejected) {
			t.Errorf("%s: want rejection, got %v", name, err)
		}
	}
}

// A slice produced under a different configuration is a valid trace on its
// own, and still must not be accepted into a log that declares another one.
func TestSliceFromAnotherConfigurationIsRejected(t *testing.T) {
	l := recordedWorld(t).Log()
	other := HostConfig()
	other.Sc = reduce.ScAny
	tm := term.MustParse("S K K x")
	_, doc, err := trace.Record(other, tm, reduce.LeftmostOutermost, l.Policy.Budget)
	if err != nil {
		t.Fatal(err)
	}
	l.Events[0].Slice = &doc
	if err := CheckLog(l); !errors.Is(err, ErrLogRejected) {
		t.Fatalf("got %v", err)
	}
}

func TestEveryReductionStepIsInsideSliceDocuments(t *testing.T) {
	w := recordedWorld(t)
	total := 0
	for _, ev := range w.Log().Events {
		if ev.Slice != nil {
			total += len(ev.Slice.Steps)
		}
		if ev.Decision == Skip && (ev.Slice != nil || ev.Budget != 0) {
			t.Fatal("scheduler decisions must not carry reduction content")
		}
	}
	if total == 0 {
		t.Fatal("no steps recorded")
	}
}

func TestRearmStartsANewRoundAndIsLogged(t *testing.T) {
	w := world(t, HostConfig(), 5, inits(t, "a", "S K K x"))
	if err := w.Tick(); err != nil {
		t.Fatal(err)
	}
	if !w.States()[0].Normal {
		t.Fatal("first round should finish")
	}
	next := term.MustParse("K y z")
	if err := w.Rearm("a", next); err != nil {
		t.Fatal(err)
	}
	if err := w.Tick(); err != nil {
		t.Fatal(err)
	}
	st := w.States()[0]
	if st.Term != "y" || !st.Normal {
		t.Fatalf("second round: %+v", st)
	}
	ev := w.Log().Events
	if ev[1].Rearm == nil || ev[1].Rearm.Preempted || ev[1].Rearm.Term != "K y z" || ev[1].Slice == nil {
		t.Fatalf("rearm not recorded as a fresh start: %+v", ev[1])
	}
	if ev[1].Slice.StartDigest != next.Digest().String() {
		t.Fatal("slice does not start from the replacement")
	}
	if err := CheckLog(w.Log()); err != nil {
		t.Fatal(err)
	}
}

func TestRearmCanPreemptAndIsMarked(t *testing.T) {
	w := world(t, HostConfig(), 3, inits(t, "a", omega))
	w.Tick()
	if err := w.Rearm("a", term.MustParse("I q")); err != nil {
		t.Fatal(err)
	}
	w.Tick()
	ev := w.Log().Events
	if ev[1].Rearm == nil || !ev[1].Rearm.Preempted {
		t.Fatalf("preemption not marked: %+v", ev[1].Rearm)
	}
	if st := w.States()[0]; st.Term != "q" || !st.Normal {
		t.Fatalf("%+v", st)
	}
	if err := CheckLog(w.Log()); err != nil {
		t.Fatal(err)
	}
}

func TestRearmErrors(t *testing.T) {
	w := world(t, HostConfig(), 3, inits(t, "a", "K x y"))
	x := term.MustParse("I a")
	if err := w.Rearm("nobody", x); !errors.Is(err, ErrUnknownID) {
		t.Errorf("unknown id: %v", err)
	}
	if err := w.Rearm("a", nil); !errors.Is(err, ErrBadEntity) {
		t.Errorf("nil term: %v", err)
	}
	if err := w.Rearm("a", term.MustParse("<K a b>_q")); !errors.Is(err, ErrBadEntity) {
		t.Errorf("unknown label: %v", err)
	}
	if err := w.Rearm("a", x); err != nil {
		t.Fatal(err)
	}
	if err := w.Rearm("a", x); !errors.Is(err, ErrPending) {
		t.Errorf("double rearm: %v", err)
	}
}

func TestCheckLogRejectsForgedRearms(t *testing.T) {
	build := func() Log {
		w := world(t, HostConfig(), 3, inits(t, "a", "S K K x"))
		w.Tick()
		w.Rearm("a", term.MustParse("K y z"))
		w.Tick()
		return w.Log()
	}
	if err := CheckLog(build()); err != nil {
		t.Fatal(err)
	}
	mut := func(f func(*Log)) Log { l := build(); f(&l); return l }
	for name, l := range map[string]Log{
		"claims preemption of a finished entity": mut(func(l *Log) { l.Events[1].Rearm.Preempted = true }),
		"digest does not match term":             mut(func(l *Log) { l.Events[1].Rearm.Digest = l.Events[0].Slice.StartDigest }),
		"term unparseable":                       mut(func(l *Log) { l.Events[1].Rearm.Term = "((" }),
		"unknown label in replacement": mut(func(l *Log) {
			x := term.MustParse("<K a b>_q")
			l.Events[1].Rearm.Term, l.Events[1].Rearm.Digest = x.String(), x.Digest().String()
		}),
		"rearm removed but slice kept": mut(func(l *Log) { l.Events[1].Rearm = nil }),
		"rearm on a skip": mut(func(l *Log) {
			l.Events[1] = Event{Tick: 1, Entity: "a", Decision: Skip, Rearm: l.Events[1].Rearm}
		}),
		"slice starts elsewhere": mut(func(l *Log) {
			x := term.MustParse("I a")
			l.Events[1].Rearm.Term, l.Events[1].Rearm.Digest = x.String(), x.Digest().String()
		}),
	} {
		if err := CheckLog(l); !errors.Is(err, ErrLogRejected) {
			t.Errorf("%s: want rejection, got %v", name, err)
		}
	}
}

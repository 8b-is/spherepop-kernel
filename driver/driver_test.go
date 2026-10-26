package driver

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/standardgalactic/spherepop-kernel/effects"
	"github.com/standardgalactic/spherepop-kernel/encode"
	"github.com/standardgalactic/spherepop-kernel/reduce"
	"github.com/standardgalactic/spherepop-kernel/sched"
	"github.com/standardgalactic/spherepop-kernel/term"
)

type doors struct{ open map[string]bool }

func (d *doors) Commit(r effects.Request) error {
	d.open[r.ID] = r.Verb == "open"
	return nil
}

// Observe reports whether door 3 is open, as a Church Boolean.
type observer struct {
	w     *doors
	calls []string
}

func (o *observer) Observe(subject string, frame int) ([]*term.Term, error) {
	o.calls = append(o.calls, fmt.Sprintf("%s@%d", subject, frame))
	return []*term.Term{encode.Bool(o.w.open["3"])}, nil
}

func schema() effects.Schema {
	return effects.Schema{Targets: map[string]effects.TargetSpec{
		"door": {ID: effects.Int, Verbs: map[string]effects.VerbSpec{"open": {}, "close": {}}},
	}}
}

// pad wraps t in n identities, which cost n reduction steps to remove.
func pad(n int, t string) string {
	return strings.Repeat("I (", n) + t + strings.Repeat(")", n)
}

// choose is a program that receives a Boolean and yields a if it is true and
// b if it is false.
func choose(a, b string) string { return fmt.Sprintf("C (C I (%s)) (%s)", a, b) }

func eff(verb string) string { return "effects (effect (door 3) " + verb + ")" }

// toggle closes the door if it is open and opens it if it is closed.
func toggle(padding int) string { return choose(pad(padding, eff("close")), pad(padding, eff("open"))) }

type rig struct {
	d    *Driver
	w    *doors
	obs  *observer
	host *effects.Host
}

func build(t *testing.T, budget uint64, maxRound int, progs map[string]string) *rig {
	t.Helper()
	w := &doors{open: map[string]bool{}}
	pol := &effects.Policy{}
	var ps []Program
	for id, src := range progs {
		pol.Grant(id, "door", "open").Grant(id, "door", "close")
		ps = append(ps, Program{ID: id, Term: term.MustParse(src)})
	}
	host := &effects.Host{Schema: schema(), Auth: pol, World: w, Log: effects.NewLog(nil)}
	obs := &observer{w: w}
	d, err := New(Params{
		Reduce:         sched.HostConfig(),
		Policy:         sched.Policy{Strategy: reduce.LeftmostOutermost, Budget: budget},
		Host:           host,
		Observer:       obs,
		MaxRoundFrames: maxRound,
	}, ps)
	if err != nil {
		t.Fatal(err)
	}
	return &rig{d: d, w: w, obs: obs, host: host}
}

func (r *rig) trajectory(t *testing.T, frames int) []bool {
	t.Helper()
	var out []bool
	for i := 0; i < frames; i++ {
		if err := r.d.Step(); err != nil {
			t.Fatal(err)
		}
		out = append(out, r.w.open["3"])
	}
	return out
}

func (r *rig) requests() []string {
	var out []string
	for _, e := range r.host.Log.Entries() {
		if e.Disposition == effects.Committed {
			out = append(out, e.Subject+":"+e.Request)
		}
	}
	return out
}

func TestProgramReadsTheWorldAndActsOnIt(t *testing.T) {
	r := build(t, 100, 0, map[string]string{"t": toggle(0)})
	got := r.trajectory(t, 6)
	want := []bool{true, false, true, false, true, false}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("door %v, want %v", got, want)
	}
	if r.d.Rounds("t") != 6 {
		t.Fatalf("rounds %d", r.d.Rounds("t"))
	}
}

// With observation, the schedule can matter: a slow round lands its effect
// later. The proposals themselves stay the same sequence, so the difference is
// in timing, and the logs make that visible.
func TestFrameBudgetChangesTimingNotTheSequenceOfProposals(t *testing.T) {
	fast := build(t, 200, 0, map[string]string{"t": toggle(10)})
	slow := build(t, 4, 0, map[string]string{"t": toggle(10)})
	tf, ts := fast.trajectory(t, 12), slow.trajectory(t, 12)
	if reflect.DeepEqual(tf, ts) {
		t.Fatalf("expected different timing: %v", tf)
	}
	rf, rs := fast.requests(), slow.requests()
	n := len(rs)
	if n == 0 || n >= len(rf) {
		t.Fatalf("expected the slow run to commit fewer requests: %d vs %d", n, len(rf))
	}
	if !reflect.DeepEqual(rf[:n], rs) {
		t.Fatalf("proposal sequences differ:\n%v\n%v", rf[:n], rs)
	}
}

// Observations are a snapshot taken after every commit of the frame. If an
// entity could observe between another entity's commit and its own, a would
// see the door open at the start of frame 1.
func TestObservationsAreASimultaneousSnapshot(t *testing.T) {
	r := build(t, 100, 0, map[string]string{
		"a": toggle(0),    // toggles whatever it sees
		"b": eff("close"), // ignores what it sees... but must still take its argument
	})
	// b needs to accept the observation argument: K x ignores it.
	_ = r
	progs := map[string]string{"a": toggle(0), "b": "K (" + eff("close") + ")"}
	r = build(t, 100, 0, progs)
	if err := r.d.Run(3); err != nil {
		t.Fatal(err)
	}
	var seen []string
	for _, e := range r.host.Log.Entries() {
		if e.Disposition == effects.Committed {
			seen = append(seen, fmt.Sprintf("%d/%s:%s", e.Tick, e.Subject, strings.TrimPrefix(e.Request, "effect (door 3) ")))
		}
	}
	// a observes a closed door every frame, because b closes it after a opens
	// it and the snapshot is taken after both commits.
	want := []string{"0/a:open", "0/b:close", "1/a:open", "1/b:close", "2/a:open", "2/b:close"}
	if !reflect.DeepEqual(seen, want) {
		t.Fatalf("got %v, want %v", seen, want)
	}
}

func TestObservationOrderIsIdentifierOrder(t *testing.T) {
	r := build(t, 100, 0, map[string]string{"b": toggle(0), "a": toggle(0), "c": toggle(0)})
	if err := r.d.Run(2); err != nil {
		t.Fatal(err)
	}
	want := []string{"a@0", "b@0", "c@0", "a@1", "b@1", "c@1", "a@2", "b@2", "c@2"}
	if !reflect.DeepEqual(r.obs.calls, want) {
		t.Fatalf("%v", r.obs.calls)
	}
}

func TestWatchdogAbandonsARunawayRoundWithoutHarmingOthers(t *testing.T) {
	om := "S (S K K) (S K K) (S (S K K) (S K K))"
	r := build(t, 5, 3, map[string]string{"loop": "K (" + om + ")", "ok": toggle(0)})
	if err := r.d.Run(10); err != nil {
		t.Fatal(err)
	}
	var exhausted, committedOK int
	for _, e := range r.host.Log.Entries() {
		switch {
		case e.Subject == "loop" && e.Disposition == effects.Exhausted:
			exhausted++
		case e.Subject == "loop" && e.Disposition == effects.Committed:
			t.Fatal("a runaway round must never act")
		case e.Subject == "ok" && e.Disposition == effects.Committed:
			committedOK++
		}
	}
	if exhausted < 3 || committedOK != 10 {
		t.Fatalf("exhausted=%d committed(ok)=%d", exhausted, committedOK)
	}
	preempted := 0
	for _, ev := range r.d.Scheduler().Log().Events {
		if ev.Rearm != nil && ev.Rearm.Preempted {
			preempted++
		}
	}
	if preempted != exhausted {
		t.Fatalf("preempted rearms %d, exhausted rounds %d", preempted, exhausted)
	}
}

func TestWithoutAWatchdogARunawayNeverActs(t *testing.T) {
	om := "S (S K K) (S K K) (S (S K K) (S K K))"
	r := build(t, 5, 0, map[string]string{"loop": "K (" + om + ")"})
	if err := r.d.Run(8); err != nil {
		t.Fatal(err)
	}
	if len(r.host.Log.Entries()) != 0 || r.d.Rounds("loop") != 0 {
		t.Fatal("nothing should have been logged for a round still running")
	}
}

func TestEvidenceChainsCheck(t *testing.T) {
	om := "S (S K K) (S K K) (S (S K K) (S K K))"
	r := build(t, 5, 3, map[string]string{"loop": "K (" + om + ")", "ok": toggle(2)})
	if err := r.d.Run(12); err != nil {
		t.Fatal(err)
	}
	if err := sched.CheckLog(r.d.Scheduler().Log()); err != nil {
		t.Fatalf("scheduler log: %v", err)
	}
	if err := effects.VerifyAgainst(r.host.Log.Entries(), schema()); err != nil {
		t.Fatalf("effects log: %v", err)
	}
	// Each effect entry points at the head of the slice that produced its output.
	heads := map[string]bool{}
	for _, ev := range r.d.Scheduler().Log().Events {
		if ev.Slice != nil {
			heads[ev.Slice.Head] = true
		}
	}
	for _, e := range r.host.Log.Entries() {
		if !heads[e.Ref] {
			t.Fatalf("entry %d refers to %q, which is no slice head", e.Seq, e.Ref)
		}
	}
}

func TestTwoIdenticalRunsAreByteIdentical(t *testing.T) {
	run := func() ([]effects.Entry, string) {
		r := build(t, 6, 4, map[string]string{"x": toggle(3), "y": toggle(1), "z": "K (" + eff("open") + ")"})
		if err := r.d.Run(15); err != nil {
			t.Fatal(err)
		}
		b, err := sched.Marshal(r.d.Scheduler().Log())
		if err != nil {
			t.Fatal(err)
		}
		return r.host.Log.Entries(), string(b)
	}
	e1, s1 := run()
	e2, s2 := run()
	if !reflect.DeepEqual(e1, e2) || s1 != s2 {
		t.Fatal("the same world produced two different histories")
	}
}

func TestNewRejectsBadPrograms(t *testing.T) {
	r := build(t, 10, 0, map[string]string{"a": toggle(0)})
	p := Params{Reduce: sched.HostConfig(), Policy: sched.Policy{Strategy: reduce.LeftmostOutermost, Budget: 5}, Host: r.host, Observer: r.obs}
	for name, ps := range map[string][]Program{
		"empty id":  {{ID: "", Term: term.MustParse("x")}},
		"nil term":  {{ID: "a"}},
		"duplicate": {{ID: "a", Term: term.MustParse("x")}, {ID: "a", Term: term.MustParse("y")}},
	} {
		if _, err := New(p, ps); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

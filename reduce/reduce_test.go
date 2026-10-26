package reduce

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/standardgalactic/spherepop-kernel/term"
)

func mustParse(t *testing.T, src string, defs term.Defs) *term.Term {
	t.Helper()
	x, err := term.ParseWith(src, defs)
	if err != nil {
		t.Fatalf("parse %q: %v", src, err)
	}
	return x
}

func normal(t *testing.T, cfg Config, src string, fuel uint64) Result {
	t.Helper()
	res, err := cfg.Reduce(mustParse(t, src, nil), LeftmostOutermost, fuel)
	if err != nil {
		t.Fatalf("reduce %q: %v", src, err)
	}
	return res
}

func TestCombinatoryRules(t *testing.T) {
	cases := []struct{ in, want string }{
		{"I x", "x"},
		{"B f g x", "f (g x)"},
		{"C f x y", "f y x"},
		{"K x y", "x"},
		{"W f x", "f x x"},
		{"S f g x", "f x (g x)"},
		{"S K K x", "x"},
		{"S K S x", "x"},
		{"K (I a) b", "a"},
		{"f (K a b) (I c)", "f a c"},
		{"B I I x", "x"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			res := normal(t, DefaultConfig(), tc.in, 100)
			want := mustParse(t, tc.want, nil)
			if res.Exhausted || !term.Equal(res.Term, want) {
				t.Fatalf("%s reduced to %s (exhausted=%v), want %s", tc.in, res.Term, res.Exhausted, want)
			}
		})
	}
}

func TestSKIdentityStepCount(t *testing.T) {
	res := normal(t, DefaultConfig(), "S K K x", 100)
	if res.Steps != 2 {
		t.Fatalf("S K K x took %d steps, want 2", res.Steps)
	}
	if len(res.Trace) != 2 || res.Trace[0].Rule != RuleS || res.Trace[1].Rule != RuleK {
		t.Fatalf("unexpected trace %+v", res.Trace)
	}
}

// The essay's table for Omega = W W with W = S I I and I = S K K.
func omegaDefs() term.Defs {
	i := term.MustParse("S K K")
	w := term.MustParse("S ((S K K)) ((S K K))")
	return term.Defs{"i": i, "w": w, "om": term.App(w, w)}
}

func TestOmegaStrategies(t *testing.T) {
	cfg := DefaultConfig()
	om := omegaDefs()["om"]
	for _, s := range []Strategy{LeftmostOutermost, LeftmostInnermost, RightmostInnermost, RightmostOutermost} {
		res, err := cfg.Reduce(om, s, 200)
		if err != nil {
			t.Fatal(err)
		}
		if !res.Exhausted || res.Steps != 200 {
			t.Fatalf("%s: omega should exhaust fuel, got steps=%d exhausted=%v", s, res.Steps, res.Exhausted)
		}
	}
}

func TestKaOmega(t *testing.T) {
	cfg := DefaultConfig()
	defs := omegaDefs()
	a := term.Atom("a")
	tm := term.Apply(term.Const("K"), a, defs["om"])
	for _, tc := range []struct {
		s         Strategy
		terminate bool
	}{
		{LeftmostOutermost, true},
		{RightmostOutermost, true},
		{LeftmostInnermost, false},
		{RightmostInnermost, false},
	} {
		res, err := cfg.Reduce(tm, tc.s, 300)
		if err != nil {
			t.Fatal(err)
		}
		if tc.terminate {
			if res.Exhausted || res.Steps != 1 || !term.Equal(res.Term, a) {
				t.Errorf("%s: want a in 1 step, got %s in %d (exhausted=%v)", tc.s, res.Term, res.Steps, res.Exhausted)
			}
		} else if !res.Exhausted {
			t.Errorf("%s: K a Omega should not terminate", tc.s)
		}
	}
}

// W (I^n W) under leftmost-outermost takes 2n+3 steps and never repeats.
func TestOmegaLeftmostOutermostNeverRepeats(t *testing.T) {
	cfg := DefaultConfig()
	res, err := cfg.Reduce(omegaDefs()["om"], LeftmostOutermost, 60)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[term.Digest]int{}
	cur := omegaDefs()["om"]
	seen[cur.Digest()] = 0
	for i, st := range res.Trace {
		if st.Before != cur.Digest() {
			t.Fatalf("step %d does not start from the previous term", i)
		}
		next, err := cfg.Apply(cur, st.Path, st.Rule)
		if err != nil {
			t.Fatal(err)
		}
		if j, dup := seen[next.Digest()]; dup {
			t.Fatalf("lo repeated a term: step %d equals step %d", i+1, j)
		}
		seen[next.Digest()] = i + 1
		cur = next
	}
}

func TestOmegaOtherStrategiesCycle(t *testing.T) {
	cfg := DefaultConfig()
	for _, s := range []Strategy{LeftmostInnermost, RightmostInnermost, RightmostOutermost} {
		res, err := cfg.Reduce(omegaDefs()["om"], s, 40)
		if err != nil {
			t.Fatal(err)
		}
		cur := omegaDefs()["om"]
		seen := map[term.Digest]int{cur.Digest(): 0}
		cycle := 0
		for i, st := range res.Trace {
			cur, err = cfg.Apply(cur, st.Path, st.Rule)
			if err != nil {
				t.Fatal(err)
			}
			if j, dup := seen[cur.Digest()]; dup {
				cycle = i + 1 - j
				break
			}
			seen[cur.Digest()] = i + 1
		}
		if cycle != 5 {
			t.Errorf("%s: cycle period %d, want 5", s, cycle)
		}
	}
}

func TestFuelBoundary(t *testing.T) {
	cfg := DefaultConfig()
	x := mustParse(t, "S K K x", nil)
	for fuel, want := range map[uint64]struct {
		exhausted bool
		steps     uint64
	}{0: {true, 0}, 1: {true, 1}, 2: {false, 2}, 3: {false, 2}} {
		res, err := cfg.Reduce(x, LeftmostOutermost, fuel)
		if err != nil {
			t.Fatal(err)
		}
		if res.Exhausted != want.exhausted || res.Steps != want.steps {
			t.Errorf("fuel %d: exhausted=%v steps=%d, want %+v", fuel, res.Exhausted, res.Steps, want)
		}
	}
}

func TestStrategyDependentTraces(t *testing.T) {
	cfg := DefaultConfig()
	x := mustParse(t, "K (I a) (I b)", nil)
	lo, _ := cfg.Reduce(x, LeftmostOutermost, 10)
	li, _ := cfg.Reduce(x, LeftmostInnermost, 10)
	if !term.Equal(lo.Term, li.Term) {
		t.Fatalf("normal forms differ: %s vs %s", lo.Term, li.Term)
	}
	if lo.Steps != 2 || li.Steps != 3 {
		t.Fatalf("steps lo=%d li=%d, want 2 and 3", lo.Steps, li.Steps)
	}
}

func TestDeterministicReplay(t *testing.T) {
	cfg := DefaultConfig()
	x := mustParse(t, "S (K S) K a b c", nil)
	a, _ := cfg.Reduce(x, LeftmostInnermost, 100)
	b, _ := cfg.Reduce(x, LeftmostInnermost, 100)
	if fmt.Sprint(a.Trace) != fmt.Sprint(b.Trace) || !term.Equal(a.Term, b.Term) {
		t.Fatal("two runs differ")
	}
}

func TestSizeLaws(t *testing.T) {
	cfg := DefaultConfig()
	// K deletes y and its application circle: size changes by -(2+|y|) where
	// |y| counts y's own circles.
	x := mustParse(t, "K a (f b c)", nil)
	res, _ := cfg.Reduce(x, LeftmostOutermost, 1)
	if got := x.Size() - res.Term.Size(); got != 2+2 {
		t.Errorf("K size drop %d, want 4", got)
	}
	// S adds |z| circles.
	y := mustParse(t, "S f g (h i j)", nil)
	res, _ = cfg.Reduce(y, LeftmostOutermost, 1)
	if got := res.Term.Size() - y.Size(); got != 2 {
		t.Errorf("S size gain %d, want |z| = 2", got)
	}
}

func TestEveryStepIsReplayable(t *testing.T) {
	cfg := DefaultConfig()
	x := mustParse(t, "S (K S) K a b c", nil)
	res, _ := cfg.Reduce(x, RightmostOutermost, 100)
	cur := x
	for i, st := range res.Trace {
		next, err := cfg.Apply(cur, st.Path, st.Rule)
		if err != nil || next.Digest() != st.After {
			t.Fatalf("step %d does not replay: %v", i, err)
		}
		cur = next
	}
}

func TestApplyRejections(t *testing.T) {
	cfg := DefaultConfig()
	x := mustParse(t, "K a b", nil)
	if _, err := cfg.Apply(x, "", RuleS); !errors.Is(err, ErrRuleMismatch) {
		t.Errorf("wrong rule: %v", err)
	}
	if _, err := cfg.Apply(x, "", RuleID("Z")); !errors.Is(err, ErrUnknownRule) {
		t.Errorf("unknown rule: %v", err)
	}
	if _, err := cfg.Apply(x, "RL", RuleK); !errors.Is(err, term.ErrBadPath) {
		t.Errorf("bad path: %v", err)
	}
	if _, err := cfg.Apply(mustParse(t, "K a", nil), "", RuleK); !errors.Is(err, ErrRuleMismatch) {
		t.Errorf("too few arguments: %v", err)
	}
}

func TestUnknownLabelFailsClosed(t *testing.T) {
	cfg := DefaultConfig()
	_, err := cfg.Reduce(mustParse(t, "<K a b>_x", nil), LeftmostOutermost, 10)
	if !errors.Is(err, ErrUnknownLabel) {
		t.Fatalf("want ErrUnknownLabel, got %v", err)
	}
}

func scoped(perms map[string]PermSet, mode Absorption) Config {
	c := DefaultConfig()
	c.Perm = perms
	c.Absorb = mode
	return c
}

func TestScopeBlocksGatedConstants(t *testing.T) {
	cfg := scoped(map[string]PermSet{"x": 0, "y": PermK}, AbsU)
	// K is not permitted inside x, so the redex is stuck there.
	res := normal(t, cfg, "<K a b>_x", 10)
	if res.Steps != 0 {
		t.Fatalf("K fired inside a scope that forbids it: %s", res.Term)
	}
	// I is always admissible.
	if res := normal(t, cfg, "<I a>_x", 10); res.Term.String() != "<a>_x" {
		t.Fatalf("I did not fire inside x: %s", res.Term)
	}
	// y permits K, and root permission is intersected, not unioned.
	if res := normal(t, cfg, "<K a b>_y", 10); res.Term.String() != "<a>_y" {
		t.Fatalf("K blocked inside y: %s", res.Term)
	}
	root := scoped(map[string]PermSet{"y": PermK}, AbsU)
	root.Root = PermW
	if res := normal(t, root, "<K a b>_y", 10); res.Steps != 0 {
		t.Fatalf("K fired though the root forbids it: %s", res.Term)
	}
}

func TestScRule(t *testing.T) {
	cfg := scoped(map[string]PermSet{"x": PermAll}, AbsU)
	res := normal(t, cfg, "sc_x (K a b)", 10)
	if res.Term.String() != "<a>_x" || res.Steps != 2 {
		t.Fatalf("got %s in %d steps", res.Term, res.Steps)
	}
}

// Theorem 4.2: with unrestricted absorption the system is not confluent when
// the root permits a constant that some label forbids. Absorbing the argument
// into the bracket and contracting at the root give different normal forms.
func TestUnrestrictedAbsorptionDiverges(t *testing.T) {
	cfg := scoped(map[string]PermSet{"x": 0}, AbsAny)
	start := mustParse(t, "<K a>_x (K b c)", nil)
	nfs := map[string]bool{}
	for _, s := range []Strategy{LeftmostOutermost, LeftmostInnermost} {
		res, err := cfg.Reduce(start, s, 50)
		if err != nil || res.Exhausted {
			t.Fatalf("%s: %v exhausted=%v", s, err, res.Exhausted)
		}
		nfs[res.Term.String()] = true
	}
	if len(nfs) != 2 {
		t.Fatalf("expected two distinct normal forms, got %v", nfs)
	}
	// The default AbsU refuses the unsafe absorption and stays confluent.
	safe := scoped(map[string]PermSet{"x": 0}, AbsU)
	nfs = map[string]bool{}
	for _, s := range []Strategy{LeftmostOutermost, LeftmostInnermost, RightmostInnermost, RightmostOutermost} {
		res, _ := safe.Reduce(start, s, 50)
		nfs[res.Term.String()] = true
	}
	if len(nfs) != 1 {
		t.Fatalf("AbsU should agree across strategies, got %v", nfs)
	}
}

func TestAbsorptionModes(t *testing.T) {
	perms := map[string]PermSet{"x": 0}
	redexArg := mustParse(t, "<a>_x (K b c)", nil)
	settledArg := mustParse(t, "<a>_x b", nil)
	pending := mustParse(t, "<a>_x (<I>_x b)", nil)
	sensitivePending := mustParse(t, "<a>_x (<K b>_x c)", nil)
	check := func(mode Absorption, tm *term.Term, want bool) {
		t.Helper()
		cfg := scoped(perms, mode)
		got := false
		for _, r := range cfg.Redexes(tm, LeftmostOutermost) {
			if r.Rule == RuleAbs && r.Path == "" {
				got = true
			}
		}
		if got != want {
			t.Errorf("mode %s on %s: Abs at root = %v, want %v", mode, tm, got, want)
		}
	}
	check(AbsOff, settledArg, false)
	check(AbsAny, redexArg, true)
	check(AbsNF, redexArg, false)
	check(AbsNF, settledArg, true)
	check(AbsS, settledArg, true)
	check(AbsS, pending, false)
	check(AbsU, pending, true) // R-normal and insensitive, though not settled
	check(AbsU, sensitivePending, false)
	check(AbsU, redexArg, false)
}

func TestRedexesListsWithoutStopping(t *testing.T) {
	cfg := DefaultConfig()
	x := mustParse(t, "f (K a b) (I c)", nil)
	rs := cfg.Redexes(x, LeftmostOutermost)
	if len(rs) != 2 || rs[0].Rule != RuleK || rs[1].Rule != RuleI {
		t.Fatalf("redexes %+v", rs)
	}
	if rs := cfg.Redexes(x, RightmostOutermost); rs[0].Rule != RuleI {
		t.Fatalf("ro should take I first: %+v", rs)
	}
}

func TestConfigSpecRoundTrip(t *testing.T) {
	cfg := scoped(map[string]PermSet{"x": PermK | PermC, "y": 0}, AbsS)
	back, err := cfg.Spec().Config()
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(back.Spec()) != fmt.Sprint(cfg.Spec()) || back.Absorb != AbsS {
		t.Fatalf("round trip changed the config: %+v", back)
	}
	bad := cfg.Spec()
	bad.Root = []string{"Q"}
	if _, err := bad.Config(); err == nil || !strings.Contains(err.Error(), "alphabet") {
		t.Fatalf("unknown permission accepted: %v", err)
	}
}

package trace

import (
	"errors"
	"strings"
	"testing"

	"github.com/standardgalactic/spherepop-kernel/reduce"
	"github.com/standardgalactic/spherepop-kernel/term"
)

func record(t *testing.T, cfg reduce.Config, src string, s reduce.Strategy, fuel uint64) Document {
	t.Helper()
	_, d, err := Record(cfg, term.MustParse(src), s, fuel)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func cloneDoc(d Document) Document {
	c := d
	c.Steps = append([]StepRecord(nil), d.Steps...)
	return c
}

func wantViolation(t *testing.T, d Document, opt Options, want error) {
	t.Helper()
	_, err := Check(d, opt)
	if !errors.Is(err, want) {
		t.Fatalf("want %v, got %v", want, err)
	}
	var v *Violation
	if !errors.As(err, &v) {
		t.Fatalf("error is not a *Violation: %T", err)
	}
}

func TestValidTraceAccepted(t *testing.T) {
	for _, s := range []reduce.Strategy{reduce.LeftmostOutermost, reduce.LeftmostInnermost, reduce.RightmostInnermost, reduce.RightmostOutermost} {
		d := record(t, reduce.DefaultConfig(), "S (K S) K a b c", s, 100)
		rep, err := Check(d, Options{RequireStrategy: true})
		if err != nil || !rep.Normal || rep.Steps != len(d.Steps) {
			t.Fatalf("%s: %+v %v", s, rep, err)
		}
		if rep.Final.String() != d.Final {
			t.Fatalf("%s: final mismatch", s)
		}
	}
}

func TestExhaustedTraceAccepted(t *testing.T) {
	om := "S (S K K) (S K K) (S (S K K) (S K K))"
	d := record(t, reduce.DefaultConfig(), om, reduce.LeftmostOutermost, 25)
	if !d.Exhausted {
		t.Fatal("omega should exhaust")
	}
	rep, err := Check(d, Options{RequireStrategy: true})
	if err != nil || rep.Normal {
		t.Fatalf("%+v %v", rep, err)
	}
}

func TestJSONRoundTripAndReplay(t *testing.T) {
	d := record(t, reduce.DefaultConfig(), "S K K x", reduce.LeftmostOutermost, 10)
	b, err := Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Unmarshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Check(back, Options{RequireStrategy: true}); err != nil {
		t.Fatal(err)
	}
	// Recording the same run twice yields byte-identical documents.
	b2, _ := Marshal(record(t, reduce.DefaultConfig(), "S K K x", reduce.LeftmostOutermost, 10))
	if string(b) != string(b2) {
		t.Fatal("two recordings differ")
	}
}

func TestUnmarshalRejectsJunk(t *testing.T) {
	for _, in := range []string{``, `{`, `{"format":"x","bogus":1}`, `{} {}`} {
		if _, err := Unmarshal([]byte(in)); !errors.Is(err, ErrFormat) {
			t.Errorf("%q: %v", in, err)
		}
	}
}

func TestTamperingIsDetected(t *testing.T) {
	good := record(t, reduce.DefaultConfig(), "S K K x", reduce.LeftmostOutermost, 10)
	mut := func(f func(*Document)) Document { d := cloneDoc(good); f(&d); return d }

	wantViolation(t, mut(func(d *Document) { d.Format = "other/1" }), Options{}, ErrFormat)
	wantViolation(t, mut(func(d *Document) { d.Start = "S K K y" }), Options{}, ErrStartMismatch)
	wantViolation(t, mut(func(d *Document) { d.Steps[0].Rule = reduce.RuleK }), Options{}, ErrChainBroken)
	wantViolation(t, mut(func(d *Document) { d.Steps[0].Path = "L" }), Options{}, ErrChainBroken)
	wantViolation(t, mut(func(d *Document) { d.Steps[1].After = d.Steps[0].After }), Options{}, ErrChainBroken)
	wantViolation(t, mut(func(d *Document) { d.Head = d.Steps[0].Link }), Options{}, ErrChainBroken)
	wantViolation(t, mut(func(d *Document) { d.Steps[0], d.Steps[1] = d.Steps[1], d.Steps[0] }), Options{}, ErrChainBroken)
	wantViolation(t, mut(func(d *Document) { d.Steps = d.Steps[:1] }), Options{}, ErrChainBroken)
	wantViolation(t, mut(func(d *Document) { d.Config.Absorb = "any" }), Options{}, ErrChainBroken)
}

// A forger who recomputes the hash chain defeats the chain but not the
// checker: the chain gives order and integrity, the rule table gives validity.
func TestResealedForgeriesStillFail(t *testing.T) {
	good := record(t, reduce.DefaultConfig(), "S K K x", reduce.LeftmostOutermost, 10)
	mut := func(f func(*Document)) Document {
		d := cloneDoc(good)
		f(&d)
		if err := d.seal(); err != nil {
			t.Fatal(err)
		}
		return d
	}
	// Claim a different result for the first step.
	wantViolation(t, mut(func(d *Document) { d.Steps[0].After = good.Steps[1].After }), Options{}, ErrDigestMismatch)
	// Cite a rule that does not match the redex.
	wantViolation(t, mut(func(d *Document) { d.Steps[0].Rule = reduce.RuleK }), Options{}, ErrStepRejected)
	// Cite a rule outside the table.
	wantViolation(t, mut(func(d *Document) { d.Steps[0].Rule = "Omega" }), Options{}, ErrStepRejected)
	// Cite a position that does not exist.
	wantViolation(t, mut(func(d *Document) { d.Steps[0].Path = "RRRR" }), Options{}, ErrStepRejected)
	// Drop the last step but keep the final claim.
	wantViolation(t, mut(func(d *Document) { d.Steps = d.Steps[:1] }), Options{}, ErrFinalMismatch)
	// Claim the run stopped early.
	wantViolation(t, mut(func(d *Document) {
		d.Steps = d.Steps[:1]
		d.Final = "K x (K x)"
		d.FinalDigest = term.MustParse("K x (K x)").Digest().String()
	}), Options{}, ErrStatusMismatch)
	// Claim exhaustion of a run that reached a normal form.
	wantViolation(t, mut(func(d *Document) { d.Exhausted = true }), Options{}, ErrStatusMismatch)
	// More steps than the stated fuel.
	wantViolation(t, mut(func(d *Document) { d.Fuel = 1 }), Options{}, ErrStatusMismatch)
}

// The central case for scoped permissions: a worker claims K fired inside a
// scope that forbids it. The chain verifies and the endpoints are plausible,
// but the step is not authorised at its position.
func TestUnauthorisedScopedStepRejected(t *testing.T) {
	cfg := reduce.DefaultConfig()
	cfg.Perm = map[string]reduce.PermSet{"x": 0}
	good := record(t, cfg, "<K a b>_x", reduce.LeftmostOutermost, 10)
	if good.Steps != nil && len(good.Steps) != 0 {
		t.Fatalf("K should be stuck inside x, got %d steps", len(good.Steps))
	}
	forged := cloneDoc(good)
	forged.Steps = []StepRecord{{Rule: reduce.RuleK, Path: "B", After: term.MustParse("<a>_x").Digest().String()}}
	forged.Final = "<a>_x"
	forged.FinalDigest = term.MustParse("<a>_x").Digest().String()
	if err := forged.seal(); err != nil {
		t.Fatal(err)
	}
	wantViolation(t, forged, Options{}, ErrStepRejected)
	_, err := Check(forged, Options{})
	if !errors.Is(err, reduce.ErrNotAdmissible) {
		t.Fatalf("reason should name the admissibility failure: %v", err)
	}
}

func TestSamePermissionStructureAcceptsLegitimateStep(t *testing.T) {
	cfg := reduce.DefaultConfig()
	cfg.Perm = map[string]reduce.PermSet{"x": reduce.PermK}
	d := record(t, cfg, "<K a b>_x", reduce.LeftmostOutermost, 10)
	if _, err := Check(d, Options{RequireStrategy: true}); err != nil {
		t.Fatal(err)
	}
}

func TestAbsorptionStepsNeedTheConfiguredMode(t *testing.T) {
	cfg := reduce.DefaultConfig()
	cfg.Perm = map[string]reduce.PermSet{"x": 0}
	cfg.Absorb = reduce.AbsAny
	d := record(t, cfg, "<K a>_x (K b c)", reduce.LeftmostOutermost, 20)
	if _, err := Check(d, Options{}); err != nil {
		t.Fatal(err)
	}
	// The same steps under a restricted mode are not authorised.
	d.Config.Absorb = "u"
	if err := d.seal(); err != nil {
		t.Fatal(err)
	}
	wantViolation(t, d, Options{}, ErrStepRejected)
}

func TestAgreementIsNotDiscovery(t *testing.T) {
	// Two different orders reach the same result. Both traces are valid; only
	// the strategy-checking mode distinguishes which order was followed.
	cfg := reduce.DefaultConfig()
	lo := record(t, cfg, "K (I a) (I b)", reduce.LeftmostOutermost, 10)
	li := record(t, cfg, "K (I a) (I b)", reduce.LeftmostInnermost, 10)
	if lo.FinalDigest != li.FinalDigest || len(lo.Steps) == len(li.Steps) {
		t.Fatal("test premise: same result, different routes")
	}
	for _, d := range []Document{lo, li} {
		if _, err := Check(d, Options{}); err != nil {
			t.Fatal(err)
		}
	}
	lying := cloneDoc(li)
	lying.Strategy = "lo"
	if _, err := Check(lying, Options{}); err != nil {
		t.Fatalf("without RequireStrategy a mislabelled but valid trace is fine: %v", err)
	}
	wantViolation(t, lying, Options{RequireStrategy: true}, ErrStrategyDeviation)
}

func TestMaxStepsBoundsTheChecker(t *testing.T) {
	d := record(t, reduce.DefaultConfig(), "S (K S) K a b c", reduce.LeftmostOutermost, 100)
	wantViolation(t, d, Options{MaxSteps: 1}, ErrFormat)
}

func TestUnknownLabelInStartRejected(t *testing.T) {
	d := record(t, reduce.DefaultConfig(), "S K K x", reduce.LeftmostOutermost, 10)
	d.Start = "<S K K x>_q"
	d.StartDigest = term.MustParse(d.Start).Digest().String()
	_, err := Check(d, Options{})
	if err == nil || !strings.Contains(err.Error(), "label") {
		t.Fatalf("unknown label accepted: %v", err)
	}
}

// A worker can cut a run short and still claim exhaustion. Exhaustion means
// the whole budget was spent, so the step count must equal the fuel.
func TestTruncatedRunCannotClaimExhaustion(t *testing.T) {
	om := "S (S K K) (S K K) (S (S K K) (S K K))"
	d := record(t, reduce.DefaultConfig(), om, reduce.LeftmostOutermost, 25)
	if _, err := Check(d, Options{}); err != nil {
		t.Fatal(err)
	}
	short := cloneDoc(d)
	short.Steps = short.Steps[:10]
	cur := term.MustParse(short.Start)
	cfg := reduce.DefaultConfig()
	for _, s := range short.Steps {
		cur, _ = cfg.Apply(cur, term.Path(s.Path), s.Rule)
	}
	short.Final, short.FinalDigest = cur.String(), cur.Digest().String()
	if err := short.seal(); err != nil {
		t.Fatal(err)
	}
	wantViolation(t, short, Options{}, ErrStatusMismatch)
	// Restating the fuel to match the truncation passes unless the host
	// supplies the budget it agreed.
	short.Fuel = 10
	if err := short.seal(); err != nil {
		t.Fatal(err)
	}
	if _, err := Check(short, Options{}); err != nil {
		t.Fatalf("self-consistent short run should pass without an agreed budget: %v", err)
	}
	wantViolation(t, short, Options{Fuel: 25}, ErrStatusMismatch)
}

// A label whose permission is empty must be written as [] and read back by
// the strict decoder; the first version wrote null and rejected its own output.
func TestEmptyLabelPermissionRoundTripsThroughStrictDecoding(t *testing.T) {
	cfg := reduce.DefaultConfig()
	cfg.Perm = map[string]reduce.PermSet{"p": 0, "q": reduce.PermAll}
	start, err := term.Parse("<K a b>_p (<I c>_q d)")
	if err != nil {
		t.Fatal(err)
	}
	_, doc, err := Record(cfg, start, reduce.LeftmostOutermost, 20)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"p": []`) {
		t.Fatalf("empty permission not written as []:\n%s", b)
	}
	back, err := Unmarshal(b)
	if err != nil {
		t.Fatalf("own output rejected: %v", err)
	}
	if _, err := Check(back, Options{RequireStrategy: true}); err != nil {
		t.Fatal(err)
	}
}

// An execution boundary ends a run: a trace that carries on from a stopped
// term is rejected when the checker is given the boundary, and accepted when
// it is not, because the steps are all authorised by the rules.
func TestStepFromAStoppedTermIsRejectedOnlyUnderTheBoundary(t *testing.T) {
	cfg := reduce.DefaultConfig()
	start := term.MustParse("yield (K a b)")
	_, doc, err := Record(cfg, start, reduce.LeftmostOutermost, 10) // ignores the boundary
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Steps) != 1 {
		t.Fatalf("setup: %d steps", len(doc.Steps))
	}
	stop := func(x *term.Term) bool {
		return x.Kind() == term.KindApp && x.Fun().Kind() == term.KindAtom && x.Fun().Name() == "yield"
	}
	if _, err := Check(doc, Options{RequireStrategy: true}); err != nil {
		t.Fatalf("without a boundary: %v", err)
	}
	if _, err := Check(doc, Options{RequireStrategy: true, Stop: stop}); !errors.Is(err, ErrStepAfterStop) {
		t.Fatalf("with the boundary: %v", err)
	}
	// The honest run stops at once and is accepted, with a redex still inside.
	_, doc2, err := RecordUntil(cfg, start, reduce.LeftmostOutermost, 10, stop)
	if err != nil || len(doc2.Steps) != 0 || doc2.Exhausted {
		t.Fatalf("stopped run: %v %+v", err, doc2)
	}
	if _, err := Check(doc2, Options{RequireStrategy: true, Stop: stop}); err != nil {
		t.Fatal(err)
	}
	if _, err := Check(doc2, Options{RequireStrategy: true}); !errors.Is(err, ErrStatusMismatch) {
		t.Fatalf("a stopped trace must not pass as a finished one: %v", err)
	}
}

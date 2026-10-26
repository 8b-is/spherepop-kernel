package effects

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/standardgalactic/spherepop-kernel/reduce"
	"github.com/standardgalactic/spherepop-kernel/sched"
	"github.com/standardgalactic/spherepop-kernel/term"
)

func testSchema() Schema {
	return Schema{Targets: map[string]TargetSpec{
		"door": {ID: Int, Verbs: map[string]VerbSpec{"open": {}, "close": {}}},
		"lamp": {ID: Sym, Verbs: map[string]VerbSpec{"dim": {Args: []ArgType{Int}}}},
	}}
}

// doors is a toy world. It counts commits so tests can show when the world
// was and was not touched.
type doors struct {
	open    map[string]bool
	commits int
}

func newDoors() *doors { return &doors{open: map[string]bool{}} }

func (d *doors) Commit(r Request) error {
	if r.Kind == "door" {
		if r.ID == "0" || len(r.ID) > 1 {
			return fmt.Errorf("no such door %s", r.ID)
		}
		d.open[r.ID] = r.Verb == "open"
	}
	d.commits++
	return nil
}

func host(w World, p *Policy) *Host {
	return &Host{Schema: testSchema(), Auth: p, World: w, Log: NewLog(nil)}
}

func normalForm(t *testing.T, src string, s reduce.Strategy) *term.Term {
	t.Helper()
	res, err := reduce.Reduce(term.MustParse(src), s, 1000)
	if err != nil || res.Exhausted {
		t.Fatalf("%q: %v exhausted=%v", src, err, res.Exhausted)
	}
	return res.Term
}

func TestSchemaValidate(t *testing.T) {
	if err := testSchema().Validate(); err != nil {
		t.Fatal(err)
	}
	bad := []Schema{
		{Targets: map[string]TargetSpec{"Door": {ID: Int, Verbs: map[string]VerbSpec{"open": {}}}}},
		{Targets: map[string]TargetSpec{"effect": {ID: Int, Verbs: map[string]VerbSpec{"open": {}}}}},
		{Targets: map[string]TargetSpec{"door": {ID: 0, Verbs: map[string]VerbSpec{"open": {}}}}},
		{Targets: map[string]TargetSpec{"door": {ID: Int}}},
		{Targets: map[string]TargetSpec{"door": {ID: Int, Verbs: map[string]VerbSpec{"effects": {}}}}},
		{Targets: map[string]TargetSpec{"door": {ID: Int, Verbs: map[string]VerbSpec{"open": {Args: []ArgType{0}}}}}},
	}
	for i, s := range bad {
		if err := s.Validate(); !errors.Is(err, ErrSchema) {
			t.Errorf("schema %d accepted: %v", i, err)
		}
	}
}

func TestDecode(t *testing.T) {
	s := testSchema()
	ok := map[string]Request{
		"effect (door 3) open":         {Kind: "door", ID: "3", Verb: "open"},
		"effect (door 0) close":        {Kind: "door", ID: "0", Verb: "close"},
		"effect (lamp kitchen) dim 40": {Kind: "lamp", ID: "kitchen", Verb: "dim", Args: []string{"40"}},
	}
	for src, want := range ok {
		got, err := s.Decode(term.MustParse(src))
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%q: %+v %v", src, got, err)
		}
		if got.String() != src {
			t.Errorf("canonical form %q != %q", got.String(), src)
		}
	}
	for _, src := range []string{
		"effect",                           // too short
		"effect (door 3)",                  // no verb
		"effect door open",                 // target not (kind id)
		"effect (door 3 4) open",           // target with two ids
		"effect (window 3) open",           // unknown kind
		"effect (door 3) smash",            // unknown verb
		"effect (door x) open",             // id not an integer
		"effect (door 03) open",            // non-canonical numeral
		"effect (door 99999999999) open",   // overflows
		"effect (door 3) open extra",       // too many arguments
		"effect (lamp kitchen) dim",        // too few arguments
		"effect (lamp kitchen) dim bright", // wrong argument type
		"effect (lamp 7) dim 5",            // id must be an identifier
		"effect (door 3) (open now)",       // verb not an atom
		"effect (door 3) open (K a b)",     // argument not an atom
		"act (door 3) open",                // wrong head
		"K (effect (door 3) open) x",       // unreduced: a constant head is not an effect
		"<effect (door 3) open>_l",         // a bracket is not an effect
		"effect (door 3) K",                // constant as verb
	} {
		if _, err := s.Decode(term.MustParse(src)); !errors.Is(err, ErrMalformed) {
			t.Errorf("%q accepted or wrong error: %v", src, err)
		}
	}
}

func TestExtract(t *testing.T) {
	reqs, found := Extract(term.MustParse("effects (effect (door 1) open) (effect (door 2) close)"))
	if !found || len(reqs) != 2 {
		t.Fatalf("found=%v n=%d", found, len(reqs))
	}
	if reqs, found := Extract(term.MustParse("effects")); !found || len(reqs) != 0 {
		t.Error("bare effects is an empty output")
	}
	for _, src := range []string{"x x x", "effect (door 1) open", "K effects a", "<effects>_l"} {
		if _, found := Extract(term.MustParse(src)); found {
			t.Errorf("%q treated as output", src)
		}
	}
}

func TestHandleDispositionsAreIndependentAndOrdered(t *testing.T) {
	w := newDoors()
	p := (&Policy{}).Grant("a", "door", "open").Grant("a", "door", "close")
	h := host(w, p)
	n := term.MustParse(strings.Join([]string{
		"effects",
		"(effect (door 1) open)",  // committed
		"(effect (door x) open)",  // malformed
		"(effect (lamp k) dim 5)", // denied: no grant
		"(effect (door 0) close)", // failed: world refuses
		"(effect (door 2) open)",  // committed, after failures
	}, " "))
	entries, err := h.HandleOutcome(7, "a", "ref1", n, false)
	if err != nil {
		t.Fatal(err)
	}
	var got []Disposition
	for _, e := range entries {
		got = append(got, e.Disposition)
		if e.Tick != 7 || e.Subject != "a" || e.Ref != "ref1" {
			t.Errorf("entry context lost: %+v", e)
		}
	}
	want := []Disposition{Committed, Malformed, Denied, Failed, Committed}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dispositions %v, want %v", got, want)
	}
	if !w.open["1"] || !w.open["2"] || w.commits != 2 { // the refused commit is not counted
		t.Fatalf("world %+v", w)
	}
	for i, e := range entries {
		if e.Index != i {
			t.Errorf("entry %d has index %d", i, e.Index)
		}
	}
	if err := VerifyAgainst(h.Log.Entries(), testSchema()); err != nil {
		t.Fatal(err)
	}
}

func TestDeniedRequestNeverReachesTheWorld(t *testing.T) {
	w := newDoors()
	h := host(w, &Policy{}) // grants nothing
	if _, err := h.HandleOutcome(0, "a", "", term.MustParse("effects (effect (door 1) open)"), false); err != nil {
		t.Fatal(err)
	}
	if w.commits != 0 {
		t.Fatal("fail-closed policy let a request through")
	}
	var nilPolicy *Policy
	if nilPolicy.Authorize("a", Request{}).Allow {
		t.Fatal("nil policy must deny")
	}
}

func TestNoOutputExhaustedAndTruncated(t *testing.T) {
	w := newDoors()
	h := host(w, (&Policy{}).Grant("a", "door", "open"))
	e, _ := h.HandleOutcome(0, "a", "", term.MustParse("x x x"), false)
	if len(e) != 1 || e[0].Disposition != NoOutput || e[0].Index != -1 {
		t.Fatalf("%+v", e)
	}
	// An exhausted run is never read, even if its partial term looks like output.
	e, _ = h.HandleOutcome(0, "a", "", term.MustParse("effects (effect (door 1) open)"), true)
	if len(e) != 1 || e[0].Disposition != Exhausted || w.commits != 0 {
		t.Fatalf("%+v commits=%d", e, w.commits)
	}
	h.MaxRequests = 2
	e, _ = h.HandleOutcome(0, "a", "", term.MustParse("effects (effect (door 1) open) (effect (door 2) open) (effect (door 3) open) (effect (door 4) open)"), false)
	if len(e) != 3 || e[2].Disposition != Truncated || w.commits != 2 {
		t.Fatalf("%+v commits=%d", e, w.commits)
	}
}

// Effects are data. Whether one is proposed depends on the normal form and
// on nothing that happened on the way to it.
func TestEffectsDoNotFireDuringReduction(t *testing.T) {
	w := newDoors()
	h := host(w, (&Policy{}).Grant("a", "door", "open"))
	// K discards a whole second output, so only the first is ever seen.
	for _, s := range []reduce.Strategy{reduce.LeftmostOutermost, reduce.LeftmostInnermost, reduce.RightmostInnermost, reduce.RightmostOutermost} {
		n := normalForm(t, "K (effects (effect (door 1) open)) (effects (effect (door 2) open))", s)
		if n.String() != "effects (effect (door 1) open)" {
			t.Fatalf("%s: %s", s, n)
		}
	}
	if w.commits != 0 {
		t.Fatal("the world changed before the host was asked")
	}
	// W duplicates a request; the host sees two requests and treats each on its own.
	n := normalForm(t, "W effects (effect (door 1) open)", reduce.LeftmostOutermost)
	e, _ := h.HandleOutcome(0, "a", "", n, false)
	if len(e) != 2 || w.commits != 2 {
		t.Fatalf("duplicated request: %+v", e)
	}
}

func TestLogChainTamperingAndAnchor(t *testing.T) {
	h := host(newDoors(), (&Policy{}).Grant("a", "door", "open"))
	for i := 0; i < 3; i++ {
		h.HandleOutcome(i, "a", "", term.MustParse("effects (effect (door 1) open) (effect (door 9) smash)"), false)
	}
	all := h.Log.Entries()
	if err := Verify(all); err != nil {
		t.Fatal(err)
	}
	anchor := h.Log.Head()
	mut := func(f func([]Entry) []Entry) []Entry { return f(h.Log.Entries()) }
	for name, es := range map[string][]Entry{
		"reason edited":   mut(func(e []Entry) []Entry { e[1].Reason = "fine"; return e }),
		"entry removed":   mut(func(e []Entry) []Entry { return append(e[:2], e[3:]...) }),
		"entries swapped": mut(func(e []Entry) []Entry { e[0], e[1] = e[1], e[0]; return e }),
		"disposition":     mut(func(e []Entry) []Entry { e[1].Disposition = Committed; return e }),
		"head removed":    mut(func(e []Entry) []Entry { return e[1:] }),
	} {
		if err := Verify(es); !errors.Is(err, ErrChain) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A truncated tail is a valid shorter chain. Only the anchor reveals it.
	short := all[:len(all)-2]
	if err := Verify(short); err != nil {
		t.Fatalf("a shorter prefix is a valid chain: %v", err)
	}
	if err := VerifyAnchored(short, anchor); !errors.Is(err, ErrAnchor) {
		t.Fatalf("truncation not caught by the anchor: %v", err)
	}
	if err := VerifyAnchored(all, anchor); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyAgainstSchemaCatchesLaunderedRequests(t *testing.T) {
	l := NewLog(nil)
	l.Append(Entry{Subject: "a", Index: 0, Request: "effect (door x) smash", Disposition: Committed})
	if err := VerifyAgainst(l.Entries(), testSchema()); !errors.Is(err, ErrLogEntry) {
		t.Fatalf("a schema-invalid request recorded as committed: %v", err)
	}
	l = NewLog(nil)
	l.Append(Entry{Subject: "a", Index: 0, Request: "effect (door 3) open", Disposition: Malformed})
	if err := VerifyAgainst(l.Entries(), testSchema()); !errors.Is(err, ErrLogEntry) {
		t.Fatalf("a valid request recorded as malformed: %v", err)
	}
	l = NewLog(nil)
	l.Append(Entry{Subject: "a", Index: 2, Disposition: NoOutput})
	if err := VerifyAgainst(l.Entries(), testSchema()); !errors.Is(err, ErrLogEntry) {
		t.Fatalf("whole-output record with an index: %v", err)
	}
	l = NewLog(nil)
	l.Append(Entry{Subject: "a", Index: -1, Disposition: "vanished"})
	if err := VerifyAgainst(l.Entries(), testSchema()); !errors.Is(err, ErrLogEntry) {
		t.Fatalf("unknown disposition: %v", err)
	}
}

func TestJSONLSinkRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	h := &Host{Schema: testSchema(), Auth: (&Policy{}).Grant("a", "door", "open"), World: newDoors(), Log: NewLog(&buf)}
	h.HandleOutcome(1, "a", "r", term.MustParse("effects (effect (door 1) open) (effect (door 2) bad)"), false)
	back, err := ReadJSONL(&buf)
	if err != nil || !reflect.DeepEqual(back, h.Log.Entries()) {
		t.Fatalf("round trip: %v", err)
	}
	if _, err := ReadJSONL(strings.NewReader(`{"seq":0,"bogus":1}`)); !errors.Is(err, ErrChain) {
		t.Fatalf("unknown field accepted: %v", err)
	}
}

// The experiment. Two entities propose conflicting things about door 3. Their
// programs reach the same normal form under every strategy and under every
// subdivision of the same total fuel, so the proposals are identical in every
// run. The committed outcome then differs only with the order in which the host
// processes the entities, which is the host's decision and nothing else's.
func TestProposalsAreStrategyAndScheduleInvariantCommitsAreOrderDependent(t *testing.T) {
	programs := map[string]string{
		"a": "I effects (K (effect (door 3) open) (W K z))",
		"b": "B effects I (effect (door 3) close)",
	}
	canonical := map[string]string{
		"a": "effects (effect (door 3) open)",
		"b": "effects (effect (door 3) close)",
	}

	// Same normal form under every strategy, hence the same proposals.
	for id, src := range programs {
		for _, s := range []reduce.Strategy{reduce.LeftmostOutermost, reduce.LeftmostInnermost, reduce.RightmostInnermost, reduce.RightmostOutermost} {
			if got := normalForm(t, src, s).String(); got != canonical[id] {
				t.Fatalf("%s under %s: %s", id, s, got)
			}
		}
	}

	// Same proposals under every frame subdivision of 40 total steps.
	var inits []sched.Init
	for id, src := range programs {
		inits = append(inits, sched.Init{ID: id, Term: term.MustParse(src)})
	}
	for _, budget := range []uint64{1, 2, 4, 5, 8, 10, 20, 40} {
		w, err := sched.New(sched.HostConfig(), sched.Policy{Strategy: reduce.LeftmostOutermost, Budget: budget}, inits)
		if err != nil {
			t.Fatal(err)
		}
		if err := w.Run(int(40 / budget)); err != nil {
			t.Fatal(err)
		}
		for _, st := range w.States() {
			if !st.Normal || st.Term != canonical[st.ID] {
				t.Fatalf("budget %d: %+v", budget, st)
			}
		}
	}

	// Host ordering is the only thing that moves the committed state.
	run := func(order []string) (map[string]bool, []Disposition) {
		world := newDoors()
		p := (&Policy{}).Grant("a", "door", "open").Grant("b", "door", "close")
		h := host(world, p)
		var ds []Disposition
		for _, id := range order {
			e, err := h.HandleOutcome(0, id, "", term.MustParse(canonical[id]), false)
			if err != nil {
				t.Fatal(err)
			}
			for _, x := range e {
				ds = append(ds, x.Disposition)
			}
		}
		return world.open, ds
	}
	ab, dab := run([]string{"a", "b"})
	ba, dba := run([]string{"b", "a"})
	if ab["3"] || !ba["3"] {
		t.Fatalf("expected order to decide door 3: a,b -> %v ; b,a -> %v", ab, ba)
	}
	if !reflect.DeepEqual(dab, dba) {
		t.Fatalf("dispositions should match, only the final state differs: %v %v", dab, dba)
	}
}

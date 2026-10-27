package effects

import (
	"strings"
	"testing"

	"github.com/standardgalactic/spherepop-kernel/reduce"
)

// txWorld is a toy world with an atomic commit built on a snapshot.
type txWorld struct {
	*doors
	groups int
}

func (w *txWorld) CommitAll(rs []Request) error {
	w.groups++
	snap := map[string]bool{}
	for k, v := range w.open {
		snap[k] = v
	}
	for i, r := range rs {
		if err := w.Commit(r); err != nil {
			w.open = snap
			return err_(i, err)
		}
	}
	return nil
}

func err_(i int, err error) error { return &memberErr{i, err} }

type memberErr struct {
	i   int
	err error
}

func (e *memberErr) Error() string { return "member " + string(rune('0'+e.i)) + ": " + e.err.Error() }

func groupHost(w World) *Host {
	p := (&Policy{}).Grant("s", "door", "open").Grant("s", "door", "close")
	return host(w, p)
}

func handle(t *testing.T, h *Host, src string) []Entry {
	t.Helper()
	es, err := h.Handle(0, "s", "ref", reduce.Result{Term: normalForm(t, src, reduce.LeftmostOutermost)})
	if err != nil {
		t.Fatal(err)
	}
	return es
}

func TestAtomicGroupCommitsAllOrNothing(t *testing.T) {
	w := &txWorld{doors: newDoors()}
	h := groupHost(w)

	es := handle(t, h, "effects (atomic (effect (door 1) open) (effect (door 2) open))")
	if len(es) != 1 || es[0].Disposition != Committed || es[0].Request != "atomic (effect (door 1) open) (effect (door 2) open)" {
		t.Fatalf("committed group: %+v", es)
	}
	if !w.open["1"] || !w.open["2"] {
		t.Fatal("members not applied")
	}

	// The second member names a door that does not exist: neither applies.
	es = handle(t, h, "effects (atomic (effect (door 1) close) (effect (door 0) open))")
	if len(es) != 1 || es[0].Disposition != Failed {
		t.Fatalf("failed group: %+v", es)
	}
	if !w.open["1"] {
		t.Fatal("first member took effect although the group failed")
	}
}

func TestAtomicGroupDeniedWhenAnyMemberIsAndNeverReachesTheWorld(t *testing.T) {
	w := &txWorld{doors: newDoors()}
	h := groupHost(w)
	es := handle(t, h, "effects (atomic (effect (door 1) open) (effect (lamp x) dim 3))")
	if len(es) != 1 || es[0].Disposition != Denied || !strings.Contains(es[0].Reason, "member 1") {
		t.Fatalf("%+v", es)
	}
	if w.commits != 0 || w.groups != 0 {
		t.Fatal("an unauthorized group reached the world")
	}
}

func TestAtomicGroupMalformedCases(t *testing.T) {
	w := &txWorld{doors: newDoors()}
	h := groupHost(w)
	for _, src := range []string{
		"effects atomic",
		"effects (atomic (effect (door 1) open) (effect (door x) open))",
		"effects (atomic (atomic (effect (door 1) open)))",
		"effects (atomic (effect (door 1) open) junk)",
	} {
		es := handle(t, h, src)
		if len(es) != 1 || es[0].Disposition != Malformed {
			t.Fatalf("%s: %+v", src, es)
		}
	}
	if w.commits != 0 {
		t.Fatal("a malformed group reached the world")
	}
}

func TestAtomicGroupNeedsAnAtomicWorldAndAGroupIsOneRequestForTheLimit(t *testing.T) {
	h := groupHost(newDoors()) // plain World
	es := handle(t, h, "effects (atomic (effect (door 1) open))")
	if es[0].Disposition != Failed || !strings.Contains(es[0].Reason, "atomically") {
		t.Fatalf("%+v", es)
	}
	h = groupHost(&txWorld{doors: newDoors()})
	h.MaxRequests = 1
	es = handle(t, h, "effects (atomic (effect (door 1) open) (effect (door 2) open)) (effect (door 3) open)")
	if len(es) != 2 || es[0].Disposition != Committed || es[1].Disposition != Truncated {
		t.Fatalf("%+v", es)
	}
}

func TestVerifyAgainstAcceptsGroupsAndRejectsLaunderedOnes(t *testing.T) {
	w := &txWorld{doors: newDoors()}
	h := groupHost(w)
	handle(t, h, "effects (atomic (effect (door 1) open) (effect (door 2) open))")
	if err := VerifyAgainst(h.Log.Entries(), testSchema()); err != nil {
		t.Fatal(err)
	}
	// A group with an undecodable member cannot be presented as committed.
	l := NewLog(nil)
	l.Append(Entry{Index: 0, Request: "atomic (effect (door 1) open) (effect (door x) open)", Disposition: Committed})
	if err := VerifyAgainst(l.Entries(), testSchema()); err == nil {
		t.Fatal("laundered group accepted")
	}
	// Reserved word.
	bad := Schema{Targets: map[string]TargetSpec{"atomic": {ID: Int, Verbs: map[string]VerbSpec{"open": {}}}}}
	if bad.Validate() == nil {
		t.Fatal("kind atomic accepted")
	}
}

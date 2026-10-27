package reduce

import (
	"context"
	"testing"

	"github.com/standardgalactic/spherepop-kernel/term"
)

func TestCapGraphAndBitTricks(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Root = PermW | PermK | PermC
	cfg.Perm = map[string]PermSet{
		"a": PermW | PermK,
		"b": PermK | PermC,
	}
	g := cfg.BuildCapGraph()
	if g.IsMonotone() {
		t.Fatalf("expected non-monotone graph when Root=%s and a=%s", cfg.Root, cfg.Perm["a"])
	}
	// From W|K|C:
	// -0 steps: W|K|C
	// -via a: W|K
	// -via b: K|C
	// -via a then b: K
	if got := g.ReachableCount(cfg.Root); got != 4 {
		t.Fatalf("ReachableCount(Root) = %d, want 4", got)
	}
	if !g.CanReach(cfg.Root, PermK) {
		t.Fatalf("expected PermK reachable from Root via a->b")
	}
	if g.CanReach(cfg.Root, PermS) {
		t.Fatalf("did not expect PermS reachable from Root")
	}
	if d := HammingAttenuation(cfg.Root, PermK); d != 2 {
		t.Fatalf("HammingAttenuation(W|K|C, K) = %d, want 2", d)
	}

	monoCfg := DefaultConfig()
	monoCfg.Root = PermK
	monoCfg.Perm = map[string]PermSet{"a": PermW | PermK, "b": PermAll}
	if !monoCfg.BuildCapGraph().IsMonotone() {
		t.Fatalf("expected monotone graph when Root ⊆ Perm[l] for all l")
	}
}

func TestRingBufferAndParallelBatch(t *testing.T) {
	rb := NewRingBuffer[int](8)
	for i := 0; i < 8; i++ {
		if !rb.TryEnqueue(i * 10) {
			t.Fatalf("TryEnqueue(%d) failed", i)
		}
	}
	if rb.TryEnqueue(999) {
		t.Fatalf("expected full ring buffer to reject 9th item")
	}
	for i := 0; i < 8; i++ {
		v, ok := rb.TryDequeue()
		if !ok || v != i*10 {
			t.Fatalf("TryDequeue() = (%d, %v), want (%d, true)", v, ok, i*10)
		}
	}
	if _, ok := rb.TryDequeue(); ok {
		t.Fatalf("expected empty ring buffer")
	}

	exprs := []string{
		"S K K x",
		"B f g x",
		"C f x y",
		"W f x",
		"K a b",
		"I z",
		"S (K (S I)) K x y",
		"B (B f g) h x",
	}
	terms := make([]*term.Term, len(exprs))
	for i, s := range exprs {
		tr, err := term.Parse(s)
		if err != nil {
			t.Fatalf("Parse(%q): %v", s, err)
		}
		terms[i] = tr
	}

	cfg := DefaultConfig()
	got, err := cfg.ReduceBatchParallel(context.Background(), terms, LeftmostOutermost, 64, 4)
	if err != nil {
		t.Fatalf("ReduceBatchParallel: %v", err)
	}
	for i, tr := range terms {
		want, err := cfg.Reduce(tr, LeftmostOutermost, 64)
		if err != nil {
			t.Fatalf("Reduce(%d): %v", i, err)
		}
		if !term.Equal(got[i].Term, want.Term) || got[i].Steps != want.Steps {
			t.Fatalf("item %d mismatch: got (%s, %d), want (%s, %d)", i, got[i].Term, got[i].Steps, want.Term, want.Steps)
		}
	}
}

func BenchmarkInsensitiveBitTrick(b *testing.B) {
	tr, err := term.Parse("B (B (I f) (I g)) (B h (I x))")
	if err != nil {
		b.Fatal(err)
	}
	cfg := DefaultConfig()
	s := newSession(&cfg)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !s.isInsensitive(tr) {
			b.Fatal("expected insensitive")
		}
	}
}

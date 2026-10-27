package sched

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/standardgalactic/spherepop-kernel/reduce"
	"github.com/standardgalactic/spherepop-kernel/term"
)

func TestTickParallelBitIdenticalToSequential(t *testing.T) {
	cfg := HostConfig()
	cfg.Perm = map[string]reduce.PermSet{"a": reduce.PermAll}
	pol := Policy{Strategy: reduce.LeftmostOutermost, Budget: 2}

	specs := []struct{ id, src string }{
		{"e1", "S K K x"},
		{"e2", "B f g x"},
		{"e3", "C f x y"},
		{"e4", "W f x"},
		{"e5", "S (K (S I)) K p q"},
		{"e6", "sc_a (K u v)"},
	}

	buildInits := func() []Init {
		out := make([]Init, len(specs))
		for i, sp := range specs {
			tr, err := term.Parse(sp.src)
			if err != nil {
				t.Fatalf("Parse(%q): %v", sp.src, err)
			}
			out[i] = Init{ID: sp.id, Term: tr}
		}
		return out
	}

	seqWorld, err := New(cfg, pol, buildInits())
	if err != nil {
		t.Fatalf("New seqWorld: %v", err)
	}
	parWorld, err := New(cfg, pol, buildInits())
	if err != nil {
		t.Fatalf("New parWorld: %v", err)
	}

	if err := seqWorld.Run(4); err != nil {
		t.Fatalf("seqWorld.Run: %v", err)
	}
	if err := parWorld.RunParallel(context.Background(), 4, 4); err != nil {
		t.Fatalf("parWorld.RunParallel: %v", err)
	}

	parLog := parWorld.Log()
	if err := CheckLog(parLog); err != nil {
		t.Fatalf("CheckLog(parLog) failed: %v", err)
	}

	seqBytes, _ := json.Marshal(seqWorld.Log())
	parBytes, _ := json.Marshal(parLog)
	if string(seqBytes) != string(parBytes) {
		t.Fatalf("parallel scheduler log differs from sequential log:\nseq=%s\npar=%s", seqBytes, parBytes)
	}
}

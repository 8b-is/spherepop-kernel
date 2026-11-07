package encode

import (
	"encoding/hex"
	"testing"
)

// These golden vectors are produced by wupz-core (Rust) and pin the Go encode
// package to the Rust kernel bit-for-bit. If either side drifts, this test
// fails — the Go↔Rust bitplane witness the architectural review asked for.
//
// Source of truth: wupz-rs/src/fold.rs
//   pack_fold_bfp64(fold, qt) = (fold & qt_broadcast_mask(qt), fold & !mask)
//   seal_frame_32b(raw, packed, gate)

func TestRustParityPackFoldBFP64(t *testing.T) {
	// wupz-core: pack_fold_bfp64(0xe1034e575c371ab0, 0x55)
	const (
		wantPos uint64 = 0x4101445554151010
		wantNeg uint64 = 0xa0020a0208220aa0
	)
	pos, neg := PackFoldBFP64(Fold0xE1034E575C371AB0, QuantQT55)
	if pos != wantPos {
		t.Fatalf("PackFoldBFP64 pos = %#016x, want %#016x", pos, wantPos)
	}
	if neg != wantNeg {
		t.Fatalf("PackFoldBFP64 neg = %#016x, want %#016x", neg, wantNeg)
	}
	if pos&neg != 0 {
		t.Fatalf("PackFoldBFP64 planes overlap: pos=%#x neg=%#x", pos, neg)
	}
}

func TestRustParitySealFrame32B(t *testing.T) {
	// wupz-core: seal_frame_32b([0x62; 3761], [0x33; 3314], 0x62)
	raw := make([]byte, 3761)
	for i := range raw {
		raw[i] = 0x62
	}
	packed := make([]byte, 3314)
	for i := range packed {
		packed[i] = 0x33
	}
	const wantHex = "62000000000000000000000000000000b10e0000f20c00006201b00e0000f20c"

	frame := SealFrame32B(raw, packed, Gate0x62)
	if got := hex.EncodeToString(frame[:]); got != wantHex {
		t.Fatalf("SealFrame32B = %s, want %s", got, wantHex)
	}
	if !CheckFrame32B(frame, raw, packed, Gate0x62) {
		t.Fatal("CheckFrame32B rejected a frame it just sealed")
	}
}

func TestRustParityXORFold(t *testing.T) {
	// wupz-core: xor_fold([0x62; 3761]) == 0x62, xor_fold([0x33; 3314]) == 0
	raw := make([]byte, 3761)
	for i := range raw {
		raw[i] = 0x62
	}
	packed := make([]byte, 3314)
	for i := range packed {
		packed[i] = 0x33
	}
	if got := XORFold(raw); got != 0x62 {
		t.Fatalf("XORFold(raw) = %#x, want 0x62", got)
	}
	if got := XORFold(packed); got != 0 {
		t.Fatalf("XORFold(packed) = %#x, want 0", got)
	}
}

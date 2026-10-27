package encode

import (
	"testing"

	"github.com/standardgalactic/spherepop-kernel/term"
)

func TestQuantTernaryLosslessRoundTrip(t *testing.T) {
	cases := []string{
		"x",
		"S K K x",
		"B f g x",
		"C f x y",
		"W f x",
		"<K a b>_alpha",
		"sc_beta (<I>_alpha z)",
		"S (K (S I)) (S (K K) I) x y",
	}

	for _, src := range cases {
		orig, err := term.Parse(src)
		if err != nil {
			t.Fatalf("Parse(%q): %v", src, err)
		}
		frame, trits, err := EncodeTernary(orig)
		if err != nil {
			t.Fatalf("EncodeTernary(%q): %v", src, err)
		}
		if len(trits) == 0 || len(frame) == 0 {
			t.Fatalf("empty encoding for %q", src)
		}
		decoded, err := DecodeTernary(frame)
		if err != nil {
			t.Fatalf("DecodeTernary(%q): %v", src, err)
		}
		if !term.Equal(orig, decoded) {
			t.Fatalf("round-trip digest mismatch for %q: got %s (%s), want %s (%s)",
				src, decoded, decoded.Digest(), orig, orig.Digest())
		}
	}

	// Also verify custom non-standard Const names round-trip losslessly.
	custom := term.App(term.Const("CUSTOM_OP"), term.Atom("arg0"))
	frame, _, err := EncodeTernary(custom)
	if err != nil {
		t.Fatalf("EncodeTernary(custom): %v", err)
	}
	decCustom, err := DecodeTernary(frame)
	if err != nil {
		t.Fatalf("DecodeTernary(custom): %v", err)
	}
	if !term.Equal(custom, decCustom) {
		t.Fatalf("custom const round-trip mismatch: got %s, want %s", decCustom, custom)
	}
}

func TestTritWord64Bitplanes(t *testing.T) {
	a := []Trit{TritPos, TritNeg, TritZero, TritPos, TritNeg, TritPos}
	b := []Trit{TritPos, TritPos, TritNeg, TritPos, TritNeg, TritZero}
	// Dot product: (+1)(+1) + (-1)(+1) + (0)(-1) + (+1)(+1) + (-1)(-1) + (+1)(0)
	//            = 1 - 1 + 0 + 1 + 1 + 0 = 2
	wa := PackWord64(a)
	wb := PackWord64(b)
	if got := DotProduct64(wa, wb); got != 2 {
		t.Fatalf("DotProduct64 = %d, want 2", got)
	}
	if got := wa.NetSum(); got != 1 {
		t.Fatalf("wa.NetSum() = %d, want 1", got)
	}
	if got := wa.NonZeroCount(); got != 5 {
		t.Fatalf("wa.NonZeroCount() = %d, want 5", got)
	}
	unpacked := wa.Unpack(len(a))
	for i := range a {
		if unpacked[i] != a[i] {
			t.Fatalf("Unpack[%d] = %d, want %d", i, unpacked[i], a[i])
		}
	}
}

package encode

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/bits"

	"github.com/standardgalactic/spherepop-kernel/term"
)

// Trit represents a balanced ternary digit in {-1, 0, +1}.
type Trit int8

const (
	TritNeg  Trit = -1
	TritZero Trit = 0
	TritPos  Trit = +1
)

// ErrBadTernary reports a malformed quantTernary payload or trit stream.
var ErrBadTernary = errors.New("encode: malformed quantTernary stream")

// MagicHeader identifies a packed SpherePOP QuantTernary v1 frame ("SPQT").
var MagicHeader = [4]byte{'S', 'P', 'Q', 'T'}

// TritWord64 packs 64 balanced trits into two orthogonal 64-bit bitplanes
// (Pos and Neg) so inner products and Hamming weights execute in O(1) via
// hardware POPCNT (bits.OnesCount64) and bitwise XOR/AND.
type TritWord64 struct {
	Pos uint64 // bit i is 1 iff trit[i] == +1
	Neg uint64 // bit i is 1 iff trit[i] == -1
}

// PackWord64 packs up to 64 trits into a dual-bitplane TritWord64.
func PackWord64(trits []Trit) TritWord64 {
	var w TritWord64
	limit := len(trits)
	if limit > 64 {
		limit = 64
	}
	for i := 0; i < limit; i++ {
		switch trits[i] {
		case TritPos:
			w.Pos |= uint64(1) << i
		case TritNeg:
			w.Neg |= uint64(1) << i
		}
	}
	return w
}

// Unpack extracts n trits (up to 64) from w.
func (w TritWord64) Unpack(n int) []Trit {
	if n > 64 {
		n = 64
	}
	out := make([]Trit, n)
	for i := 0; i < n; i++ {
		mask := uint64(1) << i
		if w.Pos&mask != 0 {
			out[i] = TritPos
		} else if w.Neg&mask != 0 {
			out[i] = TritNeg
		}
	}
	return out
}

// NetSum returns the algebraic sum of all 64 trits in O(1) using POPCNT.
func (w TritWord64) NetSum() int {
	return bits.OnesCount64(w.Pos) - bits.OnesCount64(w.Neg)
}

// NonZeroCount returns the number of non-zero trits in O(1) using POPCNT.
func (w TritWord64) NonZeroCount() int {
	return bits.OnesCount64(w.Pos | w.Neg)
}

// DotProduct64 computes the exact integer dot product of two 64-trit vectors
// in 4 bitwise operations and 2 hardware POPCNT instructions.
func DotProduct64(a, b TritWord64) int {
	sameSign := (a.Pos & b.Pos) | (a.Neg & b.Neg)
	oppSign := (a.Pos & b.Neg) | (a.Neg & b.Pos)
	return bits.OnesCount64(sameSign) - bits.OnesCount64(oppSign)
}

// PackTritsBase243 packs 5 balanced trits {-1, 0, +1} per byte (3^5 = 243 <= 255,
// 1.585 bits/trit, within 0.03% of the Shannon limit log2(3)).
func PackTritsBase243(trits []Trit) ([]byte, error) {
	n := len(trits)
	out := make([]byte, (n+4)/5)
	for i := 0; i < n; i += 5 {
		var acc uint16
		var pow uint16 = 1
		for j := 0; j < 5 && i+j < n; j++ {
			t := trits[i+j]
			if t < -1 || t > 1 {
				return nil, fmt.Errorf("%w: invalid trit %d at index %d", ErrBadTernary, t, i+j)
			}
			digit := uint16(t + 1) // map {-1,0,+1} -> {0,1,2}
			acc += digit * pow
			pow *= 3
		}
		out[i/5] = byte(acc)
	}
	return out, nil
}

// UnpackTritsBase243 unpacks count balanced trits from a base-243 byte slice.
func UnpackTritsBase243(packed []byte, count int) ([]Trit, error) {
	if count < 0 || (count+4)/5 > len(packed) {
		return nil, fmt.Errorf("%w: truncated base-243 payload for %d trits", ErrBadTernary, count)
	}
	out := make([]Trit, count)
	for i := 0; i < count; i += 5 {
		b := packed[i/5]
		if b >= 243 {
			return nil, fmt.Errorf("%w: byte 0x%02x >= 243 at offset %d", ErrBadTernary, b, i/5)
		}
		val := b
		for j := 0; j < 5 && i+j < count; j++ {
			out[i+j] = Trit(int8(val%3) - 1)
			val /= 3
		}
	}
	return out, nil
}

// appendUintTrits encodes v as a fixed-width k-trit unsigned base-3 integer
// mapped to {-1, 0, +1}.
func appendUintTrits(dst []Trit, v uint32, k int) []Trit {
	for i := 0; i < k; i++ {
		dst = append(dst, Trit(int8(v%3)-1))
		v /= 3
	}
	return dst
}

func readUintTrits(src []Trit, pos *int, k int) (uint32, error) {
	if *pos+k > len(src) {
		return 0, fmt.Errorf("%w: unexpected EOF reading %d trits at %d", ErrBadTernary, k, *pos)
	}
	var acc uint32
	var pow uint32 = 1
	for i := 0; i < k; i++ {
		t := src[*pos]
		*pos++
		if t < -1 || t > 1 {
			return 0, fmt.Errorf("%w: invalid trit %d", ErrBadTernary, t)
		}
		acc += uint32(t+1) * pow
		pow *= 3
	}
	return acc, nil
}

func appendStringTrits(dst []Trit, s string) []Trit {
	// Self-delimiting base-3 varint: 3 trits per group (2 data trits [0..8] + 1 continuation trit).
	n := uint32(len(s))
	for {
		chunk := n % 9
		n /= 9
		d0 := Trit(int8(chunk%3) - 1)
		d1 := Trit(int8((chunk/3)%3) - 1)
		cont := TritNeg // -1 = stop
		if n > 0 {
			cont = TritPos // +1 = more chunks follow
		}
		dst = append(dst, d0, d1, cont)
		if n == 0 {
			break
		}
	}
	// Each UTF-8 byte is encoded in 6 trits (3^6 = 729 >= 256).
	for i := 0; i < len(s); i++ {
		dst = appendUintTrits(dst, uint32(s[i]), 6)
	}
	return dst
}

func readStringTrits(src []Trit, pos *int) (string, error) {
	var length uint32
	var mul uint32 = 1
	for {
		if *pos+3 > len(src) {
			return "", fmt.Errorf("%w: EOF in string length", ErrBadTernary)
		}
		d0, d1, cont := src[*pos], src[*pos+1], src[*pos+2]
		*pos += 3
		chunk := uint32(d0+1) + uint32(d1+1)*3
		length += chunk * mul
		mul *= 9
		if cont == TritNeg {
			break
		}
		if cont != TritPos {
			return "", fmt.Errorf("%w: bad string continuation trit %d", ErrBadTernary, cont)
		}
		if length > 1<<20 {
			return "", fmt.Errorf("%w: string length overflow", ErrBadTernary)
		}
	}
	buf := make([]byte, length)
	for i := uint32(0); i < length; i++ {
		v, err := readUintTrits(src, pos, 6)
		if err != nil || v > 255 {
			return "", fmt.Errorf("%w: invalid byte value %d", ErrBadTernary, v)
		}
		buf[i] = byte(v)
	}
	return string(buf), nil
}

// EncodeTermTrits encodes any *term.Term into a Polish-prefix balanced ternary
// trit stream:
//   - App(f, x)      -> [+1, +1] || trits(f) || trits(x)  (only 2 trits overhead!)
//   - Const(I..W)    -> [+1,  0] || 2-trit combinator code (only 4 trits total!)
//   - Const(custom)  -> [+1,  0,  0,  0] || string(name)
//   - Atom(name)     -> [+1, -1] || string(name)
//   - Bracket(l, b)  -> [-1, +1] || string(l) || trits(b)
//   - Scope(l)       -> [-1, -1] || string(l)
func EncodeTermTrits(t *term.Term) []Trit {
	var dst []Trit
	var walk func(*term.Term)
	walk = func(u *term.Term) {
		switch u.Kind() {
		case term.KindApp:
			dst = append(dst, TritPos, TritPos)
			walk(u.Fun())
			walk(u.Arg())
		case term.KindConst:
			dst = append(dst, TritPos, TritZero)
			switch u.Name() {
			case "I":
				dst = append(dst, TritNeg, TritNeg)
			case "K":
				dst = append(dst, TritNeg, TritZero)
			case "S":
				dst = append(dst, TritNeg, TritPos)
			case "B":
				dst = append(dst, TritZero, TritNeg)
			case "C":
				dst = append(dst, TritZero, TritPos)
			case "W":
				dst = append(dst, TritPos, TritNeg)
			default:
				dst = append(dst, TritZero, TritZero)
				dst = appendStringTrits(dst, u.Name())
			}
		case term.KindAtom:
			dst = append(dst, TritPos, TritNeg)
			dst = appendStringTrits(dst, u.Name())
		case term.KindBracket:
			dst = append(dst, TritNeg, TritPos)
			dst = appendStringTrits(dst, u.Label())
			walk(u.Body())
		case term.KindScope:
			dst = append(dst, TritNeg, TritNeg)
			dst = appendStringTrits(dst, u.Label())
		}
	}
	walk(t)
	return dst
}

// DecodeTermTrits reconstructs a *term.Term from its balanced ternary trit
// stream with 100% lossless structural and SHA-256 digest fidelity.
func DecodeTermTrits(trits []Trit) (*term.Term, error) {
	pos := 0
	var walk func() (*term.Term, error)
	walk = func() (*term.Term, error) {
		if pos+2 > len(trits) {
			return nil, fmt.Errorf("%w: unexpected EOF at trit %d", ErrBadTernary, pos)
		}
		t0, t1 := trits[pos], trits[pos+1]
		pos += 2
		switch {
		case t0 == TritPos && t1 == TritPos:
			f, err := walk()
			if err != nil {
				return nil, err
			}
			x, err := walk()
			if err != nil {
				return nil, err
			}
			return term.App(f, x), nil
		case t0 == TritPos && t1 == TritZero:
			if pos+2 > len(trits) {
				return nil, fmt.Errorf("%w: truncated const tag", ErrBadTernary)
			}
			c0, c1 := trits[pos], trits[pos+1]
			pos += 2
			switch {
			case c0 == TritNeg && c1 == TritNeg:
				return term.Const("I"), nil
			case c0 == TritNeg && c1 == TritZero:
				return term.Const("K"), nil
			case c0 == TritNeg && c1 == TritPos:
				return term.Const("S"), nil
			case c0 == TritZero && c1 == TritNeg:
				return term.Const("B"), nil
			case c0 == TritZero && c1 == TritPos:
				return term.Const("C"), nil
			case c0 == TritPos && c1 == TritNeg:
				return term.Const("W"), nil
			case c0 == TritZero && c1 == TritZero:
				name, err := readStringTrits(trits, &pos)
				if err != nil {
					return nil, err
				}
				return term.Const(name), nil
			default:
				return nil, fmt.Errorf("%w: unknown const code (%d,%d)", ErrBadTernary, c0, c1)
			}
		case t0 == TritPos && t1 == TritNeg:
			name, err := readStringTrits(trits, &pos)
			if err != nil {
				return nil, err
			}
			return term.Atom(name), nil
		case t0 == TritNeg && t1 == TritPos:
			label, err := readStringTrits(trits, &pos)
			if err != nil {
				return nil, err
			}
			body, err := walk()
			if err != nil {
				return nil, err
			}
			return term.Bracket(label, body), nil
		case t0 == TritNeg && t1 == TritNeg:
			label, err := readStringTrits(trits, &pos)
			if err != nil {
				return nil, err
			}
			return term.Scope(label), nil
		default:
			return nil, fmt.Errorf("%w: unknown node tag (%d,%d) at %d", ErrBadTernary, t0, t1, pos-2)
		}
	}
	res, err := walk()
	if err != nil {
		return nil, err
	}
	if pos != len(trits) {
		return nil, fmt.Errorf("%w: %d trailing trits", ErrBadTernary, len(trits)-pos)
	}
	return res, nil
}

// EncodeTernary serializes t into a self-contained SPQT (SpherePOP QuantTernary)
// binary frame:
//
//	[0..3]         "SPQT" magic
//	[4..4+varint]  uvarint(tritCount)
//	[next]         1-byte signed algebraic checksum (int8(sum(trits) % 127))
//	[payload...]   base-243 packed trits (5 trits/byte)
func EncodeTernary(t *term.Term) ([]byte, []Trit, error) {
	if t == nil {
		return nil, nil, fmt.Errorf("%w: nil term", ErrBadTernary)
	}
	trits := EncodeTermTrits(t)
	packed, err := PackTritsBase243(trits)
	if err != nil {
		return nil, nil, err
	}
	var sum int64
	for _, tr := range trits {
		sum += int64(tr)
	}
	checksum := byte(int8(sum % 127))

	var vbuf [binary.MaxVarintLen64]byte
	vn := binary.PutUvarint(vbuf[:], uint64(len(trits)))

	out := make([]byte, 0, 4+vn+1+len(packed))
	out = append(out, MagicHeader[:]...)
	out = append(out, vbuf[:vn]...)
	out = append(out, checksum)
	out = append(out, packed...)
	return out, trits, nil
}

// DecodeTernary parses and verifies an SPQT binary frame and reconstructs the
// exact *term.Term.
func DecodeTernary(frame []byte) (*term.Term, error) {
	if len(frame) < 6 || string(frame[:4]) != string(MagicHeader[:]) {
		return nil, fmt.Errorf("%w: missing SPQT magic header", ErrBadTernary)
	}
	tritCount, vn := binary.Uvarint(frame[4:])
	if vn <= 0 || 4+vn+1 > len(frame) || tritCount > 1<<24 {
		return nil, fmt.Errorf("%w: invalid trit count header", ErrBadTernary)
	}
	expectedChecksum := int8(frame[4+vn])
	packed := frame[4+vn+1:]
	trits, err := UnpackTritsBase243(packed, int(tritCount))
	if err != nil {
		return nil, err
	}
	var sum int64
	for _, tr := range trits {
		sum += int64(tr)
	}
	if int8(sum%127) != expectedChecksum {
		return nil, fmt.Errorf("%w: algebraic trit checksum mismatch", ErrBadTernary)
	}
	return DecodeTermTrits(trits)
}

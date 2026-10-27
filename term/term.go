// Package term defines the structural objects of SpherePOP: immutable,
// content-addressed circle terms. A term is an atom, a constant, a scope
// constructor, an application circle, or a labelled bracket. Nothing in this
// package knows about reduction, permissions, or any host environment.
package term

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"math"
	"sort"
)

// Kind classifies a term node.
type Kind uint8

// The five kinds of node. The numeric values are part of the digest
// definition in SPEC.md and must not change.
const (
	KindAtom    Kind = iota + 1 // a variable or other inert symbol
	KindConst                   // a combinator constant such as S or K
	KindScope                   // the scope constructor sc_l
	KindApp                     // an application circle
	KindBracket                 // a labelled bracket <t>_l
)

func (k Kind) String() string {
	switch k {
	case KindAtom:
		return "atom"
	case KindConst:
		return "const"
	case KindScope:
		return "scope"
	case KindApp:
		return "app"
	case KindBracket:
		return "bracket"
	}
	return fmt.Sprintf("kind(%d)", uint8(k))
}

// Digest is the SHA-256 content address of a term.
type Digest [sha256.Size]byte

// String returns the full lower-case hexadecimal digest.
func (d Digest) String() string { return hex.EncodeToString(d[:]) }

// Short returns the first twelve hexadecimal characters, for display only.
func (d Digest) Short() string { return hex.EncodeToString(d[:6]) }

// ParseDigest reads a digest written by Digest.String.
func ParseDigest(s string) (Digest, error) {
	var d Digest
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != len(d) {
		return d, fmt.Errorf("term: %q is not a %d-byte hexadecimal digest", s, len(d))
	}
	copy(d[:], b)
	return d, nil
}

// domain separates term digests from every other digest in the system.
const domain = "spherepop-term/1"

// domainPrefix is the pre-encoded uvarint(16) + "spherepop-term/1" prefix,
// avoiding per-node varint formatting and heap allocations on the hot path.
var domainPrefix = [17]byte{
	16,
	's', 'p', 'h', 'e', 'r', 'e', 'p', 'o', 'p', '-', 't', 'e', 'r', 'm', '/', '1',
}

// Capability & structural bit-flags cached on every Term node in O(1) during
// construction. Bits 0..3 match reduce.PermW..PermS so insensitivity checks
// collapse to a single bitwise AND with zero tree walks or map allocations.
const (
	FlagW       uint8 = 1 << 0
	FlagK       uint8 = 1 << 1
	FlagC       uint8 = 1 << 2
	FlagS       uint8 = 1 << 3
	FlagI       uint8 = 1 << 4
	FlagB       uint8 = 1 << 5
	FlagBracket uint8 = 1 << 6
	FlagScope   uint8 = 1 << 7
)

func constFlag(name string) uint8 {
	if len(name) != 1 {
		return 0
	}
	switch name[0] {
	case 'W':
		return FlagW
	case 'K':
		return FlagK
	case 'C':
		return FlagC
	case 'S':
		return FlagS
	case 'I':
		return FlagI
	case 'B':
		return FlagB
	}
	return 0
}

// Term is an immutable node. Values are created only by the constructors in
// this package and are shared freely; two terms are equal exactly when their
// digests are equal. The zero value and a nil pointer are not valid terms.
type Term struct {
	kind   Kind
	flags  uint8  // bitwise OR of Flag* across all nodes in the subterm
	name   string // atom or constant name, or the label of a scope or bracket
	left   *Term  // function child of an application, or body of a bracket
	right  *Term  // argument child of an application
	digest Digest
	size   int // number of application circles, saturating
	leaves int // number of leaves, saturating
}

// Flags returns the O(1) structural capability bitmask of t.
func (t *Term) Flags() uint8 { return t.flags }

func satAdd(a, b int) int {
	if a > math.MaxInt-b {
		return math.MaxInt
	}
	return a + b
}

func writeString(h hash.Hash, s string) {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], uint64(len(s)))
	h.Write(buf[:n])
	h.Write([]byte(s))
}

func leaf(kind Kind, name string) *Term {
	var flags uint8
	switch kind {
	case KindConst:
		flags = constFlag(name)
	case KindScope:
		flags = FlagScope
	}
	t := &Term{kind: kind, flags: flags, name: name, leaves: 1}
	// Stack-allocated zero-alloc fast path for common short identifiers (<= 100 bytes).
	if len(name) <= 100 {
		var buf [128]byte
		copy(buf[:17], domainPrefix[:])
		buf[17] = byte(kind)
		n := binary.PutUvarint(buf[18:], uint64(len(name)))
		copy(buf[18+n:], name)
		t.digest = sha256.Sum256(buf[:18+n+len(name)])
		return t
	}
	h := sha256.New()
	h.Write(domainPrefix[:])
	h.Write([]byte{byte(kind)})
	writeString(h, name)
	h.Sum(t.digest[:0])
	return t
}

// Atom returns an inert symbol, such as a variable.
func Atom(name string) *Term { return leaf(KindAtom, name) }

// Const returns a constant, such as S or K. Whether it has a reduction rule
// is decided by the reducer's configuration and not by the term.
func Const(name string) *Term { return leaf(KindConst, name) }

// Scope returns the scope constructor sc_label.
func Scope(label string) *Term { return leaf(KindScope, label) }

// App returns the application circle f x using a stack-allocated 82-byte buffer
// for zero-allocation SHA-256 Merkle hashing.
func App(f, x *Term) *Term {
	var buf [17 + 1 + 32 + 32]byte
	copy(buf[:17], domainPrefix[:])
	buf[17] = byte(KindApp)
	copy(buf[18:50], f.digest[:])
	copy(buf[50:82], x.digest[:])
	return &Term{
		kind:   KindApp,
		flags:  f.flags | x.flags,
		left:   f,
		right:  x,
		digest: sha256.Sum256(buf[:]),
		size:   satAdd(satAdd(f.size, x.size), 1),
		leaves: satAdd(f.leaves, x.leaves),
	}
}

// Apply returns f a1 a2 ... an, associating to the left.
func Apply(f *Term, args ...*Term) *Term {
	for _, a := range args {
		f = App(f, a)
	}
	return f
}

// Bracket returns the bracket <body>_label.
func Bracket(label string, body *Term) *Term {
	t := &Term{
		kind:   KindBracket,
		flags:  body.flags | FlagBracket,
		name:   label,
		left:   body,
		size:   body.size,
		leaves: body.leaves,
	}
	if len(label) <= 64 {
		var buf [17 + 1 + 10 + 64 + 32]byte
		copy(buf[:17], domainPrefix[:])
		buf[17] = byte(KindBracket)
		n := binary.PutUvarint(buf[18:], uint64(len(label)))
		copy(buf[18+n:], label)
		pos := 18 + n + len(label)
		copy(buf[pos:pos+32], body.digest[:])
		t.digest = sha256.Sum256(buf[:pos+32])
		return t
	}
	h := sha256.New()
	h.Write(domainPrefix[:])
	h.Write([]byte{byte(KindBracket)})
	writeString(h, label)
	h.Write(body.digest[:])
	h.Sum(t.digest[:0])
	return t
}

// Kind reports the kind of node.
func (t *Term) Kind() Kind { return t.kind }

// Name returns the name of an atom or a constant, and the empty string for
// other kinds.
func (t *Term) Name() string {
	if t.kind == KindAtom || t.kind == KindConst {
		return t.name
	}
	return ""
}

// Label returns the label of a scope constructor or a bracket, and the empty
// string for other kinds.
func (t *Term) Label() string {
	if t.kind == KindScope || t.kind == KindBracket {
		return t.name
	}
	return ""
}

// Fun returns the function child of an application, or nil.
func (t *Term) Fun() *Term {
	if t.kind == KindApp {
		return t.left
	}
	return nil
}

// Arg returns the argument child of an application, or nil.
func (t *Term) Arg() *Term {
	if t.kind == KindApp {
		return t.right
	}
	return nil
}

// Body returns the contents of a bracket, or nil.
func (t *Term) Body() *Term {
	if t.kind == KindBracket {
		return t.left
	}
	return nil
}

// IsLeaf reports whether t is an atom, a constant, or a scope constructor.
func (t *Term) IsLeaf() bool {
	return t.kind == KindAtom || t.kind == KindConst || t.kind == KindScope
}

// Digest returns the content address of t.
func (t *Term) Digest() Digest { return t.digest }

// Size returns the number of application circles in the tree reading of t.
// Atoms, constants and brackets contribute nothing. The value saturates at
// the largest int instead of overflowing.
func (t *Term) Size() int { return t.size }

// Leaves returns the number of leaves in the tree reading of t.
func (t *Term) Leaves() int { return t.leaves }

// Equal reports whether a and b are the same term.
func Equal(a, b *Term) bool { return a.digest == b.digest }

// Head follows function children from t and returns the first node that is
// not an application, together with the number of application circles that
// were passed. A bracket is not an application, so a spine never crosses one.
func (t *Term) Head() (head *Term, n int) {
	head = t
	for head.kind == KindApp {
		head = head.left
		n++
	}
	return head, n
}

// Spine returns the head of t and its arguments in application order.
func (t *Term) Spine() (head *Term, args []*Term) {
	head, n := t.Head()
	if n == 0 {
		return head, nil
	}
	args = make([]*Term, n)
	cur := t
	for i := n - 1; i >= 0; i-- {
		args[i] = cur.right
		cur = cur.left
	}
	return head, args
}

// DistinctNodes returns the number of distinct subterms of t, counting
// leaves, which is the size of its directed acyclic graph when equal
// subterms are shared.
func (t *Term) DistinctNodes() int {
	seen := make(map[Digest]struct{})
	var walk func(*Term)
	walk = func(u *Term) {
		if _, ok := seen[u.digest]; ok {
			return
		}
		seen[u.digest] = struct{}{}
		switch u.kind {
		case KindApp:
			walk(u.left)
			walk(u.right)
		case KindBracket:
			walk(u.left)
		}
	}
	walk(t)
	return len(seen)
}

// Labels returns the sorted set of labels used by brackets and scope
// constructors in t.
func (t *Term) Labels() []string {
	seen := make(map[Digest]struct{})
	set := make(map[string]struct{})
	var walk func(*Term)
	walk = func(u *Term) {
		if _, ok := seen[u.digest]; ok {
			return
		}
		seen[u.digest] = struct{}{}
		switch u.kind {
		case KindScope:
			set[u.name] = struct{}{}
		case KindBracket:
			set[u.name] = struct{}{}
			walk(u.left)
		case KindApp:
			walk(u.left)
			walk(u.right)
		}
	}
	walk(t)
	out := make([]string, 0, len(set))
	for l := range set {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

// Subst replaces every atom named in m by its image. Subterms that contain
// no replaced atom are shared with t and not rebuilt.
func Subst(t *Term, m map[string]*Term) *Term {
	switch t.kind {
	case KindAtom:
		if r, ok := m[t.name]; ok {
			return r
		}
		return t
	case KindApp:
		f, x := Subst(t.left, m), Subst(t.right, m)
		if f == t.left && x == t.right {
			return t
		}
		return App(f, x)
	case KindBracket:
		b := Subst(t.left, m)
		if b == t.left {
			return t
		}
		return Bracket(t.name, b)
	}
	return t
}

// String prints t in the canonical concrete syntax accepted by Parse.
func (t *Term) String() string {
	var b []byte
	b = appendTerm(b, t, false)
	return string(b)
}

func appendTerm(b []byte, t *Term, argument bool) []byte {
	switch t.kind {
	case KindAtom, KindConst:
		return append(b, t.name...)
	case KindScope:
		b = append(b, "sc_"...)
		return append(b, t.name...)
	case KindBracket:
		b = append(b, '<')
		b = appendTerm(b, t.left, false)
		b = append(b, ">_"...)
		return append(b, t.name...)
	case KindApp:
		if argument {
			b = append(b, '(')
		}
		b = appendTerm(b, t.left, false)
		b = append(b, ' ')
		b = appendTerm(b, t.right, true)
		if argument {
			b = append(b, ')')
		}
		return b
	}
	return append(b, '?')
}

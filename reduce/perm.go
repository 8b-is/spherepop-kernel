package reduce

import (
	"fmt"
	"sort"
	"strings"
)

// PermSet is a set of permission-gated constants, stored as a bitmask. The
// alphabet is fixed and finite, as the scoped calculus requires: W, K, C and
// the operational contraction S.
type PermSet uint8

// The members of the permission alphabet.
const (
	PermW PermSet = 1 << iota
	PermK
	PermC
	PermS

	// PermAll is the whole alphabet.
	PermAll = PermW | PermK | PermC | PermS
)

var permNames = []struct {
	name string
	bit  PermSet
}{{"W", PermW}, {"K", PermK}, {"C", PermC}, {"S", PermS}}

// permBit returns the bit for a constant name, and false for constants that
// are outside the alphabet (I and B, which are never gated).
func permBit(name string) (PermSet, bool) {
	if len(name) != 1 {
		return 0, false
	}
	switch name[0] {
	case 'W':
		return PermW, true
	case 'K':
		return PermK, true
	case 'C':
		return PermC, true
	case 'S':
		return PermS, true
	}
	return 0, false
}

// Names returns the members of s in alphabet order.
func (s PermSet) Names() []string {
	var out []string
	for _, p := range permNames {
		if s&p.bit != 0 {
			out = append(out, p.name)
		}
	}
	return out
}

func (s PermSet) String() string { return "{" + strings.Join(s.Names(), ",") + "}" }

// ParsePermSet reads a list of constant names. Unknown names are an error, so
// a mistyped permission cannot silently widen or narrow a scope.
func ParsePermSet(names []string) (PermSet, error) {
	var s PermSet
	for _, n := range names {
		b, ok := permBit(n)
		if !ok {
			return 0, fmt.Errorf("reduce: %q is not in the permission alphabet %v", n, PermAll.Names())
		}
		s |= b
	}
	return s, nil
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

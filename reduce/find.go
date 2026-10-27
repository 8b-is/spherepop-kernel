package reduce

import "github.com/standardgalactic/spherepop-kernel/term"

// Strategy selects which redex to contract when several are available.
type Strategy uint8

// The four deterministic strategies. Outermost means a node is considered
// before its children, innermost after. Leftmost visits the function child
// before the argument, rightmost the reverse.
const (
	LeftmostOutermost Strategy = iota + 1
	LeftmostInnermost
	RightmostInnermost
	RightmostOutermost
)

func (s Strategy) String() string {
	switch s {
	case LeftmostOutermost:
		return "lo"
	case LeftmostInnermost:
		return "li"
	case RightmostInnermost:
		return "ri"
	case RightmostOutermost:
		return "ro"
	}
	return "strategy(?)"
}

// ParseStrategy reads a strategy name and fails closed on anything else.
func ParseStrategy(s string) (Strategy, bool) {
	for _, st := range []Strategy{LeftmostOutermost, LeftmostInnermost, RightmostInnermost, RightmostOutermost} {
		if st.String() == s {
			return st, true
		}
	}
	return 0, false
}

// Redex is a position where a step is available.
type Redex struct {
	Path term.Path
	Rule RuleID
}

type nkey struct {
	d    term.Digest
	mask PermSet
}

// session holds memo tables for one run. Every predicate it memoises is a
// pure function of a term's digest and an effective permission mask.
type session struct {
	cfg       *Config
	quiet     map[nkey]struct{} // subterms with no redex of any kind
	rnormal   map[nkey]bool
	settled   map[nkey]bool
	strategy  Strategy
	count     int
	visit     func(Redex) bool
	pathArr   [64]byte
	pathBuf   []byte
	firstOnly bool
	first     Redex
	found     bool
}

func newSession(c *Config) *session {
	s := &session{
		cfg:     c,
		quiet:   map[nkey]struct{}{},
		rnormal: map[nkey]bool{},
		settled: map[nkey]bool{},
	}
	s.pathBuf = s.pathArr[:0]
	return s
}

// activeConstFlags returns the bitmask of combinator constants that are
// admissible at effective permission mask. Because nested brackets only
// intersect (narrow) permissions, any constant absent from this mask can never
// become admissible anywhere inside the subterm.
func (s *session) activeConstFlags(mask PermSet) uint8 {
	ungated := uint8(PermAll) &^ uint8(s.cfg.Gated)
	permitted := uint8(mask & s.cfg.Gated)
	return ungated | permitted | term.FlagI | term.FlagB
}

// contractionAt reports the contraction redex rooted at n, if any.
func (s *session) contractionAt(n *term.Term, mask PermSet) (RuleID, bool) {
	if n.Kind() != term.KindApp {
		return "", false
	}
	head, k := n.Head()
	if head.Kind() != term.KindConst {
		return "", false
	}
	r, ok := ruleForHead(head.Name())
	if !ok || r.Arity != k || !s.cfg.admissible(r.Head, mask) {
		return "", false
	}
	return r.ID, true
}

// redexAt reports the redex rooted at n, if any: a permitted contraction, an
// Sc step, or an Abs step whose side condition holds.
func (s *session) redexAt(n *term.Term, mask PermSet) (RuleID, bool) {
	if n.Kind() != term.KindApp {
		return "", false
	}
	switch n.Fun().Kind() {
	case term.KindScope:
		if s.scopes(n.Arg(), mask) {
			return RuleSc, true
		}
		return "", false
	case term.KindBracket:
		if s.absorbs(n.Arg(), mask) {
			return RuleAbs, true
		}
		return "", false
	}
	return s.contractionAt(n, mask)
}

// isRNormal reports that v has no permitted contraction at the chain whose
// effective permission is mask. Boundary rules are not part of R.
func (s *session) isRNormal(v *term.Term, mask PermSet) bool {
	// O(1) capability bit-trick: if v contains no combinator constant that is
	// admissible under mask, no R-contraction can exist anywhere in v.
	if v.Flags()&s.activeConstFlags(mask) == 0 {
		return true
	}
	k := nkey{v.Digest(), mask}
	if r, ok := s.rnormal[k]; ok {
		return r
	}
	r := true
	switch v.Kind() {
	case term.KindApp:
		if _, ok := s.contractionAt(v, mask); ok {
			r = false
		} else {
			r = s.isRNormal(v.Fun(), mask) && s.isRNormal(v.Arg(), mask)
		}
	case term.KindBracket:
		r = s.isRNormal(v.Body(), mask&s.cfg.perm(v.Label()))
	}
	s.rnormal[k] = r
	return r
}

// isSettled reports that v is R-normal and has no pending boundary step: no
// application whose function child is a bracket or a scope constructor. The
// scope clause is a deliberately conservative reading, see SPEC.md.
func (s *session) isSettled(v *term.Term, mask PermSet) bool {
	// O(1) bit-trick: if v has no brackets, no scopes, and no admissible
	// constants at mask, it is settled immediately.
	if v.Flags()&(s.activeConstFlags(mask)|term.FlagBracket|term.FlagScope) == 0 {
		return true
	}
	// If v has no brackets or scopes anywhere, settled == R-normal.
	if v.Flags()&(term.FlagBracket|term.FlagScope) == 0 {
		return s.isRNormal(v, mask)
	}
	k := nkey{v.Digest(), mask}
	if r, ok := s.settled[k]; ok {
		return r
	}
	r := true
	switch v.Kind() {
	case term.KindApp:
		if fk := v.Fun().Kind(); fk == term.KindBracket || fk == term.KindScope {
			r = false
		} else if _, ok := s.contractionAt(v, mask); ok {
			r = false
		} else {
			r = s.isSettled(v.Fun(), mask) && s.isSettled(v.Arg(), mask)
		}
	case term.KindBracket:
		r = s.isSettled(v.Body(), mask&s.cfg.perm(v.Label()))
	}
	s.settled[k] = r
	return r
}

// isInsensitive reports that v contains no gated constant in O(1) via the
// cached capability bitmask on Term.
func (s *session) isInsensitive(v *term.Term) bool {
	return v.Flags()&uint8(s.cfg.Gated) == 0
}

// absorbs evaluates the side condition of Abs for argument v at the chain
// whose effective permission is mask.
func (s *session) absorbs(v *term.Term, mask PermSet) bool {
	switch s.cfg.Absorb {
	case AbsAny:
		return true
	case AbsNF:
		return s.isRNormal(v, mask)
	case AbsS:
		return s.isSettled(v, mask)
	case AbsU:
		return s.isRNormal(v, mask) && (s.isSettled(v, mask) || s.isInsensitive(v))
	}
	return false
}

// scopes evaluates the side condition of Sc for argument v at the chain with
// effective permission mask.
func (s *session) scopes(v *term.Term, mask PermSet) bool {
	switch s.cfg.scMode() {
	case ScSafe:
		return s.isRNormal(v, mask) && (s.isSettled(v, mask) || s.isInsensitive(v))
	case ScSettled:
		return s.isSettled(v, mask)
	}
	return true
}

// Safe is the decidable transfer criterion of the scoped-rules essay,
// Proposition 4.11: v is R-normal at the chain with effective permission mask
// and is either settled or insensitive. It is exported so a host can ask
// whether an argument could cross a boundary.
func (c *Config) Safe(v *term.Term, mask PermSet) bool {
	s := newSession(c)
	return s.isRNormal(v, mask) && (s.isSettled(v, mask) || s.isInsensitive(v))
}

// walk visits redexes of t in the strategy's order and reports whether the
// visitor asked to stop. Uses a stack-backed byte buffer for paths so tree
// traversal performs zero string allocations unless a redex is emitted.
func (s *session) walk(t *term.Term, mask PermSet) bool {
	// O(1) bit-trick: if t has no admissible constants, no brackets (or AbsOff),
	// and no scopes, it cannot contain any redex at all.
	possibleFlags := s.activeConstFlags(mask) | term.FlagScope
	if s.cfg.Absorb != AbsOff {
		possibleFlags |= term.FlagBracket
	}
	if t.Flags()&possibleFlags == 0 {
		return false
	}
	k := nkey{t.Digest(), mask}
	if _, ok := s.quiet[k]; ok {
		return false
	}
	before := s.count
	var stop bool
	outer := s.strategy == LeftmostOutermost || s.strategy == RightmostOutermost
	rightFirst := s.strategy == RightmostOutermost || s.strategy == RightmostInnermost
	if outer {
		stop = s.here(t, mask) || s.kids(t, mask, rightFirst)
	} else {
		stop = s.kids(t, mask, rightFirst) || s.here(t, mask)
	}
	if !stop && s.count == before {
		s.quiet[k] = struct{}{}
	}
	return stop
}

func (s *session) here(t *term.Term, mask PermSet) bool {
	id, ok := s.redexAt(t, mask)
	if !ok {
		return false
	}
	s.count++
	r := Redex{Path: term.Path(s.pathBuf), Rule: id}
	if s.firstOnly {
		s.first, s.found = r, true
		return true
	}
	return s.visit(r)
}

func (s *session) kids(t *term.Term, mask PermSet, rightFirst bool) bool {
	switch t.Kind() {
	case term.KindApp:
		if rightFirst {
			s.pathBuf = append(s.pathBuf, term.StepArg)
			stop := s.walk(t.Arg(), mask)
			s.pathBuf = s.pathBuf[:len(s.pathBuf)-1]
			if stop {
				return true
			}
			s.pathBuf = append(s.pathBuf, term.StepFun)
			stop = s.walk(t.Fun(), mask)
			s.pathBuf = s.pathBuf[:len(s.pathBuf)-1]
			return stop
		}
		s.pathBuf = append(s.pathBuf, term.StepFun)
		stop := s.walk(t.Fun(), mask)
		s.pathBuf = s.pathBuf[:len(s.pathBuf)-1]
		if stop {
			return true
		}
		s.pathBuf = append(s.pathBuf, term.StepArg)
		stop = s.walk(t.Arg(), mask)
		s.pathBuf = s.pathBuf[:len(s.pathBuf)-1]
		return stop
	case term.KindBracket:
		s.pathBuf = append(s.pathBuf, term.StepBody)
		stop := s.walk(t.Body(), mask&s.cfg.perm(t.Label()))
		s.pathBuf = s.pathBuf[:len(s.pathBuf)-1]
		return stop
	}
	return false
}

// Redexes returns every available step of t in the order the strategy would
// consider them. The first element is the step the strategy takes.
func (c *Config) Redexes(t *term.Term, strat Strategy) []Redex {
	s := newSession(c)
	s.strategy = strat
	var out []Redex
	s.visit = func(r Redex) bool { out = append(out, r); return false }
	s.walk(t, c.Root)
	return out
}

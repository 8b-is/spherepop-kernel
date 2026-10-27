package reduce

import (
	"errors"

	"github.com/standardgalactic/spherepop-kernel/term"
)

// RuleID is the stable identifier of a reduction rule. Every trace step cites
// one, and the checker resolves it against the table below.
type RuleID string

// The rule identifiers. These strings are part of the trace format.
const (
	RuleI   RuleID = "I"
	RuleB   RuleID = "B"
	RuleC   RuleID = "C"
	RuleK   RuleID = "K"
	RuleW   RuleID = "W"
	RuleS   RuleID = "S"
	RuleAbs RuleID = "Abs"
	RuleSc  RuleID = "Sc"
)

// RuleKind separates the combinatory contractions from the boundary rules.
type RuleKind uint8

// The two kinds of rule.
const (
	Contraction RuleKind = iota + 1 // headed by a constant, gated by permission
	Boundary                        // Abs and Sc, which cross a bracket
)

// Rule is one row of the rule table. The table is data: a host cannot add to
// it, and a trace that cites an identifier outside it is rejected.
type Rule struct {
	ID    RuleID
	Kind  RuleKind
	Head  string // head constant of a contraction
	Arity int    // number of arguments consumed by a contraction
	Doc   string

	contract func(a []*term.Term) *term.Term
}

var table = []Rule{
	{ID: RuleI, Kind: Contraction, Head: "I", Arity: 1, Doc: "I x -> x",
		contract: func(a []*term.Term) *term.Term { return a[0] }},
	{ID: RuleB, Kind: Contraction, Head: "B", Arity: 3, Doc: "B f g x -> f (g x)",
		contract: func(a []*term.Term) *term.Term { return term.App(a[0], term.App(a[1], a[2])) }},
	{ID: RuleC, Kind: Contraction, Head: "C", Arity: 3, Doc: "C f x y -> f y x",
		contract: func(a []*term.Term) *term.Term { return term.Apply(a[0], a[2], a[1]) }},
	{ID: RuleK, Kind: Contraction, Head: "K", Arity: 2, Doc: "K x y -> x",
		contract: func(a []*term.Term) *term.Term { return a[0] }},
	{ID: RuleW, Kind: Contraction, Head: "W", Arity: 2, Doc: "W f x -> f x x",
		contract: func(a []*term.Term) *term.Term { return term.Apply(a[0], a[1], a[1]) }},
	{ID: RuleS, Kind: Contraction, Head: "S", Arity: 3, Doc: "S f g x -> f x (g x)",
		contract: func(a []*term.Term) *term.Term { return term.Apply(a[0], a[2], term.App(a[1], a[2])) }},
	{ID: RuleAbs, Kind: Boundary, Doc: "<u>_l v -> <u v>_l, subject to the absorption mode"},
	{ID: RuleSc, Kind: Boundary, Doc: "sc_l u -> <u>_l"},
}

// Rules returns a copy of the rule table in its canonical order.
func Rules() []Rule {
	out := make([]Rule, len(table))
	copy(out, table)
	return out
}

// Lookup returns the rule with the given identifier.
func Lookup(id RuleID) (Rule, bool) {
	switch id {
	case RuleI:
		return table[0], true
	case RuleB:
		return table[1], true
	case RuleC:
		return table[2], true
	case RuleK:
		return table[3], true
	case RuleW:
		return table[4], true
	case RuleS:
		return table[5], true
	case RuleAbs:
		return table[6], true
	case RuleSc:
		return table[7], true
	}
	return Rule{}, false
}

func ruleForHead(name string) (Rule, bool) {
	if len(name) != 1 {
		return Rule{}, false
	}
	switch name[0] {
	case 'I':
		return table[0], true
	case 'B':
		return table[1], true
	case 'C':
		return table[2], true
	case 'K':
		return table[3], true
	case 'W':
		return table[4], true
	case 'S':
		return table[5], true
	}
	return Rule{}, false
}

// Errors reported when a step cannot be applied. They are sentinels for use
// with errors.Is.
var (
	ErrUnknownRule     = errors.New("reduce: rule is not in the rule table")
	ErrRuleMismatch    = errors.New("reduce: the subterm is not an instance of the cited rule")
	ErrNotAdmissible   = errors.New("reduce: the rule is not permitted at this chain")
	ErrAbsorptionBlock = errors.New("reduce: absorption is not permitted for this argument")
	ErrUnknownLabel    = errors.New("reduce: term uses a label with no permission entry")
)

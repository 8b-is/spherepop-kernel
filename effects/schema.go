// Package effects is the host boundary for SpherePOP programs. A program
// describes effects as inert data; this package reads the description from a
// designated output position of a normal form, validates it against a schema
// the host controls, asks an authorizer, and only then asks the world to
// commit. Every outcome, including every refusal, is appended to a
// hash-chained log.
//
// Nothing here changes how terms reduce. The constructors `effects`,
// `effect` and the host's own vocabulary are ordinary atoms to the reducer,
// so an effect can be built, copied, discarded or left unevaluated without
// anything happening. Three relations stay separate:
//
//	reduction   t ->* n          package reduce
//	extraction  n |-> E          this package: Extract and Decode
//	transition  (W, E) |-> (W', D)  this package: Host.Handle
//
// The package imports term and reduce.Result only.
package effects

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
)

// ArgType is the type of a request argument. Both types are carried by atoms;
// the type says how the host reads the atom.
type ArgType uint8

// The argument types.
const (
	Sym ArgType = iota + 1 // an identifier atom, read as itself
	Int                    // a canonical decimal numeral, read as an unsigned integer
)

func (t ArgType) String() string {
	switch t {
	case Sym:
		return "sym"
	case Int:
		return "int"
	}
	return fmt.Sprintf("argtype(%d)", uint8(t))
}

// VerbSpec lists the argument types a verb takes after the target.
type VerbSpec struct {
	Args []ArgType
}

// TargetSpec describes one kind of target: the type of its identifier and the
// verbs it accepts.
type TargetSpec struct {
	ID    ArgType
	Verbs map[string]VerbSpec
}

// Schema is the host's vocabulary. A request that is not an instance of the
// schema is malformed, however well-formed it is as a term.
type Schema struct {
	Targets map[string]TargetSpec
}

// Reserved atoms of the output convention.
const (
	AtomEffects = "effects" // head of the designated output term
	AtomEffect  = "effect"  // head of one request
)

// ErrSchema reports an unusable schema.
var ErrSchema = errors.New("effects: invalid schema")

func validName(s string) bool {
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '\'') {
			return false
		}
	}
	return true
}

// Validate checks that the schema names are usable atoms, avoid the reserved
// words, and that every type is known.
func (s Schema) Validate() error {
	for _, kind := range s.sortedKinds() {
		ts := s.Targets[kind]
		if !validName(kind) || kind == AtomEffect || kind == AtomEffects {
			return fmt.Errorf("%w: target kind %q", ErrSchema, kind)
		}
		if ts.ID != Sym && ts.ID != Int {
			return fmt.Errorf("%w: target %q has unknown id type", ErrSchema, kind)
		}
		if len(ts.Verbs) == 0 {
			return fmt.Errorf("%w: target %q has no verbs", ErrSchema, kind)
		}
		for verb, vs := range ts.Verbs {
			if !validName(verb) || verb == AtomEffect || verb == AtomEffects {
				return fmt.Errorf("%w: verb %q of %q", ErrSchema, verb, kind)
			}
			for _, a := range vs.Args {
				if a != Sym && a != Int {
					return fmt.Errorf("%w: verb %q of %q has unknown argument type", ErrSchema, verb, kind)
				}
			}
		}
	}
	return nil
}

func (s Schema) sortedKinds() []string {
	out := make([]string, 0, len(s.Targets))
	for k := range s.Targets {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// readAtom checks an atom's name against a type. It is deliberately strict:
// an integer has exactly one spelling, so two spellings of the same number
// cannot be two different requests.
func readAtom(name string, t ArgType) error {
	switch t {
	case Sym:
		if !validName(name) {
			return fmt.Errorf("%q is not an identifier", name)
		}
	case Int:
		if name == "" || (len(name) > 1 && name[0] == '0') {
			return fmt.Errorf("%q is not a canonical numeral", name)
		}
		if _, err := strconv.ParseUint(name, 10, 32); err != nil {
			return fmt.Errorf("%q is not an unsigned 32-bit numeral", name)
		}
	}
	return nil
}

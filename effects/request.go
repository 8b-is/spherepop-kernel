package effects

import (
	"errors"
	"fmt"

	"github.com/standardgalactic/spherepop-kernel/term"
)

// Request is a decoded, schema-valid proposal. It is a description: holding
// one grants no authority to carry it out.
type Request struct {
	Kind string   // target kind, such as door
	ID   string   // target identifier, as spelled
	Verb string   // such as open
	Args []string // remaining arguments, as spelled
}

// Term rebuilds the request as the term it was decoded from.
func (r Request) Term() *term.Term {
	target := term.App(term.Atom(r.Kind), term.Atom(r.ID))
	args := make([]*term.Term, 0, len(r.Args)+2)
	args = append(args, target, term.Atom(r.Verb))
	for _, a := range r.Args {
		args = append(args, term.Atom(a))
	}
	return term.Apply(term.Atom(AtomEffect), args...)
}

// String is the canonical text of the request, which is also its log form.
func (r Request) String() string { return r.Term().String() }

// ErrMalformed wraps every decoding failure.
var ErrMalformed = errors.New("effects: malformed request")

func malformed(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrMalformed, fmt.Sprintf(format, args...))
}

func atomName(t *term.Term) (string, bool) {
	if t.Kind() == term.KindAtom {
		return t.Name(), true
	}
	return "", false
}

// Decode reads one request, of the form (effect (kind id) verb arg...),
// against the schema. It never panics and never reduces.
func (s Schema) Decode(t *term.Term) (Request, error) {
	head, args := t.Spine()
	if name, ok := atomName(head); !ok || name != AtomEffect {
		return Request{}, malformed("head is not the atom %q", AtomEffect)
	}
	if len(args) < 2 {
		return Request{}, malformed("need a target and a verb, got %d arguments", len(args))
	}
	th, targs := args[0].Spine()
	kind, ok := atomName(th)
	if !ok || len(targs) != 1 {
		return Request{}, malformed("target must be (kind id)")
	}
	id, ok := atomName(targs[0])
	if !ok {
		return Request{}, malformed("target id must be an atom")
	}
	ts, known := s.Targets[kind]
	if !known {
		return Request{}, malformed("unknown target kind %q", kind)
	}
	if err := readAtom(id, ts.ID); err != nil {
		return Request{}, malformed("target %q: %v", kind, err)
	}
	verb, ok := atomName(args[1])
	if !ok {
		return Request{}, malformed("verb must be an atom")
	}
	vs, known := ts.Verbs[verb]
	if !known {
		return Request{}, malformed("target %q has no verb %q", kind, verb)
	}
	rest := args[2:]
	if len(rest) != len(vs.Args) {
		return Request{}, malformed("verb %q takes %d arguments, got %d", verb, len(vs.Args), len(rest))
	}
	req := Request{Kind: kind, ID: id, Verb: verb}
	for i, a := range rest {
		name, ok := atomName(a)
		if !ok {
			return Request{}, malformed("argument %d of %q must be an atom", i+1, verb)
		}
		if err := readAtom(name, vs.Args[i]); err != nil {
			return Request{}, malformed("argument %d of %q: %v", i+1, verb, err)
		}
		req.Args = append(req.Args, name)
	}
	return req, nil
}

// Extract reads the designated output position of a normal form: a term whose
// head is the atom `effects`, with one request per argument, in order. Found
// is false for any other term, which is not an error. A program is allowed to
// compute something other than effects, and the host then simply has none to
// consider.
func Extract(n *term.Term) (requests []*term.Term, found bool) {
	head, args := n.Spine()
	if name, ok := atomName(head); !ok || name != AtomEffects {
		return nil, false
	}
	return args, true
}

// AtomAtomic heads a group of requests that the host carries out together or
// not at all.
const AtomAtomic = "atomic"

// GroupTerm builds (atomic r1 ... rn) from decoded requests.
func GroupTerm(rs []Request) *term.Term {
	args := make([]*term.Term, len(rs))
	for i, r := range rs {
		args[i] = r.Term()
	}
	return term.Apply(term.Atom(AtomAtomic), args...)
}

// DecodeOutput reads one argument of the effects term: either a single
// request or a group (atomic r1 ... rn) with at least one member, every
// member a request. Groups do not nest. The second result reports a group.
func (s Schema) DecodeOutput(t *term.Term) (reqs []Request, group bool, err error) {
	head, args := t.Spine()
	if name, ok := atomName(head); ok && name == AtomAtomic {
		if len(args) == 0 {
			return nil, true, malformed("an atomic group needs at least one request")
		}
		for i, a := range args {
			r, err := s.Decode(a)
			if err != nil {
				return nil, true, malformed("member %d of an atomic group: %v", i, err)
			}
			reqs = append(reqs, r)
		}
		return reqs, true, nil
	}
	r, err := s.Decode(t)
	if err != nil {
		return nil, false, err
	}
	return []Request{r}, false, nil
}

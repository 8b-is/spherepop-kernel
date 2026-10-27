package effects

import (
	"fmt"

	"github.com/standardgalactic/spherepop-kernel/reduce"
	"github.com/standardgalactic/spherepop-kernel/term"
)

// Decision is an authorizer's answer.
type Decision struct {
	Allow  bool
	Reason string
}

// Authorizer decides whether a subject may have a request carried out. It is
// asked after decoding and before the world is touched. Nothing in a program
// can influence it except through the decoded request and the subject name.
type Authorizer interface {
	Authorize(subject string, r Request) Decision
}

// AtomicWorld is a World that can carry out several requests as one step:
// either every request takes effect or the world is left unchanged. A host
// treats a group as failed if its world does not offer this. CommitAll must
// be rollback-safe: it may only change state it can restore, and it must not
// call out to anything or emit an event that cannot be taken back. A world
// whose commits have such effects should stage them and release them after
// CommitAll succeeds.
type AtomicWorld interface {
	World
	CommitAll(rs []Request) error
}

// World carries out authorized requests. Commit may still refuse, for
// instance because a door does not exist; that is recorded as Failed.
type World interface {
	Commit(r Request) error
}

// Policy is a fail-closed Authorizer: only explicitly granted combinations of
// subject, target kind and verb are allowed.
type Policy struct {
	grants map[[3]string]bool
}

// Grant allows subject to apply verb to targets of the given kind.
func (p *Policy) Grant(subject, kind, verb string) *Policy {
	if p.grants == nil {
		p.grants = map[[3]string]bool{}
	}
	p.grants[[3]string{subject, kind, verb}] = true
	return p
}

// Authorize implements Authorizer.
func (p *Policy) Authorize(subject string, r Request) Decision {
	if p != nil && p.grants[[3]string{subject, r.Kind, r.Verb}] {
		return Decision{Allow: true}
	}
	return Decision{Reason: fmt.Sprintf("no grant for %s to %s %s", subject, r.Verb, r.Kind)}
}

// DefaultMaxRequests bounds how many requests of one output the host reads.
const DefaultMaxRequests = 64

// Host ties the pieces together. It holds no reduction machinery.
type Host struct {
	Schema      Schema
	Auth        Authorizer
	World       World
	Log         *Log
	MaxRequests int // 0 means DefaultMaxRequests
}

// Handle processes the outcome of one reduction for one subject. Outputs are
// independent: each is decoded, authorized and committed on its own, in the
// order the program listed them, and a failure of one does not affect another.
// An output is either a request or a group (atomic r1 ... rn), which is one
// unit: it is logged as one entry, every member must decode and be authorized,
// and the world applies all members or none.
// An exhausted reduction is never read, because a partial term is not an
// output. ref is an opaque pointer to the evidence, such as a trace head.
func (h *Host) Handle(tick int, subject, ref string, res reduce.Result) ([]Entry, error) {
	return h.HandleOutcome(tick, subject, ref, res.Term, res.Exhausted)
}

// HandleOutcome is Handle for a caller that has a term and a status but no
// reduce.Result, such as a scheduler reporting an entity's state.
func (h *Host) HandleOutcome(tick int, subject, ref string, t *term.Term, exhausted bool) ([]Entry, error) {
	var out []Entry
	add := func(e Entry) error {
		e.Tick, e.Subject, e.Ref = tick, subject, ref
		stored, err := h.Log.Append(e)
		if err != nil {
			return err
		}
		out = append(out, stored)
		return nil
	}
	if exhausted {
		return out, add(Entry{Index: -1, Disposition: Exhausted, Reason: "fuel exhausted; no output read"})
	}
	reqs, found := Extract(t)
	if !found {
		return out, add(Entry{Index: -1, Disposition: NoOutput, Reason: "normal form is not an effects term"})
	}
	limit := h.MaxRequests
	if limit <= 0 {
		limit = DefaultMaxRequests
	}
	for i, rt := range reqs {
		if i >= limit {
			return out, add(Entry{Index: -1, Disposition: Truncated, Reason: fmt.Sprintf("%d requests ignored beyond the limit of %d", len(reqs)-limit, limit)})
		}
		reqs, group, err := h.Schema.DecodeOutput(rt)
		if err != nil {
			if err := add(Entry{Index: i, Request: rt.String(), Disposition: Malformed, Reason: err.Error()}); err != nil {
				return out, err
			}
			continue
		}
		text := reqs[0].String()
		if group {
			text = GroupTerm(reqs).String()
		}
		denied := ""
		for j, req := range reqs {
			if d := h.Auth.Authorize(subject, req); !d.Allow {
				denied = d.Reason
				if group {
					denied = fmt.Sprintf("member %d: %s", j, d.Reason)
				}
				break
			}
		}
		if denied != "" {
			if err := add(Entry{Index: i, Request: text, Disposition: Denied, Reason: denied}); err != nil {
				return out, err
			}
			continue
		}
		var cerr error
		switch {
		case !group:
			cerr = h.World.Commit(reqs[0])
		default:
			if aw, ok := h.World.(AtomicWorld); ok {
				cerr = aw.CommitAll(reqs)
			} else {
				cerr = fmt.Errorf("the world cannot commit a group atomically")
			}
		}
		if cerr != nil {
			if err := add(Entry{Index: i, Request: text, Disposition: Failed, Reason: cerr.Error()}); err != nil {
				return out, err
			}
			continue
		}
		if err := add(Entry{Index: i, Request: text, Disposition: Committed}); err != nil {
			return out, err
		}
	}
	return out, nil
}

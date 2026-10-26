// Package sched runs several SpherePOP terms under a deterministic,
// fuel-bounded schedule. It is a host: it chooses whom to run and for how
// long, and it hands every reduction to package reduce unchanged. It has no
// access to the rule table and no way to alter a configuration after a world
// is built, so scheduling cannot change what a step means.
//
// The package is deliberately narrow. Entities do not communicate, because
// the language has no effects yet; the only things that can make two
// schedules differ are therefore the schedule itself and the reduction
// strategy. When events are added they will be a separate layer, so that any
// future difference between schedules can be attributed to them.
package sched

import (
	"errors"
	"fmt"
	"sort"

	"github.com/standardgalactic/spherepop-kernel/reduce"
	"github.com/standardgalactic/spherepop-kernel/term"
	"github.com/standardgalactic/spherepop-kernel/trace"
)

// Format identifies the log layout.
const Format = "spherepop-sched/1"

// HostConfig is the configuration a game host should start from. It differs
// from reduce.DefaultConfig in one respect: Sc is in its safe mode. That mode
// is an experimental extension whose confluence is not established
// (SPEC.md section 10), and the unrestricted rule stays available by setting
// Sc to reduce.ScAny, which is the research setting.
func HostConfig() reduce.Config {
	c := reduce.DefaultConfig()
	c.Sc = reduce.ScSafe
	return c
}

// Policy is the whole of the scheduler's authority: one strategy and one
// fixed budget per entity per tick.
type Policy struct {
	Strategy reduce.Strategy
	Budget   uint64
}

// Init names an entity and its starting term.
type Init struct {
	ID   string
	Term *term.Term
}

// Errors reported while building or checking a world.
var (
	ErrNoBudget    = errors.New("sched: budget must be positive")
	ErrBadEntity   = errors.New("sched: invalid entity")
	ErrUnknownID   = errors.New("sched: no such entity")
	ErrPending     = errors.New("sched: entity already has a pending replacement")
	ErrLogRejected = errors.New("sched: log rejected")
)

type entity struct {
	id      string
	term    *term.Term
	normal  bool
	pending *term.Term // replacement staged by Rearm, applied at the start of the next tick
}

// World is a set of entities and the log of everything done to them.
type World struct {
	cfg  reduce.Config
	pol  Policy
	ents []*entity // sorted by id
	tick int
	log  Log
}

// New builds a world. Entities are run in byte order of their identifiers,
// whatever order they are supplied in.
func New(cfg reduce.Config, pol Policy, inits []Init) (*World, error) {
	if pol.Budget == 0 {
		return nil, ErrNoBudget
	}
	if _, ok := reduce.ParseStrategy(pol.Strategy.String()); !ok {
		return nil, fmt.Errorf("sched: unknown strategy %d", uint8(pol.Strategy))
	}
	cfg.Trace = true
	w := &World{cfg: cfg, pol: pol}
	seen := map[string]bool{}
	for _, in := range inits {
		if in.ID == "" || in.Term == nil || seen[in.ID] {
			return nil, fmt.Errorf("%w: %q", ErrBadEntity, in.ID)
		}
		seen[in.ID] = true
		if err := cfg.Validate(in.Term); err != nil {
			return nil, fmt.Errorf("%w: %q: %v", ErrBadEntity, in.ID, err)
		}
		w.ents = append(w.ents, &entity{id: in.ID, term: in.Term})
	}
	sort.Slice(w.ents, func(i, j int) bool { return w.ents[i].id < w.ents[j].id })
	w.log = Log{
		Format: Format,
		Config: cfg.Spec(),
		Policy: PolicySpec{Strategy: pol.Strategy.String(), Budget: pol.Budget},
	}
	for _, e := range w.ents {
		w.log.Initial = append(w.log.Initial, EntityState{ID: e.id, Term: e.term.String(), Digest: e.term.Digest().String()})
	}
	return w, nil
}

// Rearm stages a replacement term for an entity, applied at the start of the
// next tick and recorded in that tick's event. It is how a host starts a new
// round of a persistent program: the scheduler never decides what the new term
// is, it only carries it and records it. Replacing a term that has not
// finished (preemption) is allowed and is marked as such in the log; whether
// to do so is the host's policy.
func (w *World) Rearm(id string, t *term.Term) error {
	i := sort.Search(len(w.ents), func(i int) bool { return w.ents[i].id >= id })
	if i == len(w.ents) || w.ents[i].id != id {
		return fmt.Errorf("%w: %q", ErrUnknownID, id)
	}
	if t == nil {
		return fmt.Errorf("%w: %q", ErrBadEntity, id)
	}
	if err := w.cfg.Validate(t); err != nil {
		return fmt.Errorf("%w: %q: %v", ErrBadEntity, id, err)
	}
	if w.ents[i].pending != nil {
		return fmt.Errorf("%w: %q", ErrPending, id)
	}
	w.ents[i].pending = t
	return nil
}

// EventCount returns the number of events logged so far.
func (w *World) EventCount() int { return len(w.log.Events) }

// EventsFrom returns a copy of the events from index i on.
func (w *World) EventsFrom(i int) []Event {
	return append([]Event(nil), w.log.Events[i:]...)
}

// Tick gives every entity one turn, in identifier order. An entity already in
// normal form is skipped and spends nothing. Every other entity reduces for at
// most the policy's budget from wherever it stopped, and whatever it has not
// finished is carried to the next tick as its term.
func (w *World) Tick() error {
	for _, e := range w.ents {
		var rearm *Rearm
		if e.pending != nil {
			rearm = &Rearm{Term: e.pending.String(), Digest: e.pending.Digest().String(), Preempted: !e.normal}
			e.term, e.normal, e.pending = e.pending, false, nil
		}
		if e.normal {
			w.log.Events = append(w.log.Events, Event{Tick: w.tick, Entity: e.id, Decision: Skip})
			continue
		}
		res, doc, err := trace.Record(w.cfg, e.term, w.pol.Strategy, w.pol.Budget)
		if err != nil {
			return fmt.Errorf("sched: entity %q: %w", e.id, err)
		}
		w.log.Events = append(w.log.Events, Event{Tick: w.tick, Entity: e.id, Decision: Run, Budget: w.pol.Budget, Slice: &doc, Rearm: rearm})
		e.term, e.normal = res.Term, !res.Exhausted
	}
	w.tick++
	return nil
}

// Run performs n ticks.
func (w *World) Run(n int) error {
	for i := 0; i < n; i++ {
		if err := w.Tick(); err != nil {
			return err
		}
	}
	return nil
}

// Done reports whether every entity is in normal form.
func (w *World) Done() bool {
	for _, e := range w.ents {
		if !e.normal {
			return false
		}
	}
	return true
}

// States returns the current state of every entity in identifier order.
func (w *World) States() []EntityState {
	out := make([]EntityState, len(w.ents))
	for i, e := range w.ents {
		out[i] = EntityState{ID: e.id, Term: e.term.String(), Digest: e.term.Digest().String(), Normal: e.normal}
	}
	return out
}

// Log returns the log so far, with the current states as its final section.
func (w *World) Log() Log {
	l := w.log
	l.Events = append([]Event(nil), w.log.Events...)
	l.Final = w.States()
	return l
}

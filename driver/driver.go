// Package driver runs persistent programs frame by frame. It is the loop that
// a game host would own, written against interfaces so that it needs no game.
//
// A program is a term P that takes the host's observations as arguments. A
// round is the evaluation of P o1 ... on, where the oi are encoded facts about
// the world (package encode). When a round reaches a normal form the host reads
// its proposed effects (package effects), authorizes and commits them, observes
// the new world, and starts the next round. The evaluator, scheduler and effect
// host are used unchanged.
//
// One frame is, in order: the scheduler gives every unfinished entity its
// budget; the effects of every round that finished are handled, in identifier
// order; then, from the world as it stands after all of those commits, every
// entity that finished or was abandoned is observed and re-armed, again in
// identifier order. Observations are therefore a simultaneous snapshot: no
// entity sees another's commit from the same frame before it has acted.
package driver

import (
	"fmt"
	"sort"

	"github.com/standardgalactic/spherepop-kernel/effects"
	"github.com/standardgalactic/spherepop-kernel/reduce"
	"github.com/standardgalactic/spherepop-kernel/sched"
	"github.com/standardgalactic/spherepop-kernel/term"
)

// Observer reports the world to a program as arguments, in the fixed order the
// program expects. It must be deterministic and must not change the world.
// The frame is the number of the frame the observation will be used in.
type Observer interface {
	Observe(subject string, frame int) ([]*term.Term, error)
}

// Program is a persistent behaviour.
type Program struct {
	ID   string
	Term *term.Term
}

// Params configure a driver.
type Params struct {
	Reduce   reduce.Config
	Policy   sched.Policy
	Host     *effects.Host
	Observer Observer
	// MaxRoundFrames abandons a round that has not finished after this many
	// frames, records it as exhausted, and re-arms the entity. Zero means
	// rounds are never abandoned, and a non-terminating program then simply
	// never acts again.
	MaxRoundFrames int
	// Advance, if set, is the world's own step (physics, timers). It runs once
	// per frame after every effect of that frame has been handled and before
	// any observation, so observations see the world after it has moved.
	Advance func() error
}

// Driver advances a world one frame at a time.
type Driver struct {
	p        Params
	w        *sched.World
	prog     map[string]*term.Term
	frame    int
	inRound  map[string]int // frames the current round has been running
	rounds   map[string]int // completed rounds
	observed []string       // observation order, for tests and audits
}

// New builds a driver and takes the first observation of every entity, in
// identifier order.
func New(p Params, programs []Program) (*Driver, error) {
	d := &Driver{p: p, prog: map[string]*term.Term{}, inRound: map[string]int{}, rounds: map[string]int{}}
	ids := make([]string, 0, len(programs))
	for _, pr := range programs {
		if pr.ID == "" || pr.Term == nil || d.prog[pr.ID] != nil {
			return nil, fmt.Errorf("driver: invalid or repeated program %q", pr.ID)
		}
		d.prog[pr.ID] = pr.Term
		ids = append(ids, pr.ID)
	}
	sort.Strings(ids)
	var inits []sched.Init
	for _, id := range ids {
		t, err := d.round(id, 0)
		if err != nil {
			return nil, err
		}
		inits = append(inits, sched.Init{ID: id, Term: t})
	}
	w, err := sched.New(p.Reduce, p.Policy, inits)
	if err != nil {
		return nil, err
	}
	d.w = w
	return d, nil
}

// round builds the term for a new round: the program applied to the current
// observation.
func (d *Driver) round(id string, frame int) (*term.Term, error) {
	args, err := d.p.Observer.Observe(id, frame)
	if err != nil {
		return nil, fmt.Errorf("driver: observing %q: %w", id, err)
	}
	d.observed = append(d.observed, id)
	return term.Apply(d.prog[id], args...), nil
}

// Frame returns the number of frames completed.
func (d *Driver) Frame() int { return d.frame }

// Rounds returns how many rounds an entity has completed.
func (d *Driver) Rounds(id string) int { return d.rounds[id] }

// Scheduler returns the underlying world, for its log.
func (d *Driver) Scheduler() *sched.World { return d.w }

// Observed returns the order in which entities were observed.
func (d *Driver) Observed() []string { return append([]string(nil), d.observed...) }

// Step runs one frame. It returns an error only for a failure of the host's own
// parts (the observer, the log sink, the scheduler); program misbehaviour is
// recorded as a disposition and is not an error. A failure part-way through a
// frame leaves the frame incomplete, so a host should treat it as fatal.
func (d *Driver) Step() error {
	before := d.w.EventCount()
	if err := d.w.Tick(); err != nil {
		return err
	}
	refs := map[string]string{}
	for _, ev := range d.w.EventsFrom(before) {
		if ev.Slice != nil {
			refs[ev.Entity] = ev.Slice.Head
		}
	}

	var rearm []string
	// Handle every finished round, in identifier order.
	for _, st := range d.w.States() {
		if st.Normal {
			nf, err := term.Parse(st.Term)
			if err != nil {
				return fmt.Errorf("driver: result of %q: %w", st.ID, err)
			}
			if _, err := d.p.Host.HandleOutcome(d.frame, st.ID, refs[st.ID], nf, false); err != nil {
				return err
			}
			d.rounds[st.ID]++
			d.inRound[st.ID] = 0
			rearm = append(rearm, st.ID)
			continue
		}
		d.inRound[st.ID]++
		if d.p.MaxRoundFrames > 0 && d.inRound[st.ID] >= d.p.MaxRoundFrames {
			partial, err := term.Parse(st.Term)
			if err != nil {
				return fmt.Errorf("driver: partial result of %q: %w", st.ID, err)
			}
			if _, err := d.p.Host.HandleOutcome(d.frame, st.ID, refs[st.ID], partial, true); err != nil {
				return err
			}
			d.inRound[st.ID] = 0
			rearm = append(rearm, st.ID)
		}
	}

	if d.p.Advance != nil {
		if err := d.p.Advance(); err != nil {
			return fmt.Errorf("driver: advance: %w", err)
		}
	}

	// Observe everyone from the world as it now stands, then re-arm. All
	// observations are taken before any re-arm so that none can depend on one.
	next := make([]*term.Term, len(rearm))
	for i, id := range rearm {
		t, err := d.round(id, d.frame+1)
		if err != nil {
			return err
		}
		next[i] = t
	}
	for i, id := range rearm {
		if err := d.w.Rearm(id, next[i]); err != nil {
			return err
		}
	}
	d.frame++
	return nil
}

// Run performs n frames.
func (d *Driver) Run(n int) error {
	for i := 0; i < n; i++ {
		if err := d.Step(); err != nil {
			return err
		}
	}
	return nil
}

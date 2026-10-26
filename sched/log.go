package sched

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"

	"github.com/standardgalactic/spherepop-kernel/reduce"
	"github.com/standardgalactic/spherepop-kernel/term"
	"github.com/standardgalactic/spherepop-kernel/trace"
)

// Decision is a scheduler decision, recorded apart from the reduction steps
// it caused. Reduction steps live only inside an event's slice document.
type Decision string

// The two decisions a scheduler can make about an entity in a tick.
const (
	Run  Decision = "run"  // grant the policy's budget
	Skip Decision = "skip" // the entity is in normal form; grant nothing
)

// PolicySpec is the serialised Policy.
type PolicySpec struct {
	Strategy string `json:"strategy"`
	Budget   uint64 `json:"budget"`
}

// EntityState is an entity's term at a moment. Normal is meaningful only in
// the final section of a log.
type EntityState struct {
	ID     string `json:"id"`
	Term   string `json:"term"`
	Digest string `json:"digest"`
	Normal bool   `json:"normal,omitempty"`
}

// Rearm records that the host replaced an entity's term at the start of a
// tick. Preempted is true when the entity had not finished. The scheduler
// vouches for nothing about the new term except that it is the one the next
// slice starts from.
type Rearm struct {
	Term      string `json:"term"`
	Digest    string `json:"digest"`
	Preempted bool   `json:"preempted,omitempty"`
}

// Event is one scheduler decision. For a Run event, Slice is a complete trace
// document of the reduction that followed, which the trace checker verifies.
type Event struct {
	Tick     int             `json:"tick"`
	Entity   string          `json:"entity"`
	Decision Decision        `json:"decision"`
	Budget   uint64          `json:"budget,omitempty"`
	Slice    *trace.Document `json:"slice,omitempty"`
	Rearm    *Rearm          `json:"rearm,omitempty"`
}

// Log is the evidence for a whole run.
type Log struct {
	Format  string            `json:"format"`
	Config  reduce.ConfigSpec `json:"config"`
	Policy  PolicySpec        `json:"policy"`
	Initial []EntityState     `json:"initial"`
	Events  []Event           `json:"events"`
	Final   []EntityState     `json:"final"`
}

// Marshal encodes a log as indented JSON.
func Marshal(l Log) ([]byte, error) {
	b, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// Unmarshal decodes a log and rejects unknown fields.
func Unmarshal(b []byte) (Log, error) {
	var l Log
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&l); err != nil {
		return Log{}, fmt.Errorf("%w: %v", ErrLogRejected, err)
	}
	if dec.More() {
		return Log{}, fmt.Errorf("%w: trailing data", ErrLogRejected)
	}
	return l, nil
}

func reject(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrLogRejected, fmt.Sprintf(format, args...))
}

// CheckLog verifies a log without running the world. It establishes three
// separate things. The scheduler decisions follow the policy: ticks are
// complete, entities appear in identifier order, finished entities are
// skipped and unfinished ones receive exactly the budget. Every slice is a
// valid reduction under the log's own configuration and strategy, checked by
// the trace checker with the strategy enforced and the budget fixed. And the
// slices chain: each starts where the entity's previous slice ended, and the
// final states are what the chain produces.
func CheckLog(l Log) error {
	if l.Format != Format {
		return reject("format %q, want %q", l.Format, Format)
	}
	cfg, err := l.Config.Config()
	if err != nil {
		return reject("config: %v", err)
	}
	strat, ok := reduce.ParseStrategy(l.Policy.Strategy)
	if !ok || l.Policy.Budget == 0 {
		return reject("policy %+v", l.Policy)
	}

	type state struct {
		digest string
		normal bool
	}
	states := map[string]*state{}
	var ids []string
	for i, s := range l.Initial {
		if s.ID == "" || states[s.ID] != nil {
			return reject("initial entity %d has an empty or repeated id", i)
		}
		states[s.ID] = &state{digest: s.Digest}
		ids = append(ids, s.ID)
	}
	if !sort.StringsAreSorted(ids) {
		return reject("initial entities are not in identifier order")
	}

	n := len(ids)
	if n == 0 {
		if len(l.Events) != 0 {
			return reject("events without entities")
		}
		return nil
	}
	if len(l.Events)%n != 0 {
		return reject("%d events do not make whole ticks of %d entities", len(l.Events), n)
	}
	for i, ev := range l.Events {
		tick, id := i/n, ids[i%n]
		if ev.Tick != tick || ev.Entity != id {
			return reject("event %d is (tick %d, %q), want (tick %d, %q)", i, ev.Tick, ev.Entity, tick, id)
		}
		st := states[id]
		if ev.Rearm != nil {
			t, err := term.Parse(ev.Rearm.Term)
			if err != nil || t.Digest().String() != ev.Rearm.Digest {
				return reject("event %d: replacement term for %q does not match its digest", i, id)
			}
			if err := cfg.Validate(t); err != nil {
				return reject("event %d: replacement term for %q: %v", i, id, err)
			}
			if ev.Rearm.Preempted == st.normal {
				return reject("event %d: replacement for %q claims preempted=%v but the entity's state says otherwise", i, id, ev.Rearm.Preempted)
			}
			st.digest, st.normal = ev.Rearm.Digest, false
		}
		if st.normal {
			if ev.Decision != Skip || ev.Slice != nil || ev.Budget != 0 {
				return reject("event %d: %q is finished and must be skipped", i, id)
			}
			continue
		}
		if ev.Decision != Run || ev.Budget != l.Policy.Budget || ev.Slice == nil {
			return reject("event %d: %q must run with budget %d", i, id, l.Policy.Budget)
		}
		d := *ev.Slice
		if d.StartDigest != st.digest {
			return reject("event %d: slice for %q starts at %.12s, entity is at %.12s", i, id, d.StartDigest, st.digest)
		}
		if d.Strategy != strat.String() || !reflect.DeepEqual(d.Config, l.Config) {
			return reject("event %d: slice for %q ran under a different strategy or configuration", i, id)
		}
		if _, err := trace.Check(d, trace.Options{RequireStrategy: true, Fuel: l.Policy.Budget}); err != nil {
			return fmt.Errorf("%w: event %d (%q): %w", ErrLogRejected, i, id, err)
		}
		st.digest, st.normal = d.FinalDigest, !d.Exhausted
	}

	if len(l.Final) != n {
		return reject("%d final states for %d entities", len(l.Final), n)
	}
	for i, f := range l.Final {
		st := states[ids[i]]
		if f.ID != ids[i] || f.Digest != st.digest || f.Normal != st.normal {
			return reject("final state of %q does not match the chain of slices", ids[i])
		}
		if t, err := term.Parse(f.Term); err != nil || t.Digest().String() != f.Digest {
			return reject("final term text of %q does not match its digest", ids[i])
		}
	}
	return nil
}

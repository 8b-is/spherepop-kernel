package trace

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"github.com/standardgalactic/spherepop-kernel/reduce"
	"github.com/standardgalactic/spherepop-kernel/term"
)

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

// Sentinel errors for the ways a trace can fail. A Violation wraps exactly one.
var (
	ErrFormat            = errors.New("trace: malformed document")
	ErrStartMismatch     = errors.New("trace: start term does not match its digest")
	ErrChainBroken       = errors.New("trace: hash chain does not verify")
	ErrStepRejected      = errors.New("trace: step is not authorised by the rule table")
	ErrDigestMismatch    = errors.New("trace: step result does not match its claimed digest")
	ErrFinalMismatch     = errors.New("trace: final term does not match the replay")
	ErrStatusMismatch    = errors.New("trace: claimed termination status is wrong")
	ErrStrategyDeviation = errors.New("trace: step is not the one the claimed strategy takes")
)

// Violation locates a failure. Step is the zero-based index of the offending
// step, or -1 when the failure concerns the document as a whole.
type Violation struct {
	Step   int
	Reason error
	Detail string
}

func (v *Violation) Error() string {
	if v.Step < 0 {
		return fmt.Sprintf("%v: %s", v.Reason, v.Detail)
	}
	return fmt.Sprintf("step %d: %v: %s", v.Step, v.Reason, v.Detail)
}

func (v *Violation) Unwrap() error { return v.Reason }

func fail(step int, reason error, format string, args ...any) error {
	return &Violation{Step: step, Reason: reason, Detail: fmt.Sprintf(format, args...)}
}

// Options tune the check.
type Options struct {
	// RequireStrategy additionally demands that each step is the one the
	// claimed strategy would take. Without it a trace is valid whenever every
	// step is authorised, whichever order the worker chose: agreement on a
	// result is a different matter from agreement on how it was found.
	RequireStrategy bool
	// MaxSteps rejects a document with more steps than this, if non-zero, so
	// a hostile trace cannot make the checker run for long.
	MaxSteps int
	// Fuel, if non-zero, is the budget the host agreed with the worker. The
	// document's stated fuel must equal it. Without this, a worker could
	// truncate a run and state whatever fuel makes the truncation look like
	// exhaustion.
	Fuel uint64
}

// Report is what a successful check establishes.
type Report struct {
	Steps  int
	Final  *term.Term
	Normal bool // no step is available from Final under the configuration
}

// Check replays d and returns a report, or a *Violation. It trusts nothing in
// d beyond the text of the start term and the configuration, both of which it
// parses afresh. The only logic it shares with the reducer is reduce.Apply,
// which defines what a step means.
func Check(d Document, opt Options) (Report, error) {
	if d.Format != Format {
		return Report{}, fail(-1, ErrFormat, "format %q, want %q", d.Format, Format)
	}
	if opt.MaxSteps > 0 && len(d.Steps) > opt.MaxSteps {
		return Report{}, fail(-1, ErrFormat, "%d steps exceeds limit %d", len(d.Steps), opt.MaxSteps)
	}
	cfg, err := d.Config.Config()
	if err != nil {
		return Report{}, fail(-1, ErrFormat, "%v", err)
	}
	cfg.Trace = false
	start, err := term.Parse(d.Start)
	if err != nil {
		return Report{}, fail(-1, ErrFormat, "start term: %v", err)
	}
	if start.Digest().String() != d.StartDigest {
		return Report{}, fail(-1, ErrStartMismatch, "parsed digest %s, claimed %s", start.Digest().Short(), d.StartDigest)
	}
	if err := cfg.Validate(start); err != nil {
		return Report{}, fail(-1, ErrFormat, "%v", err)
	}
	strat, haveStrat := reduce.ParseStrategy(d.Strategy)
	if opt.RequireStrategy && !haveStrat {
		return Report{}, fail(-1, ErrFormat, "unknown strategy %q", d.Strategy)
	}
	cd, err := configDigest(d.Config)
	if err != nil {
		return Report{}, fail(-1, ErrFormat, "%v", err)
	}

	prev := genesis(d.StartDigest, cd)
	cur := start
	for i, s := range d.Steps {
		if want := link(prev, s.Rule, s.Path, s.After); s.Link != want {
			return Report{}, fail(i, ErrChainBroken, "link %.12s, recomputed %.12s", s.Link, want)
		}
		prev = s.Link
		if opt.RequireStrategy {
			rs := cfg.Redexes(cur, strat)
			if len(rs) == 0 || rs[0].Rule != s.Rule || string(rs[0].Path) != s.Path {
				return Report{}, fail(i, ErrStrategyDeviation, "strategy %s does not take %s at %q", strat, s.Rule, s.Path)
			}
		}
		next, err := cfg.Apply(cur, term.Path(s.Path), s.Rule)
		if err != nil {
			return Report{}, fail(i, errors.Join(ErrStepRejected, err), "%s at %q", s.Rule, s.Path)
		}
		if next.Digest().String() != s.After {
			return Report{}, fail(i, ErrDigestMismatch, "replay gives %s, claimed %.12s", next.Digest().Short(), s.After)
		}
		cur = next
	}
	if d.Head != prev {
		return Report{}, fail(-1, ErrChainBroken, "head %.12s, recomputed %.12s", d.Head, prev)
	}
	if cur.Digest().String() != d.FinalDigest || cur.String() != d.Final {
		return Report{}, fail(-1, ErrFinalMismatch, "replay ends at %s, claimed %.12s", cur.Digest().Short(), d.FinalDigest)
	}
	more := len(cfg.Redexes(cur, reduce.LeftmostOutermost)) > 0
	if d.Exhausted != more {
		return Report{}, fail(-1, ErrStatusMismatch, "claimed exhausted=%v but redexes available=%v", d.Exhausted, more)
	}
	if opt.Fuel != 0 && d.Fuel != opt.Fuel {
		return Report{}, fail(-1, ErrStatusMismatch, "stated fuel %d, expected %d", d.Fuel, opt.Fuel)
	}
	if d.Exhausted && uint64(len(d.Steps)) != d.Fuel {
		return Report{}, fail(-1, ErrStatusMismatch, "claimed exhaustion after %d steps with fuel %d", len(d.Steps), d.Fuel)
	}
	if uint64(len(d.Steps)) > d.Fuel {
		return Report{}, fail(-1, ErrStatusMismatch, "%d steps exceed the stated fuel %d", len(d.Steps), d.Fuel)
	}
	return Report{Steps: len(d.Steps), Final: cur, Normal: !more}, nil
}

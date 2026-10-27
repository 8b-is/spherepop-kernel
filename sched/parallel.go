package sched

import (
	"context"
	"fmt"
	"runtime"
	"sync"

	"github.com/standardgalactic/spherepop-kernel/reduce"
	"github.com/standardgalactic/spherepop-kernel/term"
	"github.com/standardgalactic/spherepop-kernel/trace"
)

type entityWork struct {
	idx   int
	id    string
	term  *term.Term
	rearm *Rearm
}

type entityOutcome struct {
	event  Event
	term   *term.Term
	normal bool
	err    error
}

// TickParallel executes one deterministic scheduler tick across a bounded pool
// of worker goroutines fed by a cache-line-padded lock-free RingBuffer.
// Because entities do not share state during a tick, reductions run in
// parallel while their resulting events and state transitions are committed in
// strict canonical identifier order—producing a bit-identical Log to Tick().
func (w *World) TickParallel(ctx context.Context, workers int) error {
	n := len(w.ents)
	if n == 0 {
		w.tick++
		return nil
	}
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}

	outcomes := make([]entityOutcome, n)
	rb := reduce.NewRingBuffer[entityWork](n)
	activeCount := 0

	for i, e := range w.ents {
		var rearm *Rearm
		if e.pending != nil {
			rearm = &Rearm{
				Term:      e.pending.String(),
				Digest:    e.pending.Digest().String(),
				Preempted: !e.normal,
			}
			e.term, e.normal, e.pending = e.pending, false, nil
		}
		if e.normal {
			outcomes[i] = entityOutcome{
				event:  Event{Tick: w.tick, Entity: e.id, Decision: Skip},
				term:   e.term,
				normal: true,
			}
			continue
		}
		rb.TryEnqueue(entityWork{
			idx:   i,
			id:    e.id,
			term:  e.term,
			rearm: rearm,
		})
		activeCount++
	}

	if activeCount > 0 {
		if workers > activeCount {
			workers = activeCount
		}
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		var wg sync.WaitGroup
		var firstErr error
		var errOnce sync.Once
		tickNum := w.tick

		for k := 0; k < workers; k++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					if err := ctx.Err(); err != nil {
						errOnce.Do(func() { firstErr = err })
						return
					}
					job, ok := rb.TryDequeue()
					if !ok {
						return
					}
					res, doc, err := trace.Record(w.cfg, job.term, w.pol.Strategy, w.pol.Budget)
					if err != nil {
						wrapped := fmt.Errorf("sched: entity %q: %w", job.id, err)
						outcomes[job.idx] = entityOutcome{err: wrapped}
						errOnce.Do(func() {
							firstErr = wrapped
							cancel()
						})
						return
					}
					outcomes[job.idx] = entityOutcome{
						event: Event{
							Tick:     tickNum,
							Entity:   job.id,
							Decision: Run,
							Budget:   w.pol.Budget,
							Slice:    &doc,
							Rearm:    job.rearm,
						},
						term:   res.Term,
						normal: !res.Exhausted,
					}
				}
			}()
		}
		wg.Wait()
		if firstErr != nil {
			return firstErr
		}
	}

	// Commit in canonical sorted entity order.
	for i, out := range outcomes {
		w.log.Events = append(w.log.Events, out.event)
		w.ents[i].term = out.term
		w.ents[i].normal = out.normal
	}
	w.tick++
	return nil
}

// RunParallel performs n ticks using TickParallel.
func (w *World) RunParallel(ctx context.Context, n, workers int) error {
	for i := 0; i < n; i++ {
		if err := w.TickParallel(ctx, workers); err != nil {
			return err
		}
	}
	return nil
}

package reduce

import (
	"context"
	"math/bits"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/standardgalactic/spherepop-kernel/term"
)

// CapGraph is a bit-parallel 16-state capability automaton over the permission
// lattice 2^{W,K,C,S} (states 0x00..0x0F). Entering a bracket <t>_l transitions
// state m -> m & perm(l). Because intersection is idempotent, commutative, and
// monotone decreasing in popcount, the entire transition relation and its
// reflexive-transitive closure fit inside a 16x16 bitmatrix ([16]uint16 = 32 bytes,
// half of a single 64-byte CPU cache line).
type CapGraph struct {
	root    PermSet
	gated   PermSet
	step    [16]uint16 // step[m] has bit m' set iff a single bracket label transitions m -> m'
	closure [16]uint16 // reflexive-transitive reachability closure over 0..15
	mono    bool       // true iff Root is contained in every label's permission (Root &^ perm(l) == 0)
}

// BuildCapGraph compiles a Config's permission table into a bit-parallel
// capability graph using hardware bit-tricks and Warshall bitmatrix closure.
func (c *Config) BuildCapGraph() CapGraph {
	var g CapGraph
	g.root = c.Root & PermAll
	g.gated = c.Gated & PermAll
	g.mono = true

	// Collect distinct label masks.
	var labelMasks uint16
	for _, p := range c.Perm {
		pm := p & PermAll
		labelMasks |= uint16(1) << (pm & 0x0F)
		if g.root&^pm != 0 {
			g.mono = false
		}
	}

	for m := uint8(0); m < 16; m++ {
		g.closure[m] = uint16(1) << m
		rem := labelMasks
		for rem != 0 {
			idx := uint8(bits.TrailingZeros16(rem))
			rem &= rem - 1 // clear lowest set bit (Kernighan bit-trick)
			next := m & idx
			g.step[m] |= uint16(1) << next
			g.closure[m] |= uint16(1) << next
		}
	}

	// Warshall transitive closure in 16-bit register parallel form.
	for k := 0; k < 16; k++ {
		kBit := uint16(1) << k
		rowK := g.closure[k]
		for i := 0; i < 16; i++ {
			if g.closure[i]&kBit != 0 {
				g.closure[i] |= rowK
			}
		}
	}
	return g
}

// ReachableMask returns the 16-bit mask of all capability states reachable
// from m via zero or more bracket entries.
func (g CapGraph) ReachableMask(m PermSet) uint16 {
	return g.closure[m&0x0F]
}

// ReachableCount returns the number of distinct capability states reachable
// from m in O(1) via hardware POPCNT.
func (g CapGraph) ReachableCount(m PermSet) int {
	return bits.OnesCount16(g.closure[m&0x0F])
}

// CanReach reports in O(1) whether capability state dst is reachable from src.
func (g CapGraph) CanReach(src, dst PermSet) bool {
	return (g.closure[src&0x0F] & (uint16(1) << (dst & 0x0F))) != 0
}

// IsMonotone reports whether every bracket label preserves the root permission
// (Root ⊆ Perm[l]), the condition under which unrestricted absorption (AbsAny)
// remains confluent.
func (g CapGraph) IsMonotone() bool { return g.mono }

// HammingAttenuation returns the number of capability bits dropped between src
// and dst (popcount(src &^ dst)).
func HammingAttenuation(src, dst PermSet) int {
	return bits.OnesCount8(uint8(src &^ dst))
}

// RingBuffer is a bounded, power-of-two masked, lock-free MPMC ring buffer
// with 64-byte cache-line padding to eliminate false sharing across CPU cores.
type RingBuffer[T any] struct {
	_      [64]byte
	head   atomic.Uint64
	_      [56]byte
	tail   atomic.Uint64
	_      [56]byte
	mask   uint64
	slots  []ringSlot[T]
}

type ringSlot[T any] struct {
	seq  atomic.Uint64
	item T
	_    [48]byte
}

// NewRingBuffer allocates a power-of-two ring buffer with capacity >= minCap.
func NewRingBuffer[T any](minCap int) *RingBuffer[T] {
	capPow2 := uint64(1)
	for capPow2 < uint64(minCap) || capPow2 < 2 {
		capPow2 <<= 1
	}
	slots := make([]ringSlot[T], capPow2)
	for i := uint64(0); i < capPow2; i++ {
		slots[i].seq.Store(i)
	}
	return &RingBuffer[T]{
		mask:  capPow2 - 1,
		slots: slots,
	}
}

// TryEnqueue inserts v into the ring buffer without blocking or allocating.
func (rb *RingBuffer[T]) TryEnqueue(v T) bool {
	for {
		pos := rb.tail.Load()
		slot := &rb.slots[pos&rb.mask]
		seq := slot.seq.Load()
		diff := int64(seq) - int64(pos)
		if diff == 0 {
			if rb.tail.CompareAndSwap(pos, pos+1) {
				slot.item = v
				slot.seq.Store(pos + 1)
				return true
			}
		} else if diff < 0 {
			return false // full
		}
	}
}

// TryDequeue removes and returns the next item from the ring buffer without
// blocking or allocating.
func (rb *RingBuffer[T]) TryDequeue() (T, bool) {
	var zero T
	for {
		pos := rb.head.Load()
		slot := &rb.slots[pos&rb.mask]
		seq := slot.seq.Load()
		diff := int64(seq) - int64(pos+1)
		if diff == 0 {
			if rb.head.CompareAndSwap(pos, pos+1) {
				v := slot.item
				slot.item = zero
				slot.seq.Store(pos + rb.mask + 1)
				return v, true
			}
		} else if diff < 0 {
			return zero, false // empty
		}
	}
}

type batchJob struct {
	index int
	term  *term.Term
}

// ReduceBatchParallel reduces a batch of independent terms concurrently across
// a bounded worker pool fed by a lock-free RingBuffer, respecting ctx
// cancellation and returning results in deterministic input order.
func (c *Config) ReduceBatchParallel(ctx context.Context, terms []*term.Term, strat Strategy, fuel uint64, workers int) ([]Result, error) {
	n := len(terms)
	if n == 0 {
		return nil, nil
	}
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	if workers > n {
		workers = n
	}

	rb := NewRingBuffer[batchJob](n)
	for i, t := range terms {
		rb.TryEnqueue(batchJob{index: i, term: t})
	}

	results := make([]Result, n)
	var firstErr error
	var errOnce sync.Once
	var wg sync.WaitGroup

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	for w := 0; w < workers; w++ {
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
				res, err := c.Reduce(job.term, strat, fuel)
				if err != nil {
					errOnce.Do(func() {
						firstErr = err
						cancel()
					})
					return
				}
				results[job.index] = res
			}
		}()
	}

	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return results, nil
}

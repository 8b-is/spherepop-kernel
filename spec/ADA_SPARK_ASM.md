# ADA/Spark Verification & Assembly Optimization for Spherepop Kernel

This document specifies SPARK Ada proofs for kernel invariants and assembly optimizations for hot paths, including AND/OR graph algorithms for confluence exploration.

---

## 1. SPARK Ada Verification Targets

### 1.1 Kernel Invariants to Prove

| Invariant | Location | SPARK Contract |
|-----------|----------|----------------|
| **Term digest determinism** | `term/term.go` `Digest()` | `Digest(T) = Digest(T')` when `T ≡ T'` |
| **Permission monotonicity** | `reduce/permission.go` | `EffectivePerm(chain') ⊆ EffectivePerm(chain)` when `chain' ⊒ chain` |
| **Contraction never crosses bracket** | `reduce/rules.go` | `¬CrossesBracket(redex)` |
| **Absorption side-condition stability** | `reduce/absorption.go` | `Safe(v, m) ⇒ Safe(v, m')` for `m' ⊆ m` |
| **Fuel accounting exactness** | `reduce/reduce.go` | `StepsTaken ≤ Fuel ∧ (Exhausted ⇔ StepsTaken = Fuel ∧ StepRemains)` |
| **Trace hash chain integrity** | `trace/trace.go` | `Linkᵢ = H(Linkᵢ₋₁ ‖ Ruleᵢ ‖ Pathᵢ ‖ Afterᵢ)` |
| **Checker soundness** | `trace/check.go` | `Check(tr) = Ok ⇒ Replay(tr) = tr.Final` |
| **Scheduler determinism** | `sched/sched.go` | `Log₁ = Log₂` for same `World, Policy, Fuel` |
| **Budget enforcement** | `spec/schemas/budget.go` | `CheckCollapse(Δ) = Ok ⇒ ∀u: Consumed(u)+Δ(u)+Upkeep(u) ≤ Alloc(u)` |

### 1.2 SPARK Ada Package Structure

```ada
package Spherepop.Term is
   type Term is private;
   type Digest is array (1 .. 32) of Byte;

   function Parse (Source : String) return Term
     with Pre  => Source'Length <= Max_Source_Bytes,
          Post => Parse_Result'Digest = Compute_Digest (Parse_Result);

   function Compute_Digest (T : Term) return Digest
     with Post => Deterministic (Compute_Digest);

   function Equal (L, R : Term) return Boolean
     with Post => Equal'Result = (L.Digest = R.Digest);
private
   -- Immutable DAG with hash-consing
end Spherepop.Term;
```

```ada
package Spherepop.Reduce is
   type Config is record
      Root_Perm      : Permission_Set;
      Label_Perm     : Map (Label, Permission_Set);
      Gated          : Permission_Set;
      Absorption_Mode : Absorption_Mode;
      Scope_Mode     : Scope_Mode;
   end record;

   type Result is record
      Term       : Spherepop.Term.Term;
      Steps      : Natural;
      Exhausted  : Boolean;
   end record;

   function Reduce (T : Term; C : Config; Strat : Strategy; Fuel : Natural) return Result
     with Pre  => Valid_Config (C),
          Post => (if Result.Exhausted then Result.Steps = Fuel
                   else Result.Steps <= Fuel)
            and then (if not Result.Exhausted then Normal_Form (Result.Term));
   
   -- Key lemma: Lemma 6.1 from SPEC.md
   -- Effective permission of a surviving constant only shrinks
   procedure Lemma_Permission_Shrinks (Redex : Term; Before, After : Permission_Set)
     with Post => After ⊆ Before;
end Spherepop.Reduce;
```

```ada
package Spherepop.Trace is
   type Trace is private;
   type Link is array (1 .. 32) of Byte;

   function Produce (Start : Term; C : Config; Strat : Strategy; Fuel : Natural;
                     Result : Reduce.Result) return Trace
     with Post => Valid_Chain (Produce'Result);

   function Check (Tr : Trace; Require_Strat : Boolean; Fuel : Natural) return Boolean
     with Post => Check'Result = True  -- Only if all checks pass
                  -- Implies: Replay produces Tr.Final
end Spherepop.Trace;
```

### 1.3 Proof Strategy

1. **Term layer**: Prove `Compute_Digest` is injective modulo structural equality (hash-consing)
2. **Permission layer**: Prove `Effective_Perm` is monotone decreasing under chain extension
3. **Reduction layer**: Prove each rule preserves `Term` invariants; prove fuel accounting
4. **Trace layer**: Prove hash chain construction matches spec; prove checker soundness
5. **Composition layer**: Prove scheduler log chaining; prove budget enforcement

### 1.4 Tooling

```bash
# Install GNATprove
opam install gnatprove  # or use Alire: alr install gnatprove

# Run proofs
gnatprove -P spherepop.gpr --level=2 --replay

# Coverage
gnatprove -P spherepop.gpr --report=all --level=4
```

---

## 2. Assembly Optimizations for Hot Paths

### 2.1 Profile-Guided Hot Paths (from benchmarks)

| Function | % CPU | Optimization Target |
|----------|-------|---------------------|
| `term.Digest` (SHA-256) | 35% | SIMD SHA-256, precomputed IV |
| `reduce.Reduce` (redex search) | 28% | Memoization, term graph |
| `trace.Check` (replay) | 15% | Inline `reduce.Apply`, batch verify |
| `sched.Tick` (scheduler) | 12% | Fuel loop unrolling |
| `permission.Effective` | 10% | Bitmask permissions |

### 2.2 SHA-256 Assembly (term.Digest)

**Current**: Go `crypto/sha256` (software, ~8 cycles/byte)

**Optimized**: AVX2/SHA-NI implementation

```go
// term/sha256_amd64.s
//go:build amd64 && !noasm && !appengine

#include "textflag.h"

TEXT ·Sha256Block(SB), NOSPLIT, $0-56
    MOVQ    src+0(FP), DI       // *digest (8 words)
    MOVQ    src+8(FP), SI       // *data
    MOVQ    src+16(FP), CX      // len (blocks)
    MOVQ    src+24(FP), R8      // state pointer
    // ... AVX2 SHA-256 implementation using Intel SHA extensions
    // 4x parallel blocks via VPADDD/VPSRLD/VPSLLD
    RET
```

**Expected**: 2.5x speedup on SHA-NI CPUs (Intel Skylake+, AMD Zen+)

### 2.3 Redex Search Assembly (reduce.Apply)

**Current**: Tree walk with memoization on `(digest, perm)`

**Optimized**: Term graph with structural sharing + parallel redex discovery

```go
// reduce/search_amd64.s
// Find all redexes in parallel using bit-parallel techniques

TEXT ·FindRedexes(SB), NOSPLIT, $0-48
    MOVQ    term+0(FP), DI      // root node
    MOVQ    perm+8(FP), SI      // effective permission bitmask
    MOVQ    out+16(FP), DX      // output slice
    // ... SIMD-accelerated spine walk
    // Use VPBROADCAST + VPTEST for permission checks
    RET
```

### 2.4 Permission Check as Bitmask

Replace string-based permission with 4-bit mask:

```go
// Current: []string{"W", "K", "C"}
// Optimized: uint8 bitmask
const (
    PermW = 1 << iota  // 1
    PermK              // 2
    PermC              // 4
    PermS              // 8
)

// Check: (effective & required) == required
// Single AND instruction vs string slice search
```

**Expected**: 5-10x faster permission checks

### 2.5 Fuel Loop Unrolling

```go
// reduce/fuel_amd64.s
// Unroll 4 steps at a time with explicit fuel counter

TEXT ·ReduceFuel4(SB), NOSPLIT, $0-64
    MOVQ    term+0(FP), DI
    MOVQ    fuel+8(FP), SI
    MOVL    $4, CX              // unroll factor
loop:
    CALL    ·FindNextStep(SB)
    TESTL   AX, AX              // no step?
    JZ      done
    CALL    ·ApplyStep(SB)
    DECQ    SI                  // fuel--
    JZ      exhausted
    DECX
    JNZ     loop
    // reload unroll counter
    JMP     loop
done:
    RET
exhausted:
    MOVL    $1, exhausted_flag
    RET
```

---

## 3. AND/OR Graph Algorithms for Confluence Exploration

### 3.1 Problem

Confluence checking requires exploring all reduction paths from a term. The reduction graph is an AND/OR graph:
- **OR nodes**: Choice of which redex to contract (strategy)
- **AND nodes**: Parallel branches that must all join (critical pairs)

### 3.2 Algorithm: Parallel Critical Pair Closure

```go
// confluence/orgraph.go

// OrGraph represents the AND/OR reduction graph
type OrGraph struct {
    Nodes    map[NodeID]*Node
    Roots    []NodeID
    Joined   map[PairID]bool
}

type Node struct {
    ID       NodeID
    Term     term.Term
    IsOr     bool    // true = strategy choice, false = parallel branch
    Children []NodeID
    Parents  []NodeID
    Depth    int
}

// ComputeClosure explores the graph up to bound, checking local confluence
func (g *OrGraph) ComputeClosure(bound int) (confluent bool, counterexample *Counterexample) {
    worklist := g.Roots[:]
    
    for len(worklist) > 0 && bound > 0 {
        bound--
        n := g.Nodes[worklist[0]]
        worklist = worklist[1:]
        
        if n.IsOr {
            // Strategy choice: explore each child
            for _, c := range n.Children {
                worklist = append(worklist, c)
            }
        } else {
            // Parallel branches: must all join
            if !g.checkJoin(n) {
                return false, g.buildCounterexample(n)
            }
        }
    }
    return true, nil
}

func (g *OrGraph) checkJoin(n *Node) bool {
    // Check if all children have a common descendant
    // Uses memoized transitive closure
    // ...
}
```

### 3.3 Assembly-Accelerated Graph Operations

```go
// confluence/orgraph_amd64.s
// Bit-parallel reachability for join checking

TEXT ·Reachable(SB), NOSPLIT, $0-40
    MOVQ    graph+0(FP), DI     // *OrGraph
    MOVQ    start+8(FP), SI     // start NodeID
    MOVQ    target+16(FP), DX   // target NodeID
    // ... BFS with bitset visited (64 nodes per register)
    // Use BSF/BSR for bitset iteration
    RET
```

### 3.4 Confluence Decision Procedure

For the `safe` and `settled` modes, the proof obligation is:

```
∀ peak (t₁ ← t → t₂) :
  ∃ t' : t₁ →* t' ∧ t₂ →* t'
```

The AND/OR graph encodes:
- OR: choice of which rule at each step
- AND: all parallel branches from a peak must join

**Bounded exploration**: Up to depth 8, all terms ≤ 5 leaves (per SPEC.md §13 exhaustive search)

---

## 4. Integration Checklist

- [ ] SPARK Ada specs for `term`, `reduce`, `trace`, `sched`, `budget`
- [ ] GNATprove level 2 passes on all packages
- [ ] SHA-256 AVX2/SHA-NI assembly for `term.Digest`
- [ ] Bitmask permissions in `reduce`
- [ ] Fuel loop unrolling in `reduce.Reduce`
- [ ] AND/OR graph for confluence testing (`confluence/orgraph.go`)
- [ ] Assembly bitset reachability for join checking
- [ ] Benchmark: `go test -bench=. -benchtime=10s ./...`

---

## 5. References

- SPARK Ada RM: https://docs.adacore.com/spark2014-docs/html/ug/
- Intel SHA Extensions: https://www.intel.com/content/www/us/en/docs/intrinsics-guide/index.html#techs=SHA
- Agda formalization of confluence: `verifiers/idris/Spherepop.idr`
- Exhaustive search bounds: SPEC.md §13
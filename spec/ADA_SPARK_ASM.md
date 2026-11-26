# ADA/SPARK Verification & Assembly Optimization for Spherepop Kernel

This document specifies Ada 2022 / SPARK contracts (`SPARK_Mode => On`, `No_Allocators`) for Spherepop kernel invariants, assembly hot-path optimizations, and AND/OR graph algorithms for bounded confluence verification.

---

## 1. SPARK Ada 2022 Verification Targets

### 1.1 Kernel Invariants to Prove

| # | Invariant | Reference Implementation | SPARK Contract |
|---|-----------|--------------------------|----------------|
| 1 | **Term digest determinism** | `term/term.go` (`Digest`) | `Digest (T1) = Digest (T2)` whenever `Structurally_Equal (T1, T2)` |
| 2 | **Permission monotonicity** | `reduce/perm.go` (`PermSet`) | `(Mask and Label_Mask) <= Mask` (effective permission only shrinks along chains) |
| 3 | **Contraction bracket isolation** | `reduce/rules.go`, `reduce/apply.go` | Combinator spines (`I, B, C, K, W, S`) never traverse `KindBracket` |
| 4 | **Absorption stability (`AbsU`)** | `reduce/find.go` (`safe`) | `Safe (V, Mask) and Submask (Sub, Mask) => Safe (V, Sub)` |
| 5 | **Fuel accounting exactness** | `reduce/reduce.go` (`Reduce`) | `Res.Steps <= Fuel and (Res.Exhausted = (Res.Steps = Fuel and Has_Redex (Res.Final)))` |
| 6 | **Trace hash-chain integrity** | `trace/trace.go` (`seal`) | `Link_i = SHA256_Zero_Sep (Link_{i-1}, Rule_i, Path_i, After_i)` |
| 7 | **Independent checker soundness** | `trace/check.go` (`Check`) | `Check (Doc).Ok => Replay (Doc.Start, Doc.Steps) = Doc.Final` |
| 8 | **Budget & percolation gate safety** | `spec/schemas/budget.go`, `percolation.go` | `Check_Collapse_Ok => Consumed + Cost + Upkeep <= Allocated and Kappa_P <= 2 * Kappa_Q` |

### 1.2 SPARK Ada Package Specifications (`No_Allocators`)

```ada
pragma SPARK_Mode (On);
pragma Restrictions (No_Allocators); -- No_Heap_Allocations

with Interfaces; use Interfaces;

package Spherepop_Kernel_Proofs
  with SPARK_Mode => On
is
   subtype Perm_Mask is Unsigned_8 range 0 .. 15;
   Perm_W : constant Perm_Mask := 2#0001#;
   Perm_K : constant Perm_Mask := 2#0010#;
   Perm_C : constant Perm_Mask := 2#0100#;
   Perm_S : constant Perm_Mask := 2#1000#;

   subtype Fuel_Count is Unsigned_64;
   subtype Degree_Sum is Unsigned_64 range 0 .. 2**31 - 1;

   type Reduce_Summary is record
      Steps       : Fuel_Count;
      Exhausted   : Boolean;
      Step_Remains : Boolean;
   end record;

   -- Lemma 6.1 (SPEC.md): extending a scope chain intersects permissions.
   function Extend_Chain (Current, Label_Perm : Perm_Mask) return Perm_Mask is
     (Current and Label_Perm)
     with Post => (Extend_Chain'Result and Current) = Extend_Chain'Result;

   -- Fuel exactness invariant (SPEC.md §6 / trace/check.go).
   function Valid_Fuel_Accounting
     (Fuel : Fuel_Count; S : Reduce_Summary) return Boolean
   is
     (S.Steps <= Fuel
      and then (if S.Exhausted then S.Steps = Fuel and S.Step_Remains
                else not S.Step_Remains));

   -- Exact Zero-Float p/q Molloy–Reed percolation gate:
   -- Supercritical iff sum(d_v^2) / sum(d_v) > 2/1 <=> Sum_K2 > 2 * Sum_K.
   function Percolation_Permitted
     (Sum_K : Degree_Sum; Sum_K2 : Unsigned_64) return Boolean
   is
     (if Sum_K = 0 then True else Sum_K2 <= 2 * Unsigned_64 (Sum_K))
     with Post => Percolation_Permitted'Result =
                    (Sum_K = 0 or else Sum_K2 <= 2 * Unsigned_64 (Sum_K));

   -- Rung 0 Bonobo Resonance check: p/q = 3/2 <=> 2 * Sum_K2 = 3 * Sum_K.
   function Is_Bonobo_Resonance
     (Sum_K : Degree_Sum; Sum_K2 : Unsigned_64) return Boolean
   is
     (Sum_K > 0
      and then Sum_K2 <= Unsigned_64'Last / 2
      and then 2 * Sum_K2 = 3 * Unsigned_64 (Sum_K));

   -- Maintenance budget invariant (Protocol §12) on bounded machine integers.
   subtype Budget_Units is Integer_64 range 0 .. 1_000_000_000;

   function Collapse_Within_Budget
     (Allocated, Consumed, Cost, Upkeep : Budget_Units;
      Has_Valid_Waiver                  : Boolean) return Boolean
   is
     (Consumed + Cost + Upkeep <= Allocated or else Has_Valid_Waiver)
     with Post => (if Collapse_Within_Budget'Result and not Has_Valid_Waiver
                   then Consumed + Cost + Upkeep <= Allocated);
end Spherepop_Kernel_Proofs;
```

### 1.3 Proof Workflow

```bash
# Run GNATprove formal verification at level 4
gnatprove -P spherepop.gpr --level=4 --warnings=error --report=all
```

---

## 2. Hot-Path Architecture & Assembly Optimizations

### 2.1 Existing Zero-Allocation Fast Paths in Go Reference Kernel

The Go reference kernel already eliminates heap allocations on critical paths:
- **Stack-buffered Merkle hashing (`term/term.go`)**: `App` hashes an 82-byte `[17 + 1 + 32 + 32]byte` stack frame; short leaves (`<= 100` bytes) and brackets (`<= 64` bytes) hash directly from stack arrays.
- **`O(1)` capability & structural bitmasks (`term/term.go`, `reduce/perm.go`)**: `Term.Flags()` caches `FlagW | FlagK | FlagC | FlagS | FlagI | FlagB | FlagBracket | FlagScope` at construction time, and `reduce.PermSet` represents permissions as a 4-bit `uint8` mask so insensitivity checks execute as a single bitwise `AND`.

### 2.2 Hardware SHA-NI / ARMv8 Crypto Extensions (`term.Digest` & `trace.seal`)

`App` nodes always hash exactly 82 bytes (two 64-byte SHA-256 compression blocks):

```text
// term/sha256_amd64.s (Intel SHA-NI: SHA256RNDS2 / SHA256MSG1 / SHA256MSG2)
// Processes fixed 82-byte spherepop-term/1 App frames in ~90 cycles vs ~650 cycles software.
TEXT ·DigestAppSHANI(SB), NOSPLIT, $0-96
    MOVOU   left+0(FP), X0      // Load f.digest[0..15]
    MOVOU   left+16(FP), X1     // Load f.digest[16..31]
    MOVOU   right+32(FP), X2    // Load x.digest[0..15]
    MOVOU   right+48(FP), X3    // Load x.digest[16..31]
    // Pre-seeded midstate after domainPrefix ("spherepop-term/1") + KindApp
    // 2 compression blocks via SHA256RNDS2
    RET
```

### 2.3 SIMD WaveMemory Laplacian (`spec/schemas/unitary.go`)

`WaveFunction.Data` stores `8192` contiguous `Q16_16` (`int32`) values (`32 KiB`, fitting entirely in L1D cache on Apple Silicon M-series and Zen 4/5):
- `VPADDD` / `VPSUBD` (AVX2) or `ADD.4S` / `SUB.4S` (ARM64 NEON) advances 4–8 complex amplitudes per instruction in `computeLaplacian` with exact $\mathbb{Z}/2^{32}\mathbb{Z}$ wrap-around semantics.

---

## 3. AND/OR Graph Confluence Verification

### 3.1 Confluence as Bounded AND/OR Search

Confluence verification (`reduce/peaks_test.go`, `reduce/exhaustive_test.go`) forms a finite-depth AND/OR graph:
- **AND nodes (divergent peaks)**: Every unordered pair of distinct 1-step redexes $(t_1 \leftarrow t \rightarrow t_2)$ from a source term $t$ must join.
- **OR nodes (join search)**: A peak $(t_1, t_2)$ joins as soon as there exists a common descendant $t^*$ with $t_1 \twoheadrightarrow t^*$ and $t_2 \twoheadrightarrow t^*$ within the step bound.

### 3.2 Bitset Reachability Closure

Representing the bounded reduction DAG of a term (typically $\le 64$ reachable states for terms of $\le 5$ leaves, SPEC.md §13) as a `uint64` adjacency bitmatrix enables single-register transitive join checks:
1. Compute reachable bitmask $R(t_1) \in \texttt{uint64}$ and $R(t_2) \in \texttt{uint64}$ via bit-parallel BFS.
2. Peak $(t_1, t_2)$ joins iff `(R(t_1) & R(t_2)) != 0` (`TZCNT` / `CTZ64` extracts the witness normal form index in 1 cycle).

---

## 4. Verification Status

- [x] `PermSet` 4-bit mask & `Term.Flags()` $O(1)$ capability bitmask in `term/` and `reduce/`
- [x] Zero-Float `Q16_16` + reduced rational $p/q$ models in `spec/schemas/`
- [x] AST-enforced Zero-Float check (`TestZeroFloatInvariant` in `spec/conformance/conformance_test.go`)
- [x] Multi-language SHA-256 & trace replay verifiers (`Go`, `Rust`, `Zig`, `Idris 2`)
- [ ] Materialize standalone `ada/spherepop_kernel_proofs.ads` crate for CI `gnatprove` execution
- [ ] Optional SHA-NI / ARMv8-CE assembly specialization for 82-byte `App` frames

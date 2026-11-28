# Spherepop Kernel × Erdős–Lódri UltraGraph × FASM-TYCOON Integration

This document maps the three research artifacts into a unified architecture for **sovereign, deterministic, zero-float computation** across the stack.

---

## 1. Conceptual Mapping

| Layer | Spherepop Kernel | UltraGraph | FASM-TYCOON |
|-------|------------------|------------|-------------|
| **Core Invariant** | `pop/bind/transform/verify/collapse/refuse` | Structural Honesty (`--erdos-anomaly`) | Zero-Float $p/q$ Law (`Q16.16` + $\mathbb{Z}/2^{32}\mathbb{Z}$) |
| **History Model** | Hash-chained `spherepop-trace/1` log | Shadow Graph $G_{\text{SHADOW}} = (V, E_{\text{SHADOW}})$ | `.m8a` Compact Context + Timeline |
| **Composition** | Nested `<t>_l` brackets + `sc_l` obligations | Szemerédi `[SNCP]+w` LOD + Steiner Hub $Z(G)$ | Record → Admit → Commit Gate |
| **Verification** | Independent trace checker (Go/Zig/Rust/Idris) | Exact $p/q$ Molloy–Reed $\kappa > 2$ percolation gate | `fasmg` Borrow-Checker + `ParkSandbox` Stepper |
| **Hardware Target** | ISA-agnostic (WASM, POSIX, bare metal) | WaveMemory `64×64` (`32 KiB`) damped lattice | RV32IMAC / RV64IMAC / Thumb-2 / AArch64 |
| **Float Policy** | AST-verified Zero-Float in `spec/` | Zero-Float $p/q$ + `Q16.16` | `#![deny(clippy::float_arithmetic)]` |

---

## 2. Unified Architecture Stack

```text
┌─────────────────────────────────────────────────────────────────────┐
│                    APPLICATION / AGENT LAYER                        │
│  Spherepop Dispositions │ UltraGraph LOD Shader │ FASM-TYCOON Agent │
└─────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────┐
│                    VERIFICATION LAYER                               │
│  trace.Check()  ◄──►  Molloy–Reed κ (p/q)  ◄──►  fasmg Ownership    │
│  (4-lane)             (O(|V|+|E|), κ ≤ 2)        (Compile-time)     │
└─────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────┐
│                    EXECUTION LAYER                                  │
│  Config.Reduce()  ◄──►  Strang-split Unitary  ◄──►  ParkSandbox     │
│  (fuel-bounded)         (Q16.16, P̂_C = p/q)         (Deterministic) │
└─────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────┐
│                    HARDWARE / MEMORY LAYER                          │
│  Merkle DAG Terms  ◄──►  64×64 WaveMemory  ◄──►  64KB Arena         │
│  (spherepop-term/1)      (32 KiB L1D-fit)       (cache-aligned)     │
└─────────────────────────────────────────────────────────────────────┘
```

---

## 3. Cross-Artifact Invariants

### 3.1 Zero-Float $p/q$ Law $\iff$ Spherepop Term Determinism

- **UltraGraph**: Every density, coupling, and threshold is an exact reduced rational $p/q$ (`gcd(p, q) = 1`) or `Q16.16` fixed-point integer in $\mathbb{Z}/2^{32}\mathbb{Z}$.
- **Spherepop**: Term Merkle digest `SHA256("spherepop-term/1" || kind || children)` is strictly structural and platform-independent.
- **Solar Phylogenetic Octad Resonance**: Shadow graphs at Rung 0 Bonobo Target Resonance ($\kappa = 3/2$) emit `resonance_trit = +1` while remaining strictly subcritical ($\kappa < 2/1$).

### 3.2 Structural Honesty $\iff$ Pre-Collapse Percolation Gate

The Molloy–Reed ratio reduces directly to an exact integer quotient with zero floating-point division:

$$
\kappa = \frac{\langle k^2 \rangle}{\langle k \rangle} = \frac{\sum_{v \in V} d_v^2}{\sum_{v \in V} d_v} = \frac{P_\kappa}{Q_\kappa}
$$

- **Subcritical ($\sum d_v^2 < 2 \sum d_v$)**: Bounded component diameters; `collapse` permitted (`+1` trit when $2\sum d_v^2 = 3\sum d_v$, i.e. $\kappa = 3/2$).
- **Critical ($\sum d_v^2 = 2 \sum d_v$)**: Phase transition boundary; `collapse` permitted with `resonance_trit = 0`.
- **Supercritical ($\sum d_v^2 > 2 \sum d_v$)**: Giant transitive component across module boundaries; `CheckCollapseGate()` refuses with `ErrPercolationRisk` (`resonance_trit = -1`).

### 3.3 Szemerédi LOD $\iff$ Nested Scope Composition

- **Spherepop**: Nested scope constructors `B (sc_y) (sc_x) a -> <<a>_x>_y` isolate capability chains $\pi(\varepsilon) \cap \pi(y) \cap \pi(x)$.
- **UltraGraph**: `SzemerediPartition` refines $\varepsilon$-irregular vertex pairs using exact bipartite edge density $|E(A,B)| / (|A|\cdot|B|)$ into $\le \lfloor 4/\varepsilon^2 \rfloor$ `[SNCP]+w` pods and selects the Erdős–Steiner 1-median hub `Z(G)` by exact BFS shortest-path minimization on the quotient graph.

### 3.4 FASM-TYCOON Admit Gate $\iff$ Authorized Collapse

| FASM-TYCOON Stage | Spherepop Equivalent | Verification Artifact |
|-------------------|----------------------|-----------------------|
| `Record` | `pop` + `bind` + `transform` | `reduce.Result` + `spherepop-trace/1` |
| `Admit` | `verify` | `trace.Check` + `Footprint.Subsumes` + `ShadowGraph.CheckCollapseGate` + `Budget.CheckCollapse` |
| `Commit` | `collapse` | Head-bound Ed25519 `Authorization.VerifyForOperation("collapse", head, ...)` |

---

## 4. Implemented Go Reference APIs (`spec/schemas` & `spec/authorization`)

All P0 models compile and run under `package schemas` (`spec/schemas/`) and `package authorization` (`spec/authorization/`), enforced by `TestZeroFloatInvariant`:

```go
// 1. Exact Rational p/q Percolation Gate (spec/schemas/percolation.go)
sg, err := schemas.BuildShadowGraphFromTerm(parsedTerm, pkgMap)
kappa := sg.MolloyReedKappa() // schemas.Ratio{P, Q}, e.g. 3/2 (+1 Bonobo trit)
if err := sg.CheckCollapseGate(); errors.Is(err, schemas.ErrPercolationRisk) {
    // Refuse collapse: transitive cross-boundary giant component (kappa > 2/1)
}

// 2. Zero-Float Szemerédi Regularity LOD Partition (spec/schemas/lod.go)
part, err := schemas.SzemerediPartition(adj, labels, schemas.Ratio{P: 1, Q: 10})
// part.SuperNodes: []SuperNode with CentroidQ16, WeightRatio (p/q), WavefieldQ16
// part.SteinerHub: content-addressed Erdős–Steiner 1-median Z(G)

// 3. Q16.16 Strang-Split WaveMemory Evolution (spec/schemas/unitary.go)
psi := schemas.InitializeWaveFunction()
vks := schemas.BuildKohnShamPotential(psi)
schemas.StrangFullStep(psi, vks, 1024) // dt = 1/64 in Q16.16

// 4. Head-Bound Ed25519 Authorization (spec/authorization/authorization.go)
err = auth.VerifyForOperation("collapse", currentHead, usedNonces, now)
```

---

## 5. Verification & Build Pipeline

```bash
# 1. Go reference kernel + P0 schemas + 24 conformance vectors + Zero-Float AST check
go vet ./...
go test -v ./...

# 2. Independent trace verifiers
cargo test --manifest-path verifiers/rust/Cargo.toml
zig test verifiers/zig/src/verifier.zig
```

---

## 6. Roadmap Status

| Priority | Deliverable | Status |
|----------|-------------|--------|
| **P0** | 11 Draft 2020-12 JSON schemas (`term`, `config`, `trace`, `budget`, `footprint`, `governance`, `authorization`, `percolation`, `lod`, `unitary`, `vectors`) | Done |
| **P0** | 24 verified conformance vectors (19 base + 5 UltraGraph) with real `spherepop-term/1` SHA-256 digests & traces | Done |
| **P0** | Zero-Float $p/q$ + `Q16.16` Go implementations (`authorization`, `budget`, `footprint`, `governance`, `percolation`, `lod`, `unitary`) + unit tests | Done |
| **P1** | Port `Q16Mul` & percolation/entanglement $p/q$ checks to Zig, Rust, Idris 2, and SPARK Ada 2022 (`gnatprove` 21/21) | Done |
| **P2** | CI `smart-tree` shadow-graph $\kappa \le 2$ gate on pull requests | Planned |
| **P3** | Compact `.m8a` binary trace framing across all 4 verifier lanes | Planned |

---

*Unified by the axiom: **Ahogy a dolgok vannak** — "As things are."*

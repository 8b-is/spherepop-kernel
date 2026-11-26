# Spherepop Kernel — P0 Specification & UltraGraph Integration Artifacts

This directory contains the machine-readable Draft 2020-12 JSON schemas, 24 conformance test vectors, Zero-Float $p/q$ + `Q16.16` Go reference implementations, and formal verification / architecture guides for the Spherepop Kernel × Erdős–Lódri UltraGraph × FASM-TYCOON stack.

## Directory Structure

```text
spec/
├── README.md                          # Artifact index & verification commands
├── ADA_SPARK_ASM.md                   # SPARK Ada 2022 contracts, SIMD/SHA-NI ASM & AND/OR confluence
├── INTEGRATION.md                     # Spherepop × UltraGraph × FASM-TYCOON cross-layer mapping
├── authorization/
│   ├── authorization.schema.json      # Head-bound capability authorization schema (Protocol §5.3)
│   ├── authorization.go               # Ed25519 signature, head-match, expiry & replay-nonce verifier
│   ├── authorization_test.go          # Cryptographic & replay-protection unit tests
│   └── example-commit.json            # Deterministic Ed25519-signed commit:sphere fixture
├── conformance/
│   ├── vectors.schema.json            # Conformance vector suite schema (base + UltraGraph)
│   ├── vectors.json                   # 24 verified vectors (19 base + 5 UltraGraph) with real SHA-256 digests
│   └── conformance_test.go            # Kernel, trace interop, schema & Zero-Float AST test runner
└── schemas/
    ├── term.json                      # spherepop-term/1 — content-addressed Merkle DAG terms
    ├── config.json                    # spherepop-config/1 — canonical permission & boundary config
    ├── trace.json                     # spherepop-trace/1 — null-delimited SHA-256 execution trace
    ├── budget.json / budget.go        # Maintenance budget & waiver enforcement (Protocol §12)
    ├── footprint.json / footprint.go  # Environment footprint subsumption evidence ⊑ current (Protocol §14)
    ├── governance.json / governance.go# Exact rational p/q quorum, veto, emergency & RFC 6902 patch (Protocol §10)
    ├── percolation.json / percolation.go # Exact rational p/q Molloy–Reed κ gate & Rung 0 Bonobo resonance
    ├── lod.json / lod.go              # Zero-float Szemerédi ε-regular [SNCP]+w partition & Steiner hub Z(G)
    ├── unitary.json / unitary.go      # Q16.16 + ℤ/(2³²ℤ) Kohn–Sham / Strang-split WaveMemory stepper
    └── schemas_test.go                # Unit tests for all 6 schema models & p/q invariants
```

## JSON Schemas (11)

| Schema | Domain / Protocol Ref | Key Invariants |
|--------|-----------------------|----------------|
| `schemas/term.json` | `spherepop-term/1` (SPEC §2) | `atom`, `const`, `scope`, `app`, `bracket` with 64-hex SHA-256 Merkle digests |
| `schemas/config.json` | `spherepop-config/1` (SPEC §3, §5) | Canonical `root`, `perm`, `gated`, `absorb`, `sc` matching `reduce.ConfigSpec` |
| `schemas/trace.json` | `spherepop-trace/1` (SPEC §8) | Null-separated SHA-256 genesis/step chain, `^[LRB]*$` redex paths, independent replay |
| `schemas/budget.json` | Protocol §12 | Per-unit `allocated`/`consumed`/`estimated_upkeep`, reduced `utilization_ratio` ($p/q$), waivers |
| `schemas/footprint.json` | Protocol §14 | Arch, OS, libc, semver runtimes, accelerators, manifest CID (`evidence ⊑ current`) |
| `schemas/governance.json` | Protocol §10 | RFC 6902 patch, exact `quorum_ratio` ($p/q$), veto holders, emergency path ($1/2$ + unanimous veto) |
| `authorization/authorization.schema.json` | Protocol §5.3 | Head-bound Ed25519 capability (`commit:sphere`, `pop:sphere`, `verify:evidence`, `govern:policy`) |
| `schemas/percolation.json` | UltraGraph §1.2 | Exact reduced `kappa_ratio` ($\sum d_v^2 / \sum d_v$), Rung 0 Bonobo trit (`+1` at $3/2$), $\kappa > 2$ refusal |
| `schemas/lod.json` | UltraGraph §2 | Zero-float `epsilon_ratio`, `Q16.16` super-node centroids, `weight_ratio` ($p/q$), Steiner hub `Z(G)` |
| `schemas/unitary.json` | UltraGraph §3.1 | `Q16.16` Strang half-steps, exact `entanglement_ratio` ($|d_T - d_R|/(d_T + d_R)$), salience |
| `conformance/vectors.schema.json` | Conformance Suite | Validates both `vectors` (19 base) and `ultragraph_vectors` (5 extended) |

## Conformance Vectors (24 Total)

### Base Kernel Vectors (19)

| ID | Category | Start Term | Strategy / Fuel | Expected Outcome |
|----|----------|------------|-----------------|------------------|
| `pop-001` | pop | `I a` | `lo` / 10 | `a` (1 step, embedded trace) |
| `pop-002` | pop | `K a b` | `lo` / 10 | `a` (1 step, embedded trace) |
| `pop-003` | pop | `K a b` (`K` denied) | `lo` / 10 | `K a b` (0 steps) |
| `pop-004` | pop | `B I I a` | `lo` / 10 | `a` (3 steps) |
| `pop-005` | pop | `C I a b` | `lo` / 10 | `b a` (2 steps) |
| `pop-006` | pop | `W I a` | `lo` / 10 | `a a` (2 steps) |
| `pop-007` | pop | `S I I a` | `lo` / 10 | `a a` (3 steps, embedded trace) |
| `pop-008` | pop | `K a (W S S (W S S))` | `lo` / 10 | `a` (1 step) |
| `pop-009` | pop | `K a (W S S (W S S))` | `li` / 10 | `exhausted` (10 steps) |
| `bind-001` | bind | `sc_x a` | `lo` / 10 | `<a>_x` (1 step, embedded trace) |
| `bind-002` | bind | `<I>_x a` | `lo` / 10 | `<a>_x` (2 steps, embedded trace) |
| `bind-003` | bind | `<I>_x (K a b)` (`AbsU`) | `lo` / 10 | `<I>_x (K a b)` (0 steps, Theorem 4.9 guard) |
| `bind-004` | bind | `sc_x (K a b)` (`safe`) | `lo` / 10 | `<a>_x` (2 steps) |
| `bind-005` | bind | `sc_x (K a b)` (`settled`) | `lo` / 10 | `sc_x (K a b)` (0 steps) |
| `compose-001` | compose | `sc_y (sc_x a)` | `lo` / 10 | `<<a>_x>_y` (2 steps) |
| `compose-002` | compose | `sc_x (W I a)` | `lo` / 10 | `<W I a>_x` (1 step, permission shadowing) |
| `fuel-001` | fuel | `S I I a` | `lo` / 2 | `exhausted` (2 steps) |
| `fuel-002` | fuel | `S I I a` | `lo` / 3 | `a a` (3 steps) |
| `error-001` | error | `sc_unknown a` | `lo` / 10 | `error` (`ErrUnknownLabel`) |

### UltraGraph Extension Vectors (5)

| ID | Gate / Invariant | Verification Target |
|----|------------------|---------------------|
| `percolation-001` | Molloy–Reed $\kappa = 3/2$ (`trit = +1`) | Subcritical Rung 0 Bonobo resonance permits collapse (`sc_s (K a b) -> <a>_s`) |
| `percolation-002` | Molloy–Reed $\kappa = 3/1 > 2/1$ (`trit = -1`) | Supercritical $K_4$ shadow graph refuses collapse with `ErrPercolationRisk` |
| `lod-001` | Szemerédi LOD ($\varepsilon = 1/10$) | 2-component triangle graph compresses to 2 `[SNCP]+w` pods + deterministic `Z(G)` |
| `unitary-001` | Strang Unitary + Entanglement $\hat{P}_C = 23/25$ | `64×64` `Q16.16` WaveMemory Strang step + `S K K state -> state` |
| `zero-float-001` | `Q16.16` Exact Fixed-Point | `0x00018000 * 0x00020000 == 0x00030000` (`196608`) + AST Zero-Float enforcement |

## Running Verification

```bash
# Run all Go kernel, spec, schema, authorization & conformance tests
go vet ./...
go test -v ./spec/...
go test ./...

# Run independent Rust, Zig, Idris 2 & SPARK Ada 2022 verifiers
cargo test --manifest-path verifiers/rust/Cargo.toml
zig test verifiers/zig/src/verifier.zig
idris2 --source-dir verifiers/idris --check verifiers/idris/Spherepop.idr
gnatprove -P verifiers/ada/spherepop.gpr --level=2 --warnings=error
```

## Formal Proof & Architecture Guides

- [ADA_SPARK_ASM.md](ADA_SPARK_ASM.md) — SPARK Ada 2022 contracts (`No_Allocators`), SHA-NI/AVX2 assembly hot paths, and AND/OR confluence closure.
- [INTEGRATION.md](INTEGRATION.md) — Unified architecture mapping Spherepop Kernel × Erdős–Lódri UltraGraph × FASM-TYCOON.

# Spherepop Kernel — P0 Specification Artifacts

This directory contains the machine-readable schemas, conformance vectors, and authorization/budget/footprint/governance models specified in the Spherepop Development Protocol review.

## Directory Structure

```
spec/
├── schemas/
│   ├── term.json          # spherepop-term/1 — content-addressed immutable terms
│   ├── config.json        # spherepop-config/1 — permission structure & absorption modes
│   ├── trace.json         # spherepop-trace/1 — hash-chained execution traces
│   ├── budget.json        # Maintenance budget allocation & enforcement
│   ├── footprint.json     # Environment footprint with subsumption
│   └── governance.json    # Governance amendment procedure
├── conformance/
│   ├── vectors.schema.json  # JSON Schema for conformance vectors
│   ├── vectors.json         # 20 conformance test vectors (pop, bind, compose, fuel, error)
│   └── conformance_test.go  # Go reference kernel test runner
└── authorization/
    ├── authorization.schema.json  # Capability-based authorization (head-bound)
    ├── authorization.go           # Verification logic (head-check, expiry, nonce, sig)
    └── example-commit.json        # Example authorization for collapse
```

## Schemas

### term.json
Canonical term representation with Merkle digests. Matches SPEC.md §2.
- Kinds: `atom`, `const`, `scope`, `app`, `bracket`
- Each node carries SHA-256 digest computed per ARCHITECTURE.md §3.1

### config.json
Permission structure and absorption/sc modes. Matches SPEC.md §3, §5.
- `root_permission`: pi(ε)
- `label_permissions`: pi(l) per label
- `gated_constants`: which constants require permission
- `absorption_mode`: off | any | nf | s | u
- `scope_mode`: any | safe | settled

### trace.json
Hash-chained execution trace. Matches SPEC.md §8, ARCHITECTURE.md §3.2.
- `format`: "spherepop-trace/1"
- Null-delimited SHA-256 hash chain
- Independent checker replays every step

### budget.json
Maintenance budget per Spherepop Development Protocol §12.
- Per-unit allocation/consumption/estimated_upkeep
- Governance waivers for BUDGET_EXCEEDED override
- Kernel MUST refuse collapse if projected > allocated (unless waiver)

### footprint.json
Environment footprint per Spherepop Development Protocol §14.
- Arch, OS, libc, runtime versions, accelerators, manifest CID
- Subsumption: evidence ⊑ current (not equality)

### governance.json
Governance amendment per Spherepop Development Protocol §10.
- RFC 6902 JSON Patch against policy
- Quorum (default 67%), veto holders, emergency path (50% + all veto)

## Conformance Vectors (20)

| ID | Category | Description |
|----|----------|-------------|
| pop-001 | pop | I contraction |
| pop-002 | pop | K contraction (permission granted) |
| pop-003 | pop | K contraction (permission denied) |
| pop-004 | pop | B contraction |
| pop-005 | pop | C contraction |
| pop-006 | pop | W contraction |
| pop-007 | pop | S contraction |
| pop-008 | pop | Strategy: K a Omega under lo |
| pop-009 | pop | Strategy: K a Omega under li (cycles) |
| bind-001 | bind | Sc boundary |
| bind-002 | bind | Abs boundary (v settled) |
| bind-003 | bind | Abs blocked (v not R-normal) |
| bind-004 | bind | Sc safe mode blocked |
| bind-005 | bind | Sc settled mode blocked |
| compose-001 | compose | Nested sc_y (sc_x a) |
| compose-002 | compose | Permission shadowing |
| fuel-001 | fuel | Fuel exhaustion |
| fuel-002 | fuel | Exact fuel → normal_form |
| error-001 | error | Unknown label |

Run: `go test ./spec/conformance/...`

## Authorization Model

Capability-based authorization held against current head (Protocol §5.3).

```go
// Critical rule: authorization.head MUST match kernel's current head at collapse
func (a *Authorization) Verify(currentHead string, usedNonces map[string]bool) error
```

Capabilities: `commit:sphere`, `pop:sphere`, `verify:evidence`, `govern:policy`

## ADA/Spark Verification & Assembly Suggestions

See [ADA_SPARK_ASM.md](ADA_SPARK_ASM.md) for:
- SPARK Ada proofs for kernel invariants
- Assembly optimizations for hot paths (term digest, hash chain, reduction loop)
- AND/OR graph algorithms for confluence exploration

## Multi-Language Verification

All schemas are implementation-language agnostic. The four verification lanes (Go, Zig, Rust, Idris 2) consume the same JSON schemas and conformance vectors.

- Go: Reference kernel (`term/`, `reduce/`, `trace/`, `sched/`)
- Zig: `verifiers/zig/src/verifier.zig` — bit-exact trace replay
- Rust: `verifiers/rust/src/lib.rs` — zero-dep FIPS 180-4 verifier
- Idris 2: `verifiers/idris/Spherepop.idr` — QTT multiplicity proofs
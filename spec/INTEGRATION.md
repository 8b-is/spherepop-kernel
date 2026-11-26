# Spherepop Kernel × Erdős–Lódri UltraGraph × FASM-TYCOON Integration

This document maps the three research artifacts into a unified architecture for **sovereign, deterministic, zero-float computation** across the stack.

---

## 1. Conceptual Mapping

| Layer | Spherepop Kernel | UltraGraph | FASM-TYCOON |
|-------|------------------|------------|-------------|
| **Core Invariant** | `pop/bind/transform/verify/collapse/refuse` | Structural Honesty (`--erdos-anomaly`) | Zero-Float p/q Law (`Q16.16 + ℤ2³²`) |
| **History Model** | Hash-chained event log | Shadow Graph `G_SHADOW = (V, E_SHADOW)` | `.m8a` Compact Context + Timeline |
| **Composition** | Nested spheres + boundary obligations | Szemerédi Super-Node LOD + Steiner Hub `Z(G)` | Record → Admit → Commit Gate |
| **Verification** | Independent trace checker (Go/Zig/Rust/Idris) | Molloy–Reed κ > 2 percolation check | fasmg Borrow-Checker + ParkSandbox Stepper |
| **Hardware Target** | ISA-agnostic (WASM, POSIX, ESP32-S3) | WaveMemory `64×64` damped lattice | RV32IMAC / RV64IMAC / Thumb-2 / AArch64 |
| **Float Policy** | Not specified | Zero-Float p/q Law | `#![deny(clippy::float_arithmetic)]` |

---

## 2. Unified Architecture Stack

```
┌─────────────────────────────────────────────────────────────────────┐
│                    APPLICATION / AGENT LAYER                        │
│  Spherepop Dispositions │ UltraGraph LOD Shader │ FASM-TYCOON Agent │
└─────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────┐
│                    VERIFICATION LAYER                               │
│  trace.Check()  ◄──►  Molloy–Reed κ  ◄──►  fasmg Ownership Check  │
│  (4-lane)         (O(|V|+|E|))       (Compile-time, 0 bytes)      │
└─────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────┐
│                    EXECUTION LAYER                                  │
│  reduce.Reduce()  ◄──►  Strang-split Unitary  ◄──►  ParkSandbox    │
│  (fuel-bounded)   (e⁻ⁱᴴΔᵗ/², P̂C=0.90)     (Deterministic Stepper)  │
└─────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────┐
│                    HARDWARE / MEMORY LAYER                          │
│  Merkle DAG Terms  ◄──►  64×64 WaveMemory  ◄──►  64KB Arena       │
│  (content-addressed)   (damped, holographic)   (cache-aligned)     │
└─────────────────────────────────────────────────────────────────────┘
```

---

## 3. Cross-Artifact Invariants

### 3.1 Zero-Float p/q Law ⇔ Spherepop Term Determinism

**UltraGraph** (Eq. 6): `p/q ⟶ Q16.16 + ℤ2³²` — exact abelian group, strict associativity.

**Spherepop**: Term Merkle digest `SHA256(domain ‖ kind ‖ children)` — content-addressed, deterministic.

**FASM-TYCOON**: `#![deny(clippy::float_arithmetic)]` — all arithmetic integer/fixed-point.

**Integration**: Spherepop `term.Digest()` becomes the *commitment hash* for UltraGraph state transitions. Every `collapse` in Spherepop corresponds to a `Strang half-step` in WaveMemory, both using `Q16.16` arithmetic via FASM-TYCOON's `q16_mul` macro.

### 3.2 Structural Honesty ⇔ Spherepop Trace Integrity

**UltraGraph**: `--erdos-anomaly` extracts `G_SHADOW` where `lut[u] ≠ lut[v]`. Molloy–Reed `κ = ⟨k²⟩/⟨k⟩ > 2` signals transitive risk.

**Spherepop**: Trace checker verifies *every step* against recorded config. `Linkᵢ = H(Linkᵢ₋₁ ‖ Ruleᵢ ‖ Pathᵢ ‖ Afterᵢ)`.

**FASM-TYCOON**: `audit_stream_line()` intercepts LLM tokens, runs `fasmg` + `ParkSandbox::record_trace()` before commit.

**Integration**: Spherepop trace = UltraGraph's `G_SHADOW` edge log. The Molloy–Reed `κ` check becomes a **pre-collapse budget gate** in Spherepop: if `κ > 2` on the dependency shadow graph of the proposed sphere, `RefusalReason.PERCOLATION_RISK`.

### 3.3 Szemerédi LOD ⇔ Spherepop Nested Composition

**UltraGraph**: `V1..V4` super-nodes + `[SNCP] + w` + Erdős–Steiner hub `Z(G)`. `felix_dot64_f32` (6-cycle) inner product.

**Spherepop**: Nested spheres (`pop` → `bind` → `transform` → `verify` → `collapse`). Boundary obligations discharged at `collapse`.

**FASM-TYCOON**: `.m8a` context frame compresses 3000 tokens → 120 tokens. `SWAR` dirty cache-line tracking.

**Integration**: Spherepop **nested sphere** = UltraGraph **super-node**. The `boundary obligations` (§11) = `Pivot Halos` connecting `[SNCP]`. The Erdős–Steiner hub `Z(G)` = Spherepop **kernel** (central authority for `collapse`).

### 3.4 FASM-TYCOON Admit Gate ⇔ Spherepop Collapse

| FASM-TYCOON | Spherepop |
|-------------|-----------|
| `Record` (assemble + step) | `pop` + `bind` + `transform` |
| `Admit` (verify trace, cycles, MMIO) | `verify` (evidence + certificate) |
| `Commit` (emit sovereign binary) | `collapse` (assign to committed state) |

Both enforce: **no authority from generation alone**. Authority only from verified, authorized commitment.

---

## 4. New Artifacts to Create

### 4.1 `spec/schemas/percolation.json` — Shadow Graph Percolation Check

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://standardgalactic.github.io/spherepop-kernel/spec/schemas/percolation.json",
  "title": "Spherepop Shadow Graph Percolation Invariant",
  "description": "Molloy–Reed κ check on cross-boundary dependencies. From UltraGraph §1.2.",
  "type": "object",
  "required": ["graph_cid", "vertices", "shadow_edges", "kappa", "regime", "checked_at"],
  "properties": {
    "graph_cid": { "type": "string", "pattern": "^cid:[a-z0-9]+$" },
    "vertices": { "type": "integer", "minimum": 0 },
    "shadow_edges": { "type": "integer", "minimum": 0 },
    "mean_degree": { "type": "number", "minimum": 0 },
    "kappa": { "type": "number", "minimum": 0, "description": "⟨k²⟩/⟨k⟩" },
    "regime": { "type": "string", "enum": ["subcritical", "critical", "supercritical"] },
    "checked_at": { "type": "string", "format": "date-time" },
    "transitive_risk": { "type": "boolean", "description": "True if κ > 2" }
  }
}
```

### 4.2 `spec/schemas/lod.json` — Szemerédi LOD Compression

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://standardgalactic.github.io/spherepop-kernel/spec/schemas/lod.json",
  "title": "Spherepop Szemerédi LOD Super-Node",
  "description": "Szemerédi Regularity partition as Universe LOD Shader. From UltraGraph §2.",
  "type": "object",
  "required": ["partition_id", "epsilon", "super_nodes", "steiner_hub", "checked_at"],
  "properties": {
    "partition_id": { "type": "string", "pattern": "^cid:[a-z0-9]+$" },
    "epsilon": { "type": "number", "minimum": 0, "maximum": 1, "description": "ε-regularity parameter" },
    "super_nodes": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["sncp_id", "centroid", "weight", "partition_class"],
        "properties": {
          "sncp_id": { "type": "string", "pattern": "^cid:[a-z0-9]+$" },
          "centroid": { "type": "array", "items": { "type": "number" }, "minItems": 2, "maxItems": 2 },
          "weight": { "type": "number", "minimum": 0 },
          "partition_class": { "type": "integer", "minimum": 1, "maximum": 4, "description": "V1=mem8, V2=oracle, V3=simd, V4=capsule" }
        }
      }
    },
    "steiner_hub": { "type": "string", "pattern": "^cid:[a-z0-9]+$", "description": "Erdős–Steiner center Z(G)" },
    "checked_at": { "type": "string", "format": "date-time" }
  }
}
```

### 4.3 `spec/schemas/unitary.json` — WaveMemory Unitary Evolution

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://standardgalactic.github.io/spherepop-kernel/spec/schemas/unitary.json",
  "title": "Spherepop WaveMemory Unitary Step",
  "description": "Kohn–Sham / Strang-split unitary evolution. From UltraGraph §3.1.",
  "type": "object",
  "required": ["step_id", "hamiltonian_cid", "delta_t_q16", "half_step", "entanglement_coupling", "checked_at"],
  "properties": {
    "step_id": { "type": "string", "pattern": "^cid:[a-z0-9]+$" },
    "hamiltonian_cid": { "type": "string", "pattern": "^cid:[a-z0-9]+$" },
    "delta_t_q16": { "type": "integer", "description": "Δt in Q16.16 fixed-point" },
    "half_step": { "type": "integer", "enum": [1, 2], "description": "Strang half-step: 1=e⁻ⁱᴴ⁰Δᵗ/², 2=e⁻ⁱⱽᴷˢΔᵗ" },
    "entanglement_coupling": { "type": "number", "minimum": 0, "maximum": 1, "description": "P̂C (Shadow Entanglement Coupling), threshold 0.90" },
    "checked_at": { "type": "string", "format": "date-time" }
  }
}
```

---

## 5. Extended Conformance Vectors

Add to `spec/conformance/vectors.json`:

```json
{
  "vector_id": "percolation-001",
  "description": "Shadow graph κ < 2 (subcritical) → pop permitted",
  "config": { ... standard config ... },
  "start_term": "pop_sphere spec_001",
  "fuel": 10,
  "strategy": "lo",
  "expected": { "outcome": "normal_form", "normal_form": "bound_sphere_001", "steps": 3 },
  "percolation_check": { "kappa": 1.5, "regime": "subcritical", "permitted": true }
},
{
  "vector_id": "percolation-002",
  "description": "Shadow graph κ > 2 (supercritical) → refuse with PERCOLATION_RISK",
  "config": { ... },
  "start_term": "pop_sphere spec_risky",
  "fuel": 10,
  "strategy": "lo",
  "expected": { "outcome": "error", "error_code": "ErrPercolationRisk", "steps": 1 },
  "percolation_check": { "kappa": 2.3, "regime": "supercritical", "permitted": false }
},
{
  "vector_id": "lod-001",
  "description": "Nested sphere compression via Szemerédi LOD",
  "config": { ... },
  "start_term": "compose (pop_sphere a) (pop_sphere b)",
  "fuel": 20,
  "strategy": "lo",
  "expected": { "outcome": "normal_form", "steps": 5 },
  "lod_compression": { "epsilon": 0.1, "super_nodes": 2, "steiner_hub": "cid:..." }
},
{
  "vector_id": "unitary-001",
  "description": "WaveMemory unitary step matches Strang splitting",
  "config": { ... },
  "start_term": "unitary_step H dt",
  "fuel": 5,
  "strategy": "lo",
  "expected": { "outcome": "normal_form", "steps": 2 },
  "unitary_check": { "half_step": 1, "entanglement_coupling": 0.92 }
},
{
  "vector_id": "zero-float-001",
  "description": "Q16.16 arithmetic produces bit-exact result across ISA",
  "config": { ... },
  "start_term": "q16_mul 0x00010000 0x00020000",
  "fuel": 1,
  "strategy": "lo",
  "expected": { "outcome": "normal_form", "normal_form": "0x00020000", "steps": 1 }
}
```

---

## 6. Go Implementation Extensions

### 6.1 Percolation Checker (`spec/schemas/percolation.go`)

```go
package percolation

import (
	"errors"
	"github.com/standardgalactic/spherepop-kernel/term"
)

var ErrPercolationRisk = errors.New("percolation: κ > 2, transitive dependency risk")

// ShadowGraph represents cross-boundary dependencies (lut[u] ≠ lut[v])
type ShadowGraph struct {
	Vertices   int
	Edges      [][2]int // pairs of vertex indices
	LUT        map[int]string // vertex → package/module
}

// MolloyReedKappa computes κ = ⟨k²⟩/⟨k⟩ in O(|V| + |E|)
func (g *ShadowGraph) MolloyReedKappa() float64 {
	if g.Vertices == 0 || len(g.Edges) == 0 {
		return 0
	}
	deg := make([]int, g.Vertices)
	for _, e := range g.Edges {
		deg[e[0]]++
		deg[e[1]]++
	}
	var sumK, sumK2 float64
	for _, d := range deg {
		sumK += float64(d)
		sumK2 += float64(d * d)
	}
	if sumK == 0 {
		return 0
	}
	return sumK2 / sumK
}

// CheckCollapseGate returns ErrPercolationRisk if κ > 2
func CheckCollapseGate(g *ShadowGraph) error {
	kappa := g.MolloyReedKappa()
	if kappa > 2.0 {
		return ErrPercolationRisk
	}
	return nil
}

// BuildShadowGraphFromTerms extracts cross-boundary edges from spherepop terms
func BuildShadowGraphFromTerms(terms []term.Term, packageMap map[string]string) *ShadowGraph {
	// ... extract module references from term atoms/constants
	// build edges where packageMap[u] != packageMap[v]
}
```

### 6.2 LOD Compressor (`spec/schemas/lod.go`)

```go
package lod

import (
	"math"
	"github.com/standardgalactic/spherepop-kernel/term"
)

// SuperNodeCentroidPod represents [SNCP] + w
type SuperNodeCentroidPod struct {
	SNCPID       string
	Centroid     [2]float64 // (x, y) in embedding space
	Weight       float64
	PartitionCls int // 1=mem8, 2=oracle, 3=simd, 4=capsule
	Wavefield    []float64 // continuous wavefield coupling
}

// SzemerediPartition computes ε-regular partition
// Returns super-nodes and Erdős–Steiner hub Z(G)
func SzemerediPartition(terms []term.Term, epsilon float64) ([]SuperNodeCentroidPod, string, error) {
	// 1. Build adjacency from term references
	// 2. Apply Szemerédi regularity lemma (algorithmic version)
	// 3. Cluster into ≤ 4/ε² classes
	// 4. Compute centroids, weights, wavefields
	// 5. Find Steiner center Z(G) minimizing ∑ d(v, Z(G))
	// ...
}

// SteinerCenter finds Z(G) minimizing multi-terminal eccentricity
func SteinerCenter(pods []SuperNodeCentroidPod) string {
	// Geometric median / Fermat-Weber point
	// Return CID of hub
}
```

### 6.3 Unitary Stepper (`spec/schemas/unitary.go`)

```go
package unitary

import (
	"github.com/standardgalactic/spherepop-kernel/spec/schemas/footprint"
)

// Q16_16 is a 32-bit signed fixed-point (16.16)
type Q16_16 int32

const Q16_SHIFT = 16
const Q16_ONE = 1 << Q16_SHIFT

func Q16_Mul(a, b Q16_16) Q16_16 {
	return Q16_16((int64(a) * int64(b)) >> Q16_SHIFT)
}

func Q16_Add(a, b Q16_16) Q16_16 {
	return a + b // Wraparound in ℤ2³² is exact
}

// StrangHalfStep computes U(Δt/2) = e⁻ⁱᴴΔᵗ/² or e⁻ⁱⱽΔᵗ
// Using Kohn–Sham effective potential + discrete WaveMemory grid
func StrangHalfStep(
	psi []Q16_16,        // 64×64 = 4096 complex amplitudes (2×Q16_16)
	H []Q16_16,          // Hamiltonian matrix
	V []Q16_16,          // Kohn–Sham potential
	dt Q16_16,           // Δt in Q16.16
	halfStep int,        // 1 or 2
) []Q16_16 {
	// e⁻ⁱᴴΔᵗ ≈ I - iHΔt - ½H²Δt² + ... (truncated to O(Δt³))
	// Implemented via Q16.16 matrix-vector multiply
	// Uses felix_dot64_f32 SIMD kernel (6-cycle) for inner products
}

// ShadowEntanglementCoupling computes P̂C from Flyxion asymmetry
func ShadowEntanglementCoupling(forwardDeg, reverseDeg int) float64 {
	if forwardDeg == 0 && reverseDeg == 0 {
		return 0
	}
	// P̂C = |dT - dR| / (dT + dR) for directional asymmetry
	return math.Abs(float64(forwardDeg-reverseDeg)) / float64(forwardDeg+reverseDeg)
}
```

---

## 7. FASM-TYCOON Integration Points

### 7.1 `isa_own_32_64.inc` → Spherepop Term Encoding

The FASM-TYCOON `q16_mul`, `swar_popcnt4` macros compile to deterministic integer instructions. These become **Spherepop constants** in `term.go`:

```go
// term/constants.go
const (
    ConstQ16Mul  = "Q16MUL"  // Q16.16 multiplication
    ConstQ16Add  = "Q16ADD"  // Q16.16 addition (mod 2³²)
    ConstPopcnt4 = "POP4"    // SWAR 4×8-bit popcount
    ConstDot64   = "DOT64"   // felix_dot64_f32 inner product
)
```

### 7.2 `park_sandbox.rs` → Spherepop Scheduler

The `ParkSandbox::record_trace()` + `StepReceipt` maps to Spherepop `sched.Slice`:

```go
// sched/slice.go
type Slice struct {
	EntityID    string
	FuelBudget  int
	StartTerm   term.Term
	EndTerm     term.Term
	Steps       []Step
	MMIOEvents  []MMIOEvent
	CyclesUsed  int
	Admitted    bool
	TrapReason  string
}
```

### 7.3 `.m8a` Context → Spherepop Trace Compression

The `.m8a` frame (~120 tokens) is a **compressed trace**. Spherepop can emit `.m8a` from `trace.Trace`:

```go
// trace/m8a.go
func (t *Trace) ToM8A() []byte {
	// Compress hash chain + steps into hyper-dense binary frame
	// [TARGET: ISA | BUDGET: fuel | ARENA_MAP | ABI_CONTRACT | LAST_DIFF]
}
```

---

## 8. Unified Build & Test Pipeline

```bash
# 1. Build all four verifiers (Go, Zig, Rust, Idris)
go test ./...                    # Go reference kernel
zig test verifiers/zig/...       # Zig bit-exact verifier
cargo test --manifest-path verifiers/rust/Cargo.toml
idris2 --check verifiers/idris/Spherepop.idr

# 2. Run conformance vectors (including new UltraGraph vectors)
go test ./spec/conformance/... -v

# 3. FASM-TYCOON sovereign binary build
cd aye-asm && ./build_omniverse.sh
# Produces: aye_esp32c6_iram.bin (108 bytes), aye_sovereign_aarch64.elf (220 bytes)

# 4. Cross-verification: FASM-TYCOON stepper runs Spherepop terms
#    ParkSandbox executes compiled spherepop bytecode, produces trace
#    Go/Zig/Rust/Idris verifiers check the trace

# 5. UltraGraph diagnostics
smart-tree --erdos-anomaly ./    # κ check on codebase
# If κ > 2: block commit via Spherepop RefusalReason.PERCOLATION_RISK
```

---

## 9. Sovereign Manifesto Footer (Embedded in All Binaries)

```
[8b.is // Information is freedom, data is sovereign, free will, peace, love, unity]
```

This footer appears in:
- Spherepop trace `head` anchor
- FASM-TYCOON `SOVEREIGN_FOOTER` in every `.bin`/`.elf`
- UltraGraph `Steiner Hub Z(G)` commitment root

---

## 10. Next Actions

| Priority | Action | Owner |
|----------|--------|-------|
| P0 | Create `percolation.json`, `lod.json`, `unitary.json` schemas | ✓ This doc |
| P0 | Add 5 UltraGraph conformance vectors to `vectors.json` | Next |
| P1 | Implement `percolation.go`, `lod.go`, `unitary.go` in Go kernel | Next |
| P1 | Port `q16_mul`, `felix_dot64_f32` to Zig/Rust/Idris verifiers | Next |
| P2 | Build `smart-tree --erdos-anomaly` integration in CI | Next |
| P2 | FASM-TYCOON `park_sandbox` runs Spherepop conformance vectors | Next |
| P3 | Unified `.m8a` trace emission from all 4 verifiers | Later |

---

*Unified by the axiom: **Ahogy a dolgok vannak** — "As things are."*
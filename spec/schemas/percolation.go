// Package schemas implements the zero-float reference models for Spherepop P0
// specification schemas: maintenance budget, environment footprint, governance
// amendments, Molloy–Reed shadow-graph percolation, Szemerédi LOD partitions,
// and Kohn–Sham / Strang-split WaveMemory unitary evolution.
//
// Zero-Float p/q Invariant: no floating-point types or operations appear in
// state coordinates, ratios, accumulators, or gate predicates. All rationals
// are represented in canonical reduced form p/q (gcd(p, q) = 1, q >= 1).
package schemas

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/bits"
	"sort"
	"strconv"
	"time"

	"github.com/standardgalactic/spherepop-kernel/term"
)

var (
	ErrZeroDenominator = errors.New("schemas: zero denominator in rational p/q")
	ErrPercolationRisk = errors.New("percolation: kappa > 2, transitive dependency risk across boundaries")
	ErrInvalidGraph    = errors.New("percolation: invalid shadow graph")
)

// Ratio is an exact canonical reduced rational p/q with P >= 0 and Q >= 1.
type Ratio struct {
	P uint64 `json:"p"`
	Q uint64 `json:"q"`
}

// NewRatio constructs a canonical reduced rational p/q.
func NewRatio(p, q uint64) (Ratio, error) {
	if q == 0 {
		return Ratio{}, ErrZeroDenominator
	}
	return MustRatio(p, q), nil
}

// MustRatio constructs a canonical reduced rational p/q, panicking if q == 0.
func MustRatio(p, q uint64) Ratio {
	if q == 0 {
		panic("schemas: zero denominator")
	}
	if p == 0 {
		return Ratio{P: 0, Q: 1}
	}
	g := gcd64(p, q)
	return Ratio{P: p / g, Q: q / g}
}

// IsZero reports whether r == 0/1.
func (r Ratio) IsZero() bool {
	return r.P == 0
}

// Valid reports whether r has a non-zero denominator and is reduced.
func (r Ratio) Valid() bool {
	if r.Q == 0 {
		return false
	}
	if r.P == 0 {
		return r.Q == 1
	}
	return gcd64(r.P, r.Q) == 1
}

// Canonical normalizes an unreduced Ratio (defaulting zero value to 0/1).
func (r Ratio) Canonical() (Ratio, error) {
	if r.P == 0 && r.Q == 0 {
		return Ratio{P: 0, Q: 1}, nil
	}
	return NewRatio(r.P, r.Q)
}

// Cmp compares r and other using exact 128-bit cross-multiplication.
// Returns -1 if r < other, 0 if r == other, +1 if r > other.
func (r Ratio) Cmp(other Ratio) int {
	lhsHi, lhsLo := bits.Mul64(r.P, other.Q)
	rhsHi, rhsLo := bits.Mul64(other.P, r.Q)
	if lhsHi != rhsHi {
		if lhsHi < rhsHi {
			return -1
		}
		return 1
	}
	if lhsLo != rhsLo {
		if lhsLo < rhsLo {
			return -1
		}
		return 1
	}
	return 0
}

// IsBonoboResonance reports whether r equals Rung 0 Bonobo Target Resonance (3/2).
func (r Ratio) IsBonoboResonance() bool {
	c, err := r.Canonical()
	return err == nil && c.P == 3 && c.Q == 2
}

// String formats r as "p/q".
func (r Ratio) String() string {
	return strconv.FormatUint(r.P, 10) + "/" + strconv.FormatUint(r.Q, 10)
}

func gcd64(a, b uint64) uint64 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// computeCID computes a deterministic SHA-256 content-addressed identifier
// matching the schema pattern ^cid:[a-z0-9]+$.
func computeCID(domain string, parts ...[]byte) string {
	h := sha256.New()
	h.Write([]byte(domain))
	h.Write([]byte{0})
	for _, p := range parts {
		h.Write(p)
		h.Write([]byte{0})
	}
	return "cid:" + hex.EncodeToString(h.Sum(nil))
}

// ShadowGraph represents cross-boundary dependencies where lut[u] != lut[v].
// Vertices are modules/packages; edges are cross-package references.
type ShadowGraph struct {
	GraphCID        string         `json:"graph_cid"`
	Vertices        int            `json:"vertices"`
	Edges           [][2]int       `json:"-"`
	ShadowEdges     int            `json:"shadow_edges"`
	LUT             map[int]string `json:"-"`
	VertexLabels    map[int]string `json:"-"`
	CheckedAt       time.Time      `json:"checked_at"`
	KappaRatio      Ratio          `json:"kappa_ratio"`
	MeanDegreeRatio Ratio          `json:"mean_degree_ratio"`
	ResonanceTrit   int8           `json:"resonance_trit"`
	Regime          string         `json:"regime"`
	TransitiveRisk  bool           `json:"transitive_risk"`
}

// MolloyReedKappa computes exact reduced kappa = (sum d_v^2) / (sum d_v) in O(|V| + |E|).
// No floating-point operations are performed.
func (g *ShadowGraph) MolloyReedKappa() Ratio {
	canonicalEdges := g.canonicalEdges()
	g.ShadowEdges = len(canonicalEdges)
	g.GraphCID = g.computeGraphCID(canonicalEdges)
	if g.CheckedAt.IsZero() {
		g.CheckedAt = time.Now().UTC()
	}

	if g.Vertices <= 0 || len(canonicalEdges) == 0 {
		g.KappaRatio = Ratio{P: 0, Q: 1}
		g.MeanDegreeRatio = Ratio{P: 0, Q: 1}
		g.ResonanceTrit = 0
		g.Regime = "subcritical"
		g.TransitiveRisk = false
		return g.KappaRatio
	}

	deg := make([]uint64, g.Vertices)
	for _, e := range canonicalEdges {
		if e[0] >= 0 && e[0] < g.Vertices && e[1] >= 0 && e[1] < g.Vertices && e[0] != e[1] {
			deg[e[0]]++
			deg[e[1]]++
		}
	}

	var sumK, sumK2 uint64
	for _, d := range deg {
		sumK += d
		sumK2 += d * d
	}

	g.MeanDegreeRatio = MustRatio(sumK, uint64(g.Vertices))
	if sumK == 0 {
		g.KappaRatio = Ratio{P: 0, Q: 1}
	} else {
		g.KappaRatio = MustRatio(sumK2, sumK)
	}

	criticalThreshold := Ratio{P: 2, Q: 1}
	switch g.KappaRatio.Cmp(criticalThreshold) {
	case -1:
		g.Regime = "subcritical"
		g.TransitiveRisk = false
		if g.KappaRatio.IsBonoboResonance() {
			g.ResonanceTrit = 1 // Rung 0 Bonobo resonance (p/q = 3/2)
		} else {
			g.ResonanceTrit = 0
		}
	case 0:
		g.Regime = "critical"
		g.TransitiveRisk = false
		g.ResonanceTrit = 0
	default:
		g.Regime = "supercritical"
		g.TransitiveRisk = true
		g.ResonanceTrit = -1
	}

	return g.KappaRatio
}

// CheckCollapseGate validates the shadow graph and returns ErrPercolationRisk
// iff kappa > 2/1 (supercritical giant component regime).
func (g *ShadowGraph) CheckCollapseGate() error {
	if err := g.Validate(); err != nil {
		return err
	}
	g.MolloyReedKappa()
	if g.TransitiveRisk {
		return ErrPercolationRisk
	}
	return nil
}

// Validate checks vertex count, bounds, and absence of invalid endpoints.
func (g *ShadowGraph) Validate() error {
	if g == nil || g.Vertices < 0 {
		return ErrInvalidGraph
	}
	for _, e := range g.Edges {
		if e[0] < 0 || e[0] >= g.Vertices || e[1] < 0 || e[1] >= g.Vertices || e[0] == e[1] {
			return ErrInvalidGraph
		}
	}
	return nil
}

func (g *ShadowGraph) canonicalEdges() [][2]int {
	if g == nil || len(g.Edges) == 0 {
		return nil
	}
	seen := make(map[[2]int]struct{}, len(g.Edges))
	out := make([][2]int, 0, len(g.Edges))
	for _, e := range g.Edges {
		u, v := e[0], e[1]
		if u == v {
			continue
		}
		if u > v {
			u, v = v, u
		}
		pair := [2]int{u, v}
		if _, dup := seen[pair]; !dup {
			seen[pair] = struct{}{}
			out = append(out, pair)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i][0] != out[j][0] {
			return out[i][0] < out[j][0]
		}
		return out[i][1] < out[j][1]
	})
	return out
}

func (g *ShadowGraph) computeGraphCID(canonical [][2]int) string {
	buf := []byte(fmt.Sprintf("V=%d;", g.Vertices))
	for _, e := range canonical {
		buf = append(buf, []byte(fmt.Sprintf("%d-%d,", e[0], e[1]))...)
	}
	return computeCID("spherepop-percolation/1", buf)
}

// BuildShadowGraphFromTerms extracts cross-boundary shadow edges from atom names.
func BuildShadowGraphFromTerms(termAtoms []string, packageMap map[string]string) (*ShadowGraph, error) {
	var uniquePkgs []string
	pkgSeen := make(map[string]bool)
	for _, atom := range termAtoms {
		if pkg, ok := packageMap[atom]; ok && !pkgSeen[pkg] {
			pkgSeen[pkg] = true
			uniquePkgs = append(uniquePkgs, pkg)
		}
	}
	sort.Strings(uniquePkgs)

	pkgToVertex := make(map[string]int, len(uniquePkgs))
	lut := make(map[int]string, len(uniquePkgs))
	labels := make(map[int]string, len(uniquePkgs))
	for idx, pkg := range uniquePkgs {
		pkgToVertex[pkg] = idx
		lut[idx] = pkg
		labels[idx] = pkg
	}

	var edges [][2]int
	for i := 0; i < len(termAtoms); i++ {
		for j := i + 1; j < len(termAtoms); j++ {
			pkgA, okA := packageMap[termAtoms[i]]
			pkgB, okB := packageMap[termAtoms[j]]
			if okA && okB && pkgA != pkgB {
				u, v := pkgToVertex[pkgA], pkgToVertex[pkgB]
				if u > v {
					u, v = v, u
				}
				edges = append(edges, [2]int{u, v})
			}
		}
	}

	g := &ShadowGraph{
		Vertices:     len(uniquePkgs),
		Edges:        edges,
		LUT:          lut,
		VertexLabels: labels,
		CheckedAt:    time.Now().UTC(),
	}
	g.Edges = g.canonicalEdges()
	g.MolloyReedKappa()
	return g, nil
}

// BuildShadowGraphFromTerm extracts atom leaves directly from a parsed Spherepop Term.
func BuildShadowGraphFromTerm(t *term.Term, packageMap map[string]string) (*ShadowGraph, error) {
	var atoms []string
	collectAtoms(t, &atoms)
	return BuildShadowGraphFromTerms(atoms, packageMap)
}

func collectAtoms(t *term.Term, out *[]string) {
	if t == nil {
		return
	}
	switch t.Kind() {
	case term.KindAtom:
		*out = append(*out, t.Name())
	case term.KindApp:
		collectAtoms(t.Fun(), out)
		collectAtoms(t.Arg(), out)
	case term.KindBracket:
		collectAtoms(t.Body(), out)
	}
}

// ToJSON serializes the shadow graph document matching percolation.json.
func (g *ShadowGraph) ToJSON() ([]byte, error) {
	if err := g.Validate(); err != nil {
		return nil, err
	}
	g.MolloyReedKappa()
	return json.Marshal(g)
}

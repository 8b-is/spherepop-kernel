// Percolation checker for Spherepop kernel
// Implements Molloy–Reed κ = ⟨k²⟩/⟨k⟩ on cross-boundary shadow graph.
// From UltraGraph §1.2: if κ > 2, giant transitive component exists → PERCOLATION_RISK refusal.

package percolation

import (
	"errors"
	"time"
)

var (
	ErrPercolationRisk = errors.New("percolation: κ > 2, transitive dependency risk across boundaries")
	ErrInvalidGraph    = errors.New("percolation: invalid shadow graph")
)

// ShadowGraph represents cross-boundary dependencies where lut[u] ≠ lut[v]
// Vertices are modules/packages; edges are implicit cross-package references.
type ShadowGraph struct {
	GraphCID      string            `json:"graph_cid"`       // CID of this graph
	Vertices      int               `json:"vertices"`        // |V|
	Edges         [][2]int          `json:"edges"`           // pairs of vertex indices
	LUT           map[int]string    `json:"lut"`             // vertex → package/module
	VertexLabels  map[int]string    `json:"vertex_labels"`   // vertex → human label
	ComputedAt    time.Time         `json:"computed_at"`
	Kappa         float64           `json:"kappa"`           // ⟨k²⟩/⟨k⟩
	MeanDegree    float64           `json:"mean_degree"`     // c = 2|E|/|V|
	Regime        string            `json:"regime"`          // subcritical, critical, supercritical
	TransitiveRisk bool             `json:"transitive_risk"` // κ > 2
}

// MolloyReedKappa computes κ = ⟨k²⟩/⟨k⟩ in O(|V| + |E|)
// Returns 0 if graph is empty.
func (g *ShadowGraph) MolloyReedKappa() float64 {
	if g.Vertices == 0 || len(g.Edges) == 0 {
		g.Kappa = 0
		g.MeanDegree = 0
		g.Regime = "subcritical"
		g.TransitiveRisk = false
		return 0
	}

	deg := make([]int, g.Vertices)
	for _, e := range g.Edges {
		if e[0] >= 0 && e[0] < g.Vertices {
			deg[e[0]]++
		}
		if e[1] >= 0 && e[1] < g.Vertices {
			deg[e[1]]++
		}
	}

	var sumK, sumK2 float64
	for _, d := range deg {
		sumK += float64(d)
		sumK2 += float64(d * d)
	}

	if sumK == 0 {
		g.Kappa = 0
	} else {
		g.Kappa = sumK2 / sumK
	}
	g.MeanDegree = sumK / float64(g.Vertices)

	switch {
	case g.MeanDegree < 1.0 && g.Kappa < 2.0:
		g.Regime = "subcritical"
	case g.MeanDegree >= 1.0 && g.MeanDegree <= 1.0 && g.Kappa >= 1.9 && g.Kappa <= 2.1:
		g.Regime = "critical"
	default:
		g.Regime = "supercritical"
	}

	g.TransitiveRisk = g.Kappa > 2.0
	g.ComputedAt = time.Now().UTC()

	return g.Kappa
}

// CheckCollapseGate returns ErrPercolationRisk if κ > 2 (supercritical)
// This is the kernel gate: no collapse permitted when transitive risk exists
// unless a governance waiver is present (handled by budget package).
func (g *ShadowGraph) CheckCollapseGate() error {
	if g.Kappa == 0 && len(g.Edges) > 0 {
		g.MolloyReedKappa()
	}
	if g.TransitiveRisk {
		return ErrPercolationRisk
	}
	return nil
}

// Validate checks graph consistency
func (g *ShadowGraph) Validate() error {
	if g.Vertices < 0 {
		return ErrInvalidGraph
	}
	for _, e := range g.Edges {
		if e[0] < 0 || e[0] >= g.Vertices || e[1] < 0 || e[1] >= g.Vertices {
			return ErrInvalidGraph
		}
	}
	return nil
}

// BuildShadowGraphFromTerms extracts cross-boundary edges from spherepop terms
// packageMap maps term atom names to their package/module
func BuildShadowGraphFromTerms(termAtoms []string, packageMap map[string]string) (*ShadowGraph, error) {
	// Map each unique package to a vertex index
	pkgToVertex := make(map[string]int)
	vertexToPkg := make(map[int]string)
	vertexIdx := 0

	// Collect all packages referenced
	for _, atom := range termAtoms {
		if pkg, ok := packageMap[atom]; ok {
			if _, exists := pkgToVertex[pkg]; !exists {
				pkgToVertex[pkg] = vertexIdx
				vertexToPkg[vertexIdx] = pkg
				vertexIdx++
			}
		}
	}

	// Build edges: for each pair of atoms in same term, if different packages → shadow edge
	edgeSet := make(map[[2]int]bool)
	for i, a := range termAtoms {
		for j, b := range termAtoms {
			if i >= j {
				continue
			}
			pkgA, okA := packageMap[a]
			pkgB, okB := packageMap[b]
			if okA && okB && pkgA != pkgB {
				vA := pkgToVertex[pkgA]
				vB := pkgToVertex[pkgB]
				// Canonical order
				if vA > vB {
					vA, vB = vB, vA
				}
				edgeSet[[2]int{vA, vB}] = true
			}
		}
	}

	edges := make([][2]int, 0, len(edgeSet))
	for e := range edgeSet {
		edges = append(edges, e)
	}

	g := &ShadowGraph{
		Vertices:      vertexIdx,
		Edges:         edges,
		LUT:           make(map[int]string, vertexIdx),
		VertexLabels:  make(map[int]string, vertexIdx),
	}
	for v, pkg := range vertexToPkg {
		g.LUT[v] = pkg
		g.VertexLabels[v] = pkg
	}

	g.MolloyReedKappa()
	return g, nil
}

// ToJSON serializes the graph with computed invariants
func (g *ShadowGraph) ToJSON() ([]byte, error) {
	// Would use encoding/json; simplified here
	return nil, nil
}
// Szemerédi Regularity LOD Compressor for Spherepop kernel (UltraGraph §2).
// Partitions G_explicit ∪ G_SHADOW into deterministic super-node pods [SNCP]+w
// and selects the Erdős–Steiner 1-median routing hub Z(G) using strictly
// zero-float Q16.16 and reduced rational p/q arithmetic.

package schemas

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/bits"
	"sort"
	"time"
)

var (
	ErrInvalidPartition = errors.New("lod: invalid partition")
	ErrEmptyGraph       = errors.New("lod: empty graph")
)

// Partition represents a deterministic Szemerédi ε-regular partition.
type Partition struct {
	PartitionID  string      `json:"partition_id"`
	EpsilonRatio Ratio       `json:"epsilon_ratio"`
	SuperNodes   []SuperNode `json:"super_nodes"`
	SteinerHub   string      `json:"steiner_hub"`
	CheckedAt    time.Time   `json:"checked_at"`
	VertexCount  int         `json:"vertex_count"`
	EdgeCount    int         `json:"edge_count"`
}

// SuperNode represents a Super Node Centroid Pod [SNCP] + w.
type SuperNode struct {
	SNCPID         string    `json:"sncp_id"`
	CentroidQ16    [2]Q16_16 `json:"centroid_q16"`
	WeightRatio    Ratio     `json:"weight_ratio"`
	PartitionClass int       `json:"partition_class"` // 1=mem8, 2=oracle, 3=simd, 4=capsule
	Vertices       []int     `json:"vertices"`
	WavefieldQ16   []Q16_16  `json:"wavefield_q16,omitempty"`
}

// SzemerediPartition computes a deterministic ε-regular super-node partition
// and Erdős–Steiner hub Z(G) using exact integer / rational arithmetic.
func SzemerediPartition(adjacency [][]int, vertexLabels []string, epsilon Ratio) (*Partition, error) {
	n := len(adjacency)
	if n == 0 {
		return nil, ErrEmptyGraph
	}
	for u, nbrs := range adjacency {
		if u < 0 {
			return nil, ErrInvalidPartition
		}
		for _, v := range nbrs {
			if v < 0 || v >= n {
				return nil, ErrInvalidPartition
			}
		}
	}

	eps, err := epsilon.Canonical()
	if err != nil || eps.P == 0 || eps.Cmp(Ratio{P: 1, Q: 1}) > 0 {
		eps = Ratio{P: 1, Q: 10} // default 1/10
	}

	// Upper bound on partition classes: min(n, floor(4 * q^2 / p^2)), at least 1.
	num4Q2Hi, num4Q2Lo := bits.Mul64(4, eps.Q*eps.Q)
	var maxClasses int
	if num4Q2Hi > 0 {
		maxClasses = n
	} else {
		p2 := eps.P * eps.P
		bound := num4Q2Lo / p2
		if bound == 0 {
			bound = 1
		}
		if bound > uint64(n) {
			maxClasses = n
		} else {
			maxClasses = int(bound)
		}
	}

	adjMatrix := make([][]bool, n)
	for u := range adjacency {
		adjMatrix[u] = make([]bool, n)
		for _, v := range adjacency[u] {
			if u != v {
				adjMatrix[u][v] = true
			}
		}
	}
	for u := 0; u < n; u++ {
		for v := u + 1; v < n; v++ {
			if adjMatrix[u][v] || adjMatrix[v][u] {
				adjMatrix[u][v] = true
				adjMatrix[v][u] = true
			}
		}
	}

	// Start from connected components (or single initial class if connected),
	// then iteratively refine ε-irregular degree witnesses up to maxClasses.
	groups := initialConnectedPartition(adjMatrix)
	groups = refineIrregularClasses(adjMatrix, groups, eps, maxClasses)

	superNodes := buildSuperNodes(adjMatrix, groups, vertexLabels)
	steinerHub := computeSteinerHub(adjMatrix, superNodes)
	partitionID := generatePartitionCID(adjMatrix, eps, superNodes)

	return &Partition{
		PartitionID:  partitionID,
		EpsilonRatio: eps,
		SuperNodes:   superNodes,
		SteinerHub:   steinerHub,
		CheckedAt:    time.Now().UTC(),
		VertexCount:  n,
		EdgeCount:    countUndirectedEdges(adjMatrix),
	}, nil
}

func initialConnectedPartition(adj [][]bool) [][]int {
	n := len(adj)
	visited := make([]bool, n)
	var comps [][]int
	for start := 0; start < n; start++ {
		if visited[start] {
			continue
		}
		queue := []int{start}
		visited[start] = true
		var comp []int
		for len(queue) > 0 {
			u := queue[0]
			queue = queue[1:]
			comp = append(comp, u)
			for v := 0; v < n; v++ {
				if adj[u][v] && !visited[v] {
					visited[v] = true
					queue = append(queue, v)
				}
			}
		}
		sort.Ints(comp)
		comps = append(comps, comp)
	}
	// If the whole graph is 1 connected component with >= 2 vertices, bisect by
	// degree rank so cross-pod density refinement has at least 2 pods to inspect.
	if len(comps) == 1 && len(comps[0]) >= 2 {
		verts := append([]int(nil), comps[0]...)
		sort.Slice(verts, func(i, j int) bool {
			di, dj := vertexDegree(adj, verts[i]), vertexDegree(adj, verts[j])
			if di != dj {
				return di < dj
			}
			return verts[i] < verts[j]
		})
		mid := len(verts) / 2
		left := append([]int(nil), verts[:mid]...)
		right := append([]int(nil), verts[mid:]...)
		sort.Ints(left)
		sort.Ints(right)
		return [][]int{left, right}
	}
	return comps
}

func refineIrregularClasses(adj [][]bool, groups [][]int, eps Ratio, maxClasses int) [][]int {
	for len(groups) < maxClasses {
		splitIdx := -1
		var lowSet, highSet []int

		for i := 0; i < len(groups) && splitIdx < 0; i++ {
			a := groups[i]
			if len(a) < 4 {
				continue
			}
			for j := 0; j < len(groups); j++ {
				if i == j || len(groups[j]) == 0 {
					continue
				}
				b := groups[j]
				// Bipartite density d(A, B) = |E(A,B)| / (|A|*|B|)
				totalEdges := bipartiteEdges(adj, a, b)
				denom := uint64(len(a) * len(b))
				baseDensity := MustRatio(totalEdges, denom)

				// Partition A into vertices below vs at/above mean degree into B.
				var lo, hi []int
				for _, u := range a {
					degB := uint64(edgesToSet(adj, u, b))
					// Compare degB / |B| vs totalEdges / (|A|*|B|) <=> degB * |A| vs totalEdges
					if degB*uint64(len(a)) < totalEdges {
						lo = append(lo, u)
					} else {
						hi = append(hi, u)
					}
				}
				if len(lo) == 0 || len(hi) == 0 {
					continue
				}
				hiDensity := MustRatio(bipartiteEdges(adj, hi, b), uint64(len(hi)*len(b)))
				if absRatioDiff(hiDensity, baseDensity).Cmp(eps) > 0 {
					splitIdx = i
					lowSet = lo
					highSet = hi
					break
				}
			}
		}

		if splitIdx < 0 {
			break
		}
		sort.Ints(lowSet)
		sort.Ints(highSet)
		next := make([][]int, 0, len(groups)+1)
		next = append(next, groups[:splitIdx]...)
		next = append(next, lowSet, highSet)
		next = append(next, groups[splitIdx+1:]...)
		groups = next
	}

	sort.Slice(groups, func(i, j int) bool {
		return groups[i][0] < groups[j][0]
	})
	return groups
}

func absRatioDiff(a, b Ratio) Ratio {
	lhs := a.P * b.Q
	rhs := b.P * a.Q
	var diff uint64
	if lhs >= rhs {
		diff = lhs - rhs
	} else {
		diff = rhs - lhs
	}
	return MustRatio(diff, a.Q*b.Q)
}

func bipartiteEdges(adj [][]bool, a, b []int) uint64 {
	var c uint64
	for _, u := range a {
		for _, v := range b {
			if adj[u][v] {
				c++
			}
		}
	}
	return c
}

func edgesToSet(adj [][]bool, u int, b []int) int {
	c := 0
	for _, v := range b {
		if adj[u][v] {
			c++
		}
	}
	return c
}

func vertexDegree(adj [][]bool, u int) int {
	c := 0
	for _, ok := range adj[u] {
		if ok {
			c++
		}
	}
	return c
}

func buildSuperNodes(adj [][]bool, groups [][]int, labels []string) []SuperNode {
	superNodes := make([]SuperNode, 0, len(groups))
	for classID, vertices := range groups {
		if len(vertices) == 0 {
			continue
		}
		centroid := computeCentroidQ16(adj, vertices)
		weight := computeWeightRatio(adj, vertices)
		partitionClass := (classID % 4) + 1
		wavefield := computeWavefieldQ16(vertices, centroid)
		sncpID := generateSNCPID(vertices, labels)

		superNodes = append(superNodes, SuperNode{
			SNCPID:         sncpID,
			CentroidQ16:    centroid,
			WeightRatio:    weight,
			PartitionClass: partitionClass,
			Vertices:       vertices,
			WavefieldQ16:   wavefield,
		})
	}
	return superNodes
}

func computeCentroidQ16(adj [][]bool, vertices []int) [2]Q16_16 {
	if len(vertices) == 0 {
		return [2]Q16_16{0, 0}
	}
	var sumDegQ16, sumNbrDegQ16 int64
	for _, v := range vertices {
		deg := vertexDegree(adj, v)
		sumDegQ16 += int64(deg) << Q16_SHIFT
		if deg > 0 {
			nbrSum := 0
			for nb, ok := range adj[v] {
				if ok {
					nbrSum += vertexDegree(adj, nb)
				}
			}
			sumNbrDegQ16 += (int64(nbrSum) << Q16_SHIFT) / int64(deg)
		}
	}
	cnt := int64(len(vertices))
	return [2]Q16_16{
		Q16_16(sumDegQ16 / cnt),
		Q16_16(sumNbrDegQ16 / cnt),
	}
}

func computeWeightRatio(adj [][]bool, vertices []int) Ratio {
	k := len(vertices)
	if k < 2 {
		return Ratio{P: 1, Q: 1}
	}
	var internalEdges uint64
	for i := 0; i < k; i++ {
		for j := i + 1; j < k; j++ {
			if adj[vertices[i]][vertices[j]] {
				internalEdges++
			}
		}
	}
	maxPossible := uint64(k*(k-1)) / 2
	return MustRatio(internalEdges, maxPossible)
}

// computeWavefieldQ16 computes a deterministic 16-tap rational Padé coupling
// envelope Q16_ONE / (1 + |i - x0|) in Q16.16 with zero floating-point math.
func computeWavefieldQ16(vertices []int, centroid [2]Q16_16) []Q16_16 {
	const taps = 16
	wavefield := make([]Q16_16, taps)
	centerIdx := int(centroid[0] >> Q16_SHIFT)
	if centerIdx < 0 {
		centerIdx = -centerIdx
	}
	centerIdx = (centerIdx + len(vertices)) % taps
	for i := 0; i < taps; i++ {
		dist := i - centerIdx
		if dist < 0 {
			dist = -dist
		}
		// Exact integer reciprocal 1 / (1 + dist) in Q16.16.
		wavefield[i] = Q16_16(int64(Q16_ONE) / int64(1+dist))
	}
	return wavefield
}

// computeSteinerHub selects the Erdős–Steiner 1-median pod Z(G) minimizing
// shortest-path hop distance across the quotient super-node graph (breaking ties
// deterministically by L1 Q16.16 centroid distance and index).
func computeSteinerHub(adj [][]bool, superNodes []SuperNode) string {
	m := len(superNodes)
	if m == 0 {
		return computeCID("spherepop-steiner-hub/1", []byte("empty"))
	}
	qAdj := make([][]bool, m)
	for i := range qAdj {
		qAdj[i] = make([]bool, m)
	}
	for i := 0; i < m; i++ {
		for j := i + 1; j < m; j++ {
			if bipartiteEdges(adj, superNodes[i].Vertices, superNodes[j].Vertices) > 0 {
				qAdj[i][j] = true
				qAdj[j][i] = true
			}
		}
	}

	bestIdx := 0
	bestScore := int64(1<<62 - 1)
	for src := 0; src < m; src++ {
		dist := make([]int, m)
		for k := range dist {
			dist[k] = m + 1
		}
		dist[src] = 0
		queue := []int{src}
		for len(queue) > 0 {
			u := queue[0]
			queue = queue[1:]
			for v := 0; v < m; v++ {
				if qAdj[u][v] && dist[v] > dist[u]+1 {
					dist[v] = dist[u] + 1
					queue = append(queue, v)
				}
			}
		}
		var score int64
		for dst := 0; dst < m; dst++ {
			dx := int64(superNodes[src].CentroidQ16[0]) - int64(superNodes[dst].CentroidQ16[0])
			if dx < 0 {
				dx = -dx
			}
			dy := int64(superNodes[src].CentroidQ16[1]) - int64(superNodes[dst].CentroidQ16[1])
			if dy < 0 {
				dy = -dy
			}
			score += (int64(dist[dst]) << 20) + ((dx + dy) >> 8)
		}
		if score < bestScore {
			bestScore = score
			bestIdx = src
		}
	}

	return computeCID(
		"spherepop-steiner-hub/1",
		[]byte(superNodes[bestIdx].SNCPID),
		[]byte(fmt.Sprintf("idx=%d;m=%d", bestIdx, m)),
	)
}

func countUndirectedEdges(adj [][]bool) int {
	count := 0
	for u := 0; u < len(adj); u++ {
		for v := u + 1; v < len(adj); v++ {
			if adj[u][v] {
				count++
			}
		}
	}
	return count
}

func generateSNCPID(vertices []int, labels []string) string {
	var buf []byte
	for _, v := range vertices {
		lbl := ""
		if v >= 0 && v < len(labels) {
			lbl = labels[v]
		}
		buf = append(buf, []byte(fmt.Sprintf("%d:%s;", v, lbl))...)
	}
	return computeCID("spherepop-sncp/1", buf)
}

func generatePartitionCID(adj [][]bool, eps Ratio, superNodes []SuperNode) string {
	buf := []byte(fmt.Sprintf("n=%d;eps=%s;", len(adj), eps.String()))
	for _, sn := range superNodes {
		buf = append(buf, []byte(sn.SNCPID)...)
		buf = append(buf, ';')
	}
	return computeCID("spherepop-lod/1", buf)
}

// ToJSON serializes the partition matching lod.json.
func (p *Partition) ToJSON() ([]byte, error) {
	if p == nil {
		return nil, ErrInvalidPartition
	}
	return json.Marshal(p)
}

// ParsePartition deserializes and validates a Partition JSON document.
func ParsePartition(data []byte) (*Partition, error) {
	var p Partition
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	if p.VertexCount <= 0 || p.EpsilonRatio.Q == 0 {
		return nil, ErrInvalidPartition
	}
	return &p, nil
}

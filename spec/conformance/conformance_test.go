// Conformance test runner for Spherepop kernel and UltraGraph P0 specifications.
// Validates the Go reference kernel, trace verifier, UltraGraph percolation/LOD/unitary
// gates, JSON schemas, and Zero-Float p/q AST invariant.
//
// Run from module root: go test -v ./spec/...

package conformance

import (
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/standardgalactic/spherepop-kernel/reduce"
	"github.com/standardgalactic/spherepop-kernel/spec/authorization"
	"github.com/standardgalactic/spherepop-kernel/spec/schemas"
	"github.com/standardgalactic/spherepop-kernel/term"
	"github.com/standardgalactic/spherepop-kernel/trace"
)

var vectors *VectorSet

func TestMain(m *testing.M) {
	data, err := readVectorsFile()
	if err != nil {
		panic(err)
	}
	vectors, err = ParseVectors(data)
	if err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

func readVectorsFile() ([]byte, error) {
	candidates := []string{
		"vectors.json",
		filepath.Join("spec", "conformance", "vectors.json"),
	}
	var lastErr error
	for _, p := range candidates {
		b, err := os.ReadFile(p)
		if err == nil {
			return b, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// VectorSet is the complete P0 conformance suite (19 base + 5 UltraGraph vectors).
type VectorSet struct {
	Vectors           []Vector           `json:"vectors"`
	UltraGraphVectors []UltraGraphVector `json:"ultragraph_vectors"`
}

// Vector is a base Spherepop kernel conformance test vector.
type Vector struct {
	VectorID    string            `json:"vector_id"`
	Description string            `json:"description"`
	Config      reduce.ConfigSpec `json:"config"`
	StartTerm   string            `json:"start_term"`
	Fuel        uint64            `json:"fuel"`
	Strategy    string            `json:"strategy"`
	Expected    ExpectedOutcome   `json:"expected"`
	Trace       *trace.Document   `json:"trace,omitempty"`
}

// UltraGraphVector extends Vector with percolation, LOD, unitary, or Q16.16 checks.
type UltraGraphVector struct {
	Vector
	PercolationCheck *PercolationSpec `json:"percolation_check,omitempty"`
	LODCompression   *LODSpec         `json:"lod_compression,omitempty"`
	UnitaryCheck     *UnitarySpec     `json:"unitary_check,omitempty"`
	Q16Check         *Q16Spec         `json:"q16_check,omitempty"`
}

// ExpectedOutcome is the expected outcome of a bounded reduction or gate check.
type ExpectedOutcome struct {
	Outcome          string `json:"outcome"` // normal_form, exhausted, error
	NormalForm       string `json:"normal_form,omitempty"`
	NormalFormDigest string `json:"normal_form_digest,omitempty"`
	Steps            uint64 `json:"steps"`
	ErrorCode        string `json:"error_code,omitempty"`
}

type PercolationSpec struct {
	Vertices      int           `json:"vertices"`
	Edges         [][2]int      `json:"edges"`
	KappaRatio    schemas.Ratio `json:"kappa_ratio"`
	ResonanceTrit int8          `json:"resonance_trit"`
	Regime        string        `json:"regime"`
	Permitted     bool          `json:"permitted"`
}

type LODSpec struct {
	Adjacency    [][]int       `json:"adjacency"`
	EpsilonRatio schemas.Ratio `json:"epsilon_ratio"`
	SuperNodes   int           `json:"super_nodes"`
	SteinerHub   string        `json:"steiner_hub"`
}

type UnitarySpec struct {
	DeltaTQ16         int           `json:"delta_t_q16"`
	HalfStep          int           `json:"half_step"`
	ForwardDegree     int           `json:"forward_degree"`
	ReverseDegree     int           `json:"reverse_degree"`
	EntanglementRatio schemas.Ratio `json:"entanglement_ratio"`
}

type Q16Spec struct {
	LHSQ16     int32 `json:"lhs_q16"`
	RHSQ16     int32 `json:"rhs_q16"`
	ProductQ16 int32 `json:"product_q16"`
}

func ParseVectors(data []byte) (*VectorSet, error) {
	var vs VectorSet
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&vs); err != nil {
		return nil, err
	}
	return &vs, nil
}

// TestVectors runs all 19 base conformance vectors against the Go reference kernel.
func TestVectors(t *testing.T) {
	if len(vectors.Vectors) != 19 {
		t.Fatalf("expected 19 base vectors, got %d", len(vectors.Vectors))
	}
	for _, v := range vectors.Vectors {
		t.Run(v.VectorID, func(t *testing.T) {
			runKernelVector(t, v)
		})
	}
}

// TestUltraGraphVectors runs all 5 UltraGraph conformance vectors (kernel + gates).
func TestUltraGraphVectors(t *testing.T) {
	if len(vectors.UltraGraphVectors) != 5 {
		t.Fatalf("expected 5 UltraGraph vectors, got %d", len(vectors.UltraGraphVectors))
	}
	for _, uv := range vectors.UltraGraphVectors {
		t.Run(uv.VectorID, func(t *testing.T) {
			if uv.PercolationCheck != nil {
				pc := uv.PercolationCheck
				g := &schemas.ShadowGraph{
					Vertices: pc.Vertices,
					Edges:    pc.Edges,
				}
				kappa := g.MolloyReedKappa()
				if kappa != pc.KappaRatio {
					t.Fatalf("kappa_ratio mismatch: got %v, want %v", kappa, pc.KappaRatio)
				}
				if g.ResonanceTrit != pc.ResonanceTrit {
					t.Fatalf("resonance_trit mismatch: got %d, want %d", g.ResonanceTrit, pc.ResonanceTrit)
				}
				if g.Regime != pc.Regime {
					t.Fatalf("regime mismatch: got %q, want %q", g.Regime, pc.Regime)
				}
				err := g.CheckCollapseGate()
				if pc.Permitted && err != nil {
					t.Fatalf("expected collapse gate permitted, got error: %v", err)
				}
				if !pc.Permitted {
					if !errors.Is(err, schemas.ErrPercolationRisk) {
						t.Fatalf("expected ErrPercolationRisk, got %v", err)
					}
					if uv.Expected.Outcome == "error" && uv.Expected.ErrorCode == "ErrPercolationRisk" {
						return
					}
				}
			}

			if uv.LODCompression != nil {
				lc := uv.LODCompression
				part, err := schemas.SzemerediPartition(lc.Adjacency, nil, lc.EpsilonRatio)
				if err != nil {
					t.Fatalf("SzemerediPartition failed: %v", err)
				}
				if len(part.SuperNodes) != lc.SuperNodes {
					t.Fatalf("super_nodes count mismatch: got %d, want %d", len(part.SuperNodes), lc.SuperNodes)
				}
				if part.SteinerHub != lc.SteinerHub {
					t.Fatalf("steiner_hub mismatch: got %q, want %q", part.SteinerHub, lc.SteinerHub)
				}
			}

			if uv.UnitaryCheck != nil {
				uc := uv.UnitaryCheck
				step, err := schemas.NewUnitaryStep(
					"cid:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
					schemas.Q16_16(uc.DeltaTQ16),
					uc.HalfStep,
					uc.ForwardDegree,
					uc.ReverseDegree,
					time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC),
				)
				if err != nil {
					t.Fatalf("NewUnitaryStep failed: %v", err)
				}
				if step.EntanglementRatio != uc.EntanglementRatio {
					t.Fatalf("entanglement_ratio mismatch: got %v, want %v", step.EntanglementRatio, uc.EntanglementRatio)
				}
				psi := schemas.InitializeWaveFunction()
				vks := schemas.BuildKohnShamPotential(psi)
				schemas.StrangFullStep(psi, vks, schemas.Q16_16(uc.DeltaTQ16))
				if psi.Step != 1 {
					t.Fatalf("expected WaveFunction step=1, got %d", psi.Step)
				}
			}

			if uv.Q16Check != nil {
				qc := uv.Q16Check
				got := schemas.Q16Mul(schemas.Q16_16(qc.LHSQ16), schemas.Q16_16(qc.RHSQ16))
				if int32(got) != qc.ProductQ16 {
					t.Fatalf("Q16Mul(0x%08x, 0x%08x) = 0x%08x, want 0x%08x", qc.LHSQ16, qc.RHSQ16, int32(got), qc.ProductQ16)
				}
			}

			runKernelVector(t, uv.Vector)
		})
	}
}

func runKernelVector(t *testing.T, v Vector) {
	t.Helper()
	start, err := term.Parse(v.StartTerm)
	if err != nil {
		if v.Expected.Outcome == "error" && v.Expected.ErrorCode == "ErrSyntax" {
			return
		}
		t.Fatalf("parse start term %q: %v", v.StartTerm, err)
	}

	cfg, err := v.Config.Config()
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	strat, ok := reduce.ParseStrategy(v.Strategy)
	if !ok {
		t.Fatalf("unknown strategy %q", v.Strategy)
	}

	result, err := cfg.Reduce(start, strat, v.Fuel)
	switch v.Expected.Outcome {
	case "normal_form":
		if err != nil {
			t.Fatalf("unexpected reduce error: %v", err)
		}
		if result.Exhausted {
			t.Errorf("expected normal_form, got exhausted")
		}
		if result.Term.String() != v.Expected.NormalForm {
			t.Errorf("normal form mismatch: got %q, want %q", result.Term.String(), v.Expected.NormalForm)
		}
		if result.Term.Digest().String() != v.Expected.NormalFormDigest {
			t.Errorf("digest mismatch: got %s, want %s", result.Term.Digest().String(), v.Expected.NormalFormDigest)
		}
		if result.Steps != v.Expected.Steps {
			t.Errorf("step count mismatch: got %d, want %d", result.Steps, v.Expected.Steps)
		}

	case "exhausted":
		if err != nil {
			t.Fatalf("unexpected reduce error: %v", err)
		}
		if !result.Exhausted {
			t.Errorf("expected exhausted, got normal_form %q", result.Term.String())
		}
		if result.Steps != v.Expected.Steps {
			t.Errorf("step count mismatch: got %d, want %d", result.Steps, v.Expected.Steps)
		}

	case "error":
		if err == nil {
			t.Fatalf("expected error %s, got nil", v.Expected.ErrorCode)
		}
		switch v.Expected.ErrorCode {
		case "ErrUnknownLabel":
			if !errors.Is(err, reduce.ErrUnknownLabel) {
				t.Errorf("expected ErrUnknownLabel, got %v", err)
			}
		default:
			t.Errorf("unhandled expected error_code %q (got %v)", v.Expected.ErrorCode, err)
		}
	default:
		t.Fatalf("unknown expected outcome %q", v.Expected.Outcome)
	}
}

// TestTraceVerification verifies embedded hash-chained trace documents in vectors.json.
func TestTraceVerification(t *testing.T) {
	checked := 0
	for _, v := range vectors.Vectors {
		if v.Trace == nil {
			continue
		}
		checked++
		t.Run(v.VectorID+"-trace", func(t *testing.T) {
			rep, err := trace.Check(*v.Trace, trace.Options{
				RequireStrategy: true,
				Fuel:            v.Fuel,
			})
			if err != nil {
				t.Fatalf("trace verification failed: %v", err)
			}
			if uint64(rep.Steps) != v.Expected.Steps {
				t.Errorf("trace report steps=%d, want %d", rep.Steps, v.Expected.Steps)
			}
		})
	}
	if checked == 0 {
		t.Fatal("expected at least one embedded trace in vectors.json")
	}
}

// TestInterop records fresh traces via Go reducer, marshals/unmarshals wire JSON,
// and verifies via the independent trace checker.
func TestInterop(t *testing.T) {
	for _, v := range vectors.Vectors {
		if v.Expected.Outcome == "error" {
			continue
		}
		t.Run(v.VectorID+"-interop", func(t *testing.T) {
			start, err := term.Parse(v.StartTerm)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			cfg, err := v.Config.Config()
			if err != nil {
				t.Fatalf("config: %v", err)
			}
			strat, ok := reduce.ParseStrategy(v.Strategy)
			if !ok {
				t.Fatalf("strategy: %s", v.Strategy)
			}

			_, doc, err := trace.Record(cfg, start, strat, v.Fuel)
			if err != nil {
				t.Fatalf("trace.Record: %v", err)
			}
			wire, err := trace.Marshal(doc)
			if err != nil {
				t.Fatalf("trace.Marshal: %v", err)
			}
			decoded, err := trace.Unmarshal(wire)
			if err != nil {
				t.Fatalf("trace.Unmarshal: %v", err)
			}

			if _, err := trace.Check(decoded, trace.Options{
				RequireStrategy: true,
				Fuel:            v.Fuel,
			}); err != nil {
				t.Fatalf("self-interop trace.Check failed: %v", err)
			}
		})
	}
}

// TestSchemasAndExampleArtifacts verifies all 11 Draft 2020-12 JSON schemas parse
// cleanly with valid metadata and that example-commit.json passes cryptographic verification.
func TestSchemasAndExampleArtifacts(t *testing.T) {
	specRoot := filepath.Clean(filepath.Join(".."))
	schemaFiles := []string{
		filepath.Join(specRoot, "schemas", "term.json"),
		filepath.Join(specRoot, "schemas", "config.json"),
		filepath.Join(specRoot, "schemas", "trace.json"),
		filepath.Join(specRoot, "schemas", "budget.json"),
		filepath.Join(specRoot, "schemas", "footprint.json"),
		filepath.Join(specRoot, "schemas", "governance.json"),
		filepath.Join(specRoot, "schemas", "percolation.json"),
		filepath.Join(specRoot, "schemas", "lod.json"),
		filepath.Join(specRoot, "schemas", "unitary.json"),
		filepath.Join(specRoot, "authorization", "authorization.schema.json"),
		filepath.Join(specRoot, "conformance", "vectors.schema.json"),
	}

	for _, path := range schemaFiles {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read schema %s: %v", path, err)
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("invalid JSON in %s: %v", path, err)
		}
		if doc["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
			t.Errorf("%s: $schema = %v, want Draft 2020-12", path, doc["$schema"])
		}
		id, _ := doc["$id"].(string)
		if !strings.HasSuffix(id, filepath.Base(path)) {
			t.Errorf("%s: $id %q does not end with filename %q", path, id, filepath.Base(path))
		}
	}

	authRaw, err := os.ReadFile(filepath.Join(specRoot, "authorization", "example-commit.json"))
	if err != nil {
		t.Fatalf("read example-commit.json: %v", err)
	}
	var auth authorization.Authorization
	if err := json.Unmarshal(authRaw, &auth); err != nil {
		t.Fatalf("unmarshal example-commit.json: %v", err)
	}
	nonces := make(map[string]bool)
	refTime := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	if err := auth.VerifyForOperation("collapse", auth.Head, nonces, refTime); err != nil {
		t.Fatalf("example-commit.json failed cryptographic verification: %v", err)
	}
}

// TestZeroFloatInvariant parses the Go AST of every .go file under spec/ and
// asserts zero float32/float64 type references and zero floating-point literals.
func TestZeroFloatInvariant(t *testing.T) {
	specRoot := filepath.Clean(filepath.Join(".."))
	fset := token.NewFileSet()
	goFiles := 0

	err := filepath.WalkDir(specRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		goFiles++
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.Ident:
				if node.Name == "float32" || node.Name == "float64" || node.Name == "complex64" || node.Name == "complex128" {
					pos := fset.Position(node.Pos())
					t.Errorf("Zero-Float violation at %s:%d: forbidden type %s", pos.Filename, pos.Line, node.Name)
				}
			case *ast.BasicLit:
				if node.Kind == token.FLOAT || node.Kind == token.IMAG {
					pos := fset.Position(node.Pos())
					t.Errorf("Zero-Float violation at %s:%d: forbidden float literal %s", pos.Filename, pos.Line, node.Value)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk spec/: %v", err)
	}
	if goFiles < 8 {
		t.Fatalf("expected at least 8 Go files under spec/, scanned %d", goFiles)
	}
}

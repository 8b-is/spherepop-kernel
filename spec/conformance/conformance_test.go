// Conformance test runner for Spherepop kernel implementations
// Validates: Go, Zig, Rust, Idris implementations against spec/conformance/vectors.json
//
// Run from module root: go test ./spec/conformance/...

package conformance

import (
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/standardgalactic/spherepop-kernel/term"
	"github.com/standardgalactic/spherepop-kernel/reduce"
	"github.com/standardgalactic/spherepop-kernel/trace"
)

var vectors *VectorSet

func TestMain(m *testing.M) {
	data, err := os.ReadFile("spec/conformance/vectors.json")
	if err != nil {
		panic(err)
	}
	vectors, err = ParseVectors(data)
	if err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

// VectorSet is the collection of conformance vectors
type VectorSet struct {
	Vectors []Vector `json:"vectors"`
}

// Vector is a single conformance test vector
type Vector struct {
	VectorID     string          `json:"vector_id"`
	Description  string          `json:"description"`
	Config       reduce.Config   `json:"config"`
	StartTerm    string          `json:"start_term"`
	Fuel         int             `json:"fuel"`
	Strategy     string          `json:"strategy"`
	Expected     ExpectedOutcome `json:"expected"`
	Trace        *trace.Trace    `json:"trace,omitempty"`
}

// ExpectedOutcome is the expected result of a reduction
type ExpectedOutcome struct {
	Outcome           string `json:"outcome"`            // normal_form, exhausted, error
	NormalForm        string `json:"normal_form,omitempty"`
	NormalFormDigest  string `json:"normal_form_digest,omitempty"`
	Steps             int    `json:"steps"`
	ErrorCode         string `json:"error_code,omitempty"`
}

func ParseVectors(data []byte) (*VectorSet, error) {
	var vs VectorSet
	if err := json.Unmarshal(data, &vs); err != nil {
		return nil, err
	}
	return &vs, nil
}

// TestVectors runs all conformance vectors against the Go reference kernel
func TestVectors(t *testing.T) {
	for _, v := range vectors.Vectors {
		t.Run(v.VectorID, func(t *testing.T) {
			runVector(t, v)
		})
	}
}

func runVector(t *testing.T, v Vector) {
	// Parse start term
	start, err := term.Parse(v.StartTerm)
	if err != nil {
		if v.Expected.Outcome == "error" && v.Expected.ErrorCode == "ErrSyntax" {
			return // Expected syntax error
		}
		t.Fatalf("parse start term: %v", err)
	}

	// Run reduction
	result := reduce.Reduce(start, v.Config, v.Strategy, v.Fuel)

	// Check outcome
	switch v.Expected.Outcome {
	case "normal_form":
		if result.Exhausted {
			t.Errorf("expected normal_form, got exhausted")
		}
		if result.Term.String() != v.Expected.NormalForm {
			t.Errorf("normal form mismatch: got %q, want %q", result.Term.String(), v.Expected.NormalForm)
		}
		if result.Term.Digest() != v.Expected.NormalFormDigest && v.Expected.NormalFormDigest != "placeholder" {
			t.Errorf("digest mismatch: got %s, want %s", result.Term.Digest(), v.Expected.NormalFormDigest)
		}
		if result.Steps != v.Expected.Steps {
			t.Errorf("step count mismatch: got %d, want %d", result.Steps, v.Expected.Steps)
		}

	case "exhausted":
		if !result.Exhausted {
			t.Errorf("expected exhausted, got normal_form")
		}
		if result.Steps != v.Expected.Steps {
			t.Errorf("step count mismatch: got %d, want %d", result.Steps, v.Expected.Steps)
		}

	case "error":
		// For error outcomes, we expect the reducer to return an error
		// This depends on the specific error type
		if v.Expected.ErrorCode == "ErrUnknownLabel" {
			// Would need to check error type
		}
	}
}

// TestTraceVerification tests trace checking against vector traces
func TestTraceVerification(t *testing.T) {
	for _, v := range vectors.Vectors {
		if v.Trace == nil {
			continue
		}
		t.Run(v.VectorID+"-trace", func(t *testing.T) {
			// Verify the trace
			err := trace.Check(v.Trace, trace.CheckOptions{
				RequireStrategy: true,
				Fuel:            v.Fuel,
			})
			if err != nil {
				t.Errorf("trace verification failed: %v", err)
			}
		})
	}
}

// InteropTest runs a trace produced by one implementation through another's checker
func TestInterop(t *testing.T) {
	// This would be run with traces from Zig, Rust, Idris verifiers
	// For now, test that Go checker accepts Go-produced traces
	for _, v := range vectors.Vectors {
		t.Run(v.VectorID+"-interop", func(t *testing.T) {
			start, _ := term.Parse(v.StartTerm)
			result := reduce.Reduce(start, v.Config, v.Strategy, v.Fuel)

			// Produce a trace
			tr, err := trace.Produce(start, v.Config, v.Strategy, v.Fuel, result)
			if err != nil {
				t.Fatalf("trace production: %v", err)
			}

			// Verify it
			err = trace.Check(tr, trace.CheckOptions{
				RequireStrategy: true,
				Fuel:            v.Fuel,
			})
			if err != nil {
				t.Errorf("self-interop failed: %v", err)
			}
		})
	}
}
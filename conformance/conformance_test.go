package conformance

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/standardgalactic/spherepop-kernel/effects"
	"github.com/standardgalactic/spherepop-kernel/sched"
	"github.com/standardgalactic/spherepop-kernel/term"
	"github.com/standardgalactic/spherepop-kernel/trace"
)

// The corpus in this directory is the interoperability contract of SPEC
// section 18. manifest.json lists every case with the verdict and failure
// class the specification requires. This test holds the Go verifiers to it,
// and TestPythonReferenceVerifierAgrees holds the independent Python verifier
// to the same file.

type manifest struct {
	Format string `json:"format"`
	Cases  []struct {
		ID      string `json:"id"`
		Kind    string `json:"kind"`
		File    string `json:"file"`
		Expect  string `json:"expect"`
		Class   string `json:"class"`
		Note    string `json:"note"`
		Options struct {
			RequireStrategy bool   `json:"require_strategy"`
			Stop            bool   `json:"stop"`
			MaxSteps        int    `json:"max_steps"`
			Fuel            uint64 `json:"fuel"`
			Schema          string `json:"schema"`
			Anchor          string `json:"anchor"`
		} `json:"options"`
	} `json:"cases"`
}

func load(t *testing.T) manifest {
	t.Helper()
	b, err := os.ReadFile("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m manifest
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m.Format != "spherepop-conformance/1" || len(m.Cases) == 0 {
		t.Fatalf("manifest: format %q, %d cases", m.Format, len(m.Cases))
	}
	return m
}

var traceClasses = []struct {
	err   error
	class string
}{
	{trace.ErrFormat, "format"},
	{trace.ErrStartMismatch, "start-mismatch"},
	{trace.ErrChainBroken, "chain"},
	{trace.ErrStepRejected, "step-rejected"},
	{trace.ErrDigestMismatch, "digest-mismatch"},
	{trace.ErrFinalMismatch, "final-mismatch"},
	{trace.ErrStatusMismatch, "status-mismatch"},
	{trace.ErrStrategyDeviation, "strategy-deviation"},
	{trace.ErrStepAfterStop, "step-after-stop"},
}

func classify(err error) string {
	if err == nil {
		return ""
	}
	// A scheduler rejection wraps the trace failure of its slice as well;
	// SPEC 18.7 gives it the single class log-rejected.
	if errors.Is(err, sched.ErrLogRejected) {
		return "log-rejected"
	}
	for _, c := range traceClasses {
		if errors.Is(err, c.err) {
			return c.class
		}
	}
	switch {
	case errors.Is(err, effects.ErrChain):
		return "chain"
	case errors.Is(err, effects.ErrLogEntry):
		return "log-entry"
	case errors.Is(err, effects.ErrAnchor):
		return "anchor"
	}
	return "unclassified: " + err.Error()
}

func TestGoVerifiersMeetTheCorpus(t *testing.T) {
	m := load(t)
	for _, c := range m.Cases {
		t.Run(c.ID, func(t *testing.T) {
			b, err := os.ReadFile(c.File)
			if err != nil {
				t.Fatal(err)
			}
			var verr error
			switch c.Kind {
			case "trace":
				var d trace.Document
				if d, verr = trace.Unmarshal(b); verr == nil {
					opt := trace.Options{RequireStrategy: c.Options.RequireStrategy, MaxSteps: c.Options.MaxSteps, Fuel: c.Options.Fuel}
					if c.Options.Stop {
						opt.Stop = func(t *term.Term) bool { _, ok := sched.Continuation(t); return ok }
					}
					_, verr = trace.Check(d, opt)
				}
			case "sched":
				var l sched.Log
				if l, verr = sched.Unmarshal(b); verr == nil {
					verr = sched.CheckLog(l)
				}
			case "effects":
				var entries []effects.Entry
				entries, verr = effects.ReadJSONL(bytes.NewReader(b))
				if verr == nil && c.Options.Schema != "" {
					sb, err := os.ReadFile(c.Options.Schema)
					if err != nil {
						t.Fatal(err)
					}
					schema, err := effects.ParseSchema(sb)
					if err != nil {
						t.Fatal(err)
					}
					verr = effects.VerifyAgainst(entries, schema)
				}
				if verr == nil && c.Options.Anchor != "" {
					verr = effects.VerifyAnchored(entries, c.Options.Anchor)
				}
			default:
				t.Fatalf("unknown kind %q", c.Kind)
			}
			got := "valid"
			if verr != nil {
				got = "invalid"
			}
			if got != c.Expect {
				t.Fatalf("verdict %s (%v), want %s", got, verr, c.Expect)
			}
			if got == "invalid" && c.Class != "" && classify(verr) != c.Class {
				t.Fatalf("class %q (%v), want %q", classify(verr), verr, c.Class)
			}
		})
	}
}

func TestPythonReferenceVerifierAgrees(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}
	script, _ := filepath.Abs(filepath.Join("..", "tools", "verify-py", "spherepop_verify.py"))
	out, err := exec.Command(py, "-I", script, "corpus", "manifest.json").CombinedOutput()
	if err != nil {
		var bad []string
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(line, `"ok": false`) {
				bad = append(bad, line)
			}
		}
		t.Fatalf("the Python verifier disagrees with the corpus: %v\n%s", err, strings.Join(bad, "\n"))
	}
}

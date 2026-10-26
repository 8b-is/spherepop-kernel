// Package trace records reductions as evidence and checks that evidence. A
// trace is a claim: this start term, under this configuration, reached this
// final term by these steps. The checker accepts the claim only if every step
// is authorised by the rule table, so acceptance never rests on the worker
// that produced the trace and never on a matching final hash alone.
package trace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/standardgalactic/spherepop-kernel/reduce"
	"github.com/standardgalactic/spherepop-kernel/term"
)

// Format identifies the document layout. A checker rejects any other value.
const Format = "spherepop-trace/1"

// StepRecord is one claimed step. After is the claimed digest of the whole
// term once the step is applied; Link chains the record to its predecessors.
type StepRecord struct {
	Rule  reduce.RuleID `json:"rule"`
	Path  string        `json:"path"`
	After string        `json:"after"`
	Link  string        `json:"link"`
}

// Document is a complete trace. Every field except Format and Start is a
// claim that the checker verifies or, for Strategy, optionally compares.
type Document struct {
	Format      string            `json:"format"`
	Config      reduce.ConfigSpec `json:"config"`
	Start       string            `json:"start"`
	StartDigest string            `json:"start_digest"`
	Strategy    string            `json:"strategy"`
	Fuel        uint64            `json:"fuel"`
	Steps       []StepRecord      `json:"steps"`
	Final       string            `json:"final"`
	FinalDigest string            `json:"final_digest"`
	Exhausted   bool              `json:"exhausted"`
	Head        string            `json:"head"`
}

func sum(parts ...[]byte) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write(p)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func configDigest(s reduce.ConfigSpec) (string, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	return sum([]byte("spherepop-config/1"), b), nil
}

// genesis is the first link of the chain. It binds the chain to the start
// term and the configuration, so a step list cannot be moved to another run.
func genesis(startDigest, cfgDigest string) string {
	return sum([]byte(Format), []byte(startDigest), []byte(cfgDigest))
}

func link(prev string, r reduce.RuleID, path, after string) string {
	return sum([]byte(prev), []byte(r), []byte(path), []byte(after))
}

// seal recomputes every link and the head from the steps as they stand.
func (d *Document) seal() error {
	cd, err := configDigest(d.Config)
	if err != nil {
		return err
	}
	prev := genesis(d.StartDigest, cd)
	for i := range d.Steps {
		s := &d.Steps[i]
		s.Link = link(prev, s.Rule, s.Path, s.After)
		prev = s.Link
	}
	d.Head = prev
	return nil
}

// Build assembles a document from a finished reduction. The configuration
// must be the one the reduction ran under and must have recorded a trace.
func Build(cfg reduce.Config, start *term.Term, strat reduce.Strategy, fuel uint64, res reduce.Result) (Document, error) {
	if uint64(len(res.Trace)) != res.Steps {
		return Document{}, fmt.Errorf("trace: result has %d steps but %d recorded; run with Config.Trace set", res.Steps, len(res.Trace))
	}
	d := Document{
		Format:      Format,
		Config:      cfg.Spec(),
		Start:       start.String(),
		StartDigest: start.Digest().String(),
		Strategy:    strat.String(),
		Fuel:        fuel,
		Final:       res.Term.String(),
		FinalDigest: res.Term.Digest().String(),
		Exhausted:   res.Exhausted,
		Steps:       make([]StepRecord, len(res.Trace)),
	}
	for i, s := range res.Trace {
		d.Steps[i] = StepRecord{Rule: s.Rule, Path: string(s.Path), After: s.After.String()}
	}
	if err := d.seal(); err != nil {
		return Document{}, err
	}
	return d, nil
}

// Record reduces start and returns the result together with its trace.
func Record(cfg reduce.Config, start *term.Term, strat reduce.Strategy, fuel uint64) (reduce.Result, Document, error) {
	cfg.Trace = true
	res, err := cfg.Reduce(start, strat, fuel)
	if err != nil {
		return res, Document{}, err
	}
	d, err := Build(cfg, start, strat, fuel, res)
	return res, d, err
}

// Marshal encodes d as indented JSON with a trailing newline.
func Marshal(d Document) ([]byte, error) {
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// Unmarshal decodes a document and rejects unknown fields, so a trace cannot
// carry data the checker silently ignores.
func Unmarshal(b []byte) (Document, error) {
	var d Document
	dec := json.NewDecoder(bytesReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return Document{}, fmt.Errorf("%w: %v", ErrFormat, err)
	}
	if dec.More() {
		return Document{}, fmt.Errorf("%w: trailing data after document", ErrFormat)
	}
	return d, nil
}

package effects

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/standardgalactic/spherepop-kernel/internal/strict"
	"github.com/standardgalactic/spherepop-kernel/term"
)

// Disposition is what the host did about a proposal, or about an output that
// had none to consider. Every outcome is recorded, refusals included.
type Disposition string

// The dispositions.
const (
	Committed Disposition = "committed" // authorized, and the world applied it
	Denied    Disposition = "denied"    // well-formed, and the authorizer refused it
	Failed    Disposition = "failed"    // authorized, and the world refused to apply it
	Malformed Disposition = "malformed" // not an instance of the schema
	NoOutput  Disposition = "no-output" // normal form with no designated output
	Exhausted Disposition = "exhausted" // fuel ran out; a partial term is never read
	Truncated Disposition = "truncated" // requests beyond the host's per-output cap
)

// Entry is one record. Index is the position of the request in the output
// list, or -1 for a record about the whole output.
type Entry struct {
	Seq         uint64      `json:"seq"`
	Tick        int         `json:"tick"`
	Subject     string      `json:"subject"`
	Ref         string      `json:"ref,omitempty"`
	Index       int         `json:"index"`
	Request     string      `json:"request,omitempty"`
	Disposition Disposition `json:"disposition"`
	Reason      string      `json:"reason,omitempty"`
	Prev        string      `json:"prev"`
	Hash        string      `json:"hash"`
}

const logDomain = "spherepop-effects/2"

func genesis() string {
	h := sha256.Sum256([]byte(logDomain))
	return hex.EncodeToString(h[:])
}

func framed(buf []byte, s string) []byte {
	buf = binary.AppendUvarint(buf, uint64(len(s)))
	return append(buf, s...)
}

// digest is the entry hash of SPEC 18.8: a binary encoding with no dependence
// on how any JSON library escapes a string.
func (e Entry) digest() string {
	b := framed(nil, logDomain)
	b = binary.BigEndian.AppendUint64(b, e.Seq)
	b = binary.BigEndian.AppendUint64(b, uint64(int64(e.Tick)))
	b = framed(b, e.Subject)
	b = framed(b, e.Ref)
	b = binary.BigEndian.AppendUint64(b, uint64(int64(e.Index)))
	b = framed(b, e.Request)
	b = framed(b, string(e.Disposition))
	b = framed(b, e.Reason)
	b = framed(b, e.Prev)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Log is an append-only, hash-chained sequence of entries. Appending is the
// only operation that adds to it, and a Log offers no way to alter or remove
// an entry. The chain makes later alteration detectable, but only relative to
// a head digest kept elsewhere: whoever can rewrite the whole log can rewrite
// the chain with it.
type Log struct {
	entries []Entry
	sink    io.Writer
}

// NewLog returns an empty log. If sink is not nil, each entry is also written
// to it as one JSON line at the moment it is appended.
func NewLog(sink io.Writer) *Log { return &Log{sink: sink} }

// Head returns the digest of the last entry, or the genesis digest if empty.
func (l *Log) Head() string {
	if len(l.entries) == 0 {
		return genesis()
	}
	return l.entries[len(l.entries)-1].Hash
}

// Entries returns a copy of the entries.
func (l *Log) Entries() []Entry { return append([]Entry(nil), l.entries...) }

// Append completes and stores e, returning the stored entry.
func (l *Log) Append(e Entry) (Entry, error) {
	e.Seq = uint64(len(l.entries))
	e.Prev = l.Head()
	e.Hash = e.digest()
	if l.sink != nil {
		b, err := json.Marshal(e)
		if err == nil {
			_, err = l.sink.Write(append(b, '\n'))
		}
		if err != nil {
			return Entry{}, fmt.Errorf("effects: log sink: %w", err)
		}
	}
	l.entries = append(l.entries, e)
	return e, nil
}

// Errors reported by log verification.
var (
	ErrChain    = errors.New("effects: log chain does not verify")
	ErrAnchor   = errors.New("effects: log head does not match the anchor")
	ErrLogEntry = errors.New("effects: log entry is inconsistent with the schema")
)

// Verify recomputes the chain over entries.
func Verify(entries []Entry) error {
	prev := genesis()
	for i, e := range entries {
		if e.Seq != uint64(i) || e.Prev != prev || e.Hash != e.digest() || e.Index < -1 {
			return fmt.Errorf("%w: entry %d", ErrChain, i)
		}
		prev = e.Hash
	}
	return nil
}

// VerifyAnchored additionally requires the final head to equal an anchor
// held outside the log. This is what detects removal of the tail, which a
// bare chain cannot.
func VerifyAnchored(entries []Entry, anchor string) error {
	if err := Verify(entries); err != nil {
		return err
	}
	head := genesis()
	if len(entries) > 0 {
		head = entries[len(entries)-1].Hash
	}
	if head != anchor {
		return fmt.Errorf("%w: head %.12s, anchor %.12s", ErrAnchor, head, anchor)
	}
	return nil
}

// VerifyAgainst checks the chain and also that the log does not claim
// something the schema forbids. A request that was committed, denied or failed
// must decode under the schema, and one recorded as malformed must not. A log
// therefore cannot present a schema-invalid request as committed.
func VerifyAgainst(entries []Entry, s Schema) error {
	if err := Verify(entries); err != nil {
		return err
	}
	for i, e := range entries {
		switch e.Disposition {
		case Committed, Denied, Failed, Malformed:
			if e.Request == "" || e.Index < 0 {
				return fmt.Errorf("%w: entry %d has no request or no request index", ErrLogEntry, i)
			}
			t, err := term.Parse(e.Request)
			if err != nil {
				if e.Disposition == Malformed {
					continue
				}
				return fmt.Errorf("%w: entry %d: %v", ErrLogEntry, i, err)
			}
			if t.String() != e.Request {
				return fmt.Errorf("%w: entry %d: request is not in canonical text", ErrLogEntry, i)
			}
			_, _, derr := s.DecodeOutput(t)
			if e.Disposition == Malformed && derr == nil {
				return fmt.Errorf("%w: entry %d is recorded malformed but decodes", ErrLogEntry, i)
			}
			if e.Disposition != Malformed && derr != nil {
				return fmt.Errorf("%w: entry %d is recorded %s but does not decode: %v", ErrLogEntry, i, e.Disposition, derr)
			}
		case NoOutput, Exhausted, Truncated:
			if e.Index != -1 || e.Request != "" {
				return fmt.Errorf("%w: entry %d: %s concerns the whole output", ErrLogEntry, i, e.Disposition)
			}
		default:
			return fmt.Errorf("%w: entry %d has unknown disposition %q", ErrLogEntry, i, e.Disposition)
		}
	}
	return nil
}

// ReadJSONL reads entries written to a sink and verifies the chain.
func ReadJSONL(r io.Reader) ([]Entry, error) {
	var out []Entry
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		var e Entry
		if err := strict.Unmarshal(sc.Bytes(), &e); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrChain, err)
		}
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, Verify(out)
}

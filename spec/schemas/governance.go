// Governance amendment procedure for Spherepop kernel (Protocol §10).
// Implements exact rational p/q quorum tallying, veto enforcement, emergency
// path (unanimous veto acceptance + 1/2 quorum), expiration, and RFC 6902 JSON Patch.

package schemas

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/bits"
	"strconv"
	"strings"
	"time"
)

var (
	ErrQuorumNotMet    = errors.New("governance: quorum not met")
	ErrVetoBlocked     = errors.New("governance: veto holder rejected or missing emergency consent")
	ErrNotAuthorized   = errors.New("governance: voter not authorized")
	ErrProposalExpired = errors.New("governance: proposal expired")
	ErrInvalidPatch    = errors.New("governance: invalid RFC 6902 patch")
	ErrAlreadyResolved = errors.New("governance: proposal already resolved")
)

// PatchDocument wraps RFC 6902 operations per governance.json.
type PatchDocument struct {
	Operations []PatchOperation `json:"operations"`
}

// PatchOperation is a single RFC 6902 JSON Patch operation.
type PatchOperation struct {
	Op    string          `json:"op"`
	Path  string          `json:"path"`
	From  string          `json:"from,omitempty"`
	Value json.RawMessage `json:"value,omitempty"`
}

// Proposal represents a governance amendment proposal.
type Proposal struct {
	ProposalID  string            `json:"proposal_id"`
	Rationale   string            `json:"rationale"`
	Patch       PatchDocument     `json:"patch"`
	QuorumRatio Ratio             `json:"quorum_ratio"`
	VetoHolders []string          `json:"veto_holders"`
	Emergency   bool              `json:"emergency"`
	Status      string            `json:"status"` // pending, accepted, rejected, expired
	Votes       map[string]string `json:"votes,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	ExpiresAt   *time.Time        `json:"expires_at,omitempty"`
	ResolvedAt  *time.Time        `json:"resolved_at,omitempty"`
	Authorizers []string          `json:"authorizers,omitempty"`
}

// Vote records an authorized vote ("accept", "reject", or "abstain") on a pending proposal.
func (p *Proposal) Vote(voterDID, vote string) error {
	if p == nil || p.Status != "pending" {
		return ErrAlreadyResolved
	}
	authorized := false
	for _, a := range p.Authorizers {
		if a == voterDID {
			authorized = true
			break
		}
	}
	if !authorized {
		return ErrNotAuthorized
	}
	if vote != "accept" && vote != "reject" && vote != "abstain" {
		return errors.New("governance: invalid vote value")
	}
	if p.Votes == nil {
		p.Votes = make(map[string]string)
	}
	p.Votes[voterDID] = vote
	return nil
}

// Resolve evaluates votes against exact rational p/q quorum and veto rules.
func (p *Proposal) Resolve(now time.Time) error {
	if p == nil || p.Status != "pending" {
		return ErrAlreadyResolved
	}
	if p.ExpiresAt != nil && !now.Before(*p.ExpiresAt) {
		p.Status = "expired"
		ts := now
		p.ResolvedAt = &ts
		return ErrProposalExpired
	}
	if len(p.Authorizers) == 0 {
		p.Status = "rejected"
		ts := now
		p.ResolvedAt = &ts
		return ErrQuorumNotMet
	}

	// Standard path: any veto holder voting "reject" blocks.
	// Emergency path (Protocol §10): ALL veto holders must explicitly vote "accept".
	for _, vetoDID := range p.VetoHolders {
		vote, voted := p.Votes[vetoDID]
		if voted && vote == "reject" {
			p.Status = "rejected"
			ts := now
			p.ResolvedAt = &ts
			return ErrVetoBlocked
		}
		if p.Emergency && (!voted || vote != "accept") {
			p.Status = "rejected"
			ts := now
			p.ResolvedAt = &ts
			return ErrVetoBlocked
		}
	}

	var acceptCount uint64
	seenAuth := make(map[string]bool, len(p.Authorizers))
	var totalAuthorizers uint64
	for _, a := range p.Authorizers {
		if seenAuth[a] {
			continue
		}
		seenAuth[a] = true
		totalAuthorizers++
		if p.Votes[a] == "accept" {
			acceptCount++
		}
	}

	required := Ratio{P: 67, Q: 100}
	if p.QuorumRatio.Q != 0 && p.QuorumRatio.P != 0 {
		c, err := p.QuorumRatio.Canonical()
		if err != nil || c.Cmp(Ratio{P: 1, Q: 2}) < 0 || c.Cmp(Ratio{P: 1, Q: 1}) > 0 {
			return errors.New("governance: quorum_ratio must lie in [1/2, 1/1]")
		}
		required = c
	}
	if p.Emergency {
		required = Ratio{P: 1, Q: 2}
	}

	// Exact 128-bit comparison: acceptCount / totalAuthorizers >= required.P / required.Q
	// <=> acceptCount * required.Q >= required.P * totalAuthorizers
	lhsHi, lhsLo := bits.Mul64(acceptCount, required.Q)
	rhsHi, rhsLo := bits.Mul64(required.P, totalAuthorizers)
	quorumMet := lhsHi > rhsHi || (lhsHi == rhsHi && lhsLo >= rhsLo)

	ts := now
	p.ResolvedAt = &ts
	if !quorumMet {
		p.Status = "rejected"
		return ErrQuorumNotMet
	}
	p.Status = "accepted"
	return nil
}

// GovernanceEvent records the content-addressed execution of an accepted amendment.
type GovernanceEvent struct {
	EventID    string    `json:"event_id"`
	ProposalID string    `json:"proposal_id"`
	Outcome    string    `json:"outcome"`
	AppliedAt  time.Time `json:"applied_at"`
	AppliedBy  string    `json:"applied_by"`
	PatchHash  string    `json:"patch_hash"`
}

// ApplyPatch executes the RFC 6902 operations against currentPolicy and emits a
// content-addressed GovernanceEvent.
func (p *Proposal) ApplyPatch(currentPolicy json.RawMessage, executorDID string) (json.RawMessage, *GovernanceEvent, error) {
	if p == nil || p.Status != "accepted" {
		return nil, nil, errors.New("governance: proposal not accepted")
	}
	if len(p.Patch.Operations) == 0 {
		return nil, nil, ErrInvalidPatch
	}

	var doc any
	dec := json.NewDecoder(bytes.NewReader(currentPolicy))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return nil, nil, ErrInvalidPatch
	}

	var err error
	for _, op := range p.Patch.Operations {
		doc, err = applyPatchOp(doc, op)
		if err != nil {
			return nil, nil, err
		}
	}

	updatedBytes, err := json.Marshal(doc)
	if err != nil {
		return nil, nil, err
	}

	patchBytes, err := json.Marshal(p.Patch)
	if err != nil {
		return nil, nil, err
	}
	sum := sha256.Sum256(patchBytes)
	patchHash := hex.EncodeToString(sum[:])
	appliedAt := time.Now().UTC()

	eventID := computeCID(
		"spherepop-governance-event/1",
		[]byte(p.ProposalID),
		[]byte(executorDID),
		[]byte(patchHash),
		updatedBytes,
	)

	event := &GovernanceEvent{
		EventID:    eventID,
		ProposalID: p.ProposalID,
		Outcome:    "accepted",
		AppliedAt:  appliedAt,
		AppliedBy:  executorDID,
		PatchHash:  patchHash,
	}
	return json.RawMessage(updatedBytes), event, nil
}

func applyPatchOp(doc any, op PatchOperation) (any, error) {
	switch op.Op {
	case "add":
		val, err := decodePatchValue(op.Value)
		if err != nil {
			return nil, err
		}
		return pointerPut(doc, op.Path, val, false)
	case "replace":
		val, err := decodePatchValue(op.Value)
		if err != nil {
			return nil, err
		}
		return pointerPut(doc, op.Path, val, true)
	case "remove":
		out, _, err := pointerRemove(doc, op.Path)
		return out, err
	case "move":
		if op.From == "" {
			return nil, ErrInvalidPatch
		}
		intermediate, moved, err := pointerRemove(doc, op.From)
		if err != nil {
			return nil, err
		}
		return pointerPut(intermediate, op.Path, moved, false)
	case "copy":
		if op.From == "" {
			return nil, ErrInvalidPatch
		}
		copied, err := pointerGet(doc, op.From)
		if err != nil {
			return nil, err
		}
		cloned, err := deepCloneJSON(copied)
		if err != nil {
			return nil, err
		}
		return pointerPut(doc, op.Path, cloned, false)
	case "test":
		expected, err := decodePatchValue(op.Value)
		if err != nil {
			return nil, err
		}
		actual, err := pointerGet(doc, op.Path)
		if err != nil {
			return nil, err
		}
		aBytes, _ := json.Marshal(actual)
		eBytes, _ := json.Marshal(expected)
		if !bytes.Equal(aBytes, eBytes) {
			return nil, ErrInvalidPatch
		}
		return doc, nil
	default:
		return nil, ErrInvalidPatch
	}
}

func decodePatchValue(raw json.RawMessage) (any, error) {
	if len(raw) == 0 {
		return nil, ErrInvalidPatch
	}
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, ErrInvalidPatch
	}
	return v, nil
}

func deepCloneJSON(v any) (any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, ErrInvalidPatch
	}
	return decodePatchValue(b)
}

func splitPointer(ptr string) ([]string, error) {
	if ptr == "" {
		return nil, nil
	}
	if ptr[0] != '/' {
		return nil, ErrInvalidPatch
	}
	parts := strings.Split(ptr[1:], "/")
	for i, p := range parts {
		p = strings.ReplaceAll(p, "~1", "/")
		p = strings.ReplaceAll(p, "~0", "~")
		parts[i] = p
	}
	return parts, nil
}

func pointerGet(cur any, ptr string) (any, error) {
	tokens, err := splitPointer(ptr)
	if err != nil {
		return nil, err
	}
	for _, tok := range tokens {
		switch node := cur.(type) {
		case map[string]any:
			next, ok := node[tok]
			if !ok {
				return nil, ErrInvalidPatch
			}
			cur = next
		case []any:
			idx, err := strconv.Atoi(tok)
			if err != nil || idx < 0 || idx >= len(node) {
				return nil, ErrInvalidPatch
			}
			cur = node[idx]
		default:
			return nil, ErrInvalidPatch
		}
	}
	return cur, nil
}

func pointerPut(cur any, ptr string, val any, mustExist bool) (any, error) {
	tokens, err := splitPointer(ptr)
	if err != nil {
		return nil, err
	}
	if len(tokens) == 0 {
		return val, nil
	}
	return putTokens(cur, tokens, val, mustExist)
}

func putTokens(cur any, tokens []string, val any, mustExist bool) (any, error) {
	tok := tokens[0]
	if len(tokens) == 1 {
		switch node := cur.(type) {
		case map[string]any:
			if mustExist {
				if _, ok := node[tok]; !ok {
					return nil, ErrInvalidPatch
				}
			}
			node[tok] = val
			return node, nil
		case []any:
			if tok == "-" && !mustExist {
				return append(node, val), nil
			}
			idx, err := strconv.Atoi(tok)
			if err != nil || idx < 0 {
				return nil, ErrInvalidPatch
			}
			if mustExist {
				if idx >= len(node) {
					return nil, ErrInvalidPatch
				}
				node[idx] = val
				return node, nil
			}
			if idx > len(node) {
				return nil, ErrInvalidPatch
			}
			out := make([]any, 0, len(node)+1)
			out = append(out, node[:idx]...)
			out = append(out, val)
			out = append(out, node[idx:]...)
			return out, nil
		default:
			return nil, ErrInvalidPatch
		}
	}

	switch node := cur.(type) {
	case map[string]any:
		child, ok := node[tok]
		if !ok {
			return nil, ErrInvalidPatch
		}
		updated, err := putTokens(child, tokens[1:], val, mustExist)
		if err != nil {
			return nil, err
		}
		node[tok] = updated
		return node, nil
	case []any:
		idx, err := strconv.Atoi(tok)
		if err != nil || idx < 0 || idx >= len(node) {
			return nil, ErrInvalidPatch
		}
		updated, err := putTokens(node[idx], tokens[1:], val, mustExist)
		if err != nil {
			return nil, err
		}
		node[idx] = updated
		return node, nil
	default:
		return nil, ErrInvalidPatch
	}
}

func pointerRemove(cur any, ptr string) (any, any, error) {
	tokens, err := splitPointer(ptr)
	if err != nil || len(tokens) == 0 {
		return nil, nil, ErrInvalidPatch
	}
	return removeTokens(cur, tokens)
}

func removeTokens(cur any, tokens []string) (any, any, error) {
	tok := tokens[0]
	if len(tokens) == 1 {
		switch node := cur.(type) {
		case map[string]any:
			removed, ok := node[tok]
			if !ok {
				return nil, nil, ErrInvalidPatch
			}
			delete(node, tok)
			return node, removed, nil
		case []any:
			idx, err := strconv.Atoi(tok)
			if err != nil || idx < 0 || idx >= len(node) {
				return nil, nil, ErrInvalidPatch
			}
			removed := node[idx]
			out := make([]any, 0, len(node)-1)
			out = append(out, node[:idx]...)
			out = append(out, node[idx+1:]...)
			return out, removed, nil
		default:
			return nil, nil, ErrInvalidPatch
		}
	}

	switch node := cur.(type) {
	case map[string]any:
		child, ok := node[tok]
		if !ok {
			return nil, nil, ErrInvalidPatch
		}
		updated, removed, err := removeTokens(child, tokens[1:])
		if err != nil {
			return nil, nil, err
		}
		node[tok] = updated
		return node, removed, nil
	case []any:
		idx, err := strconv.Atoi(tok)
		if err != nil || idx < 0 || idx >= len(node) {
			return nil, nil, ErrInvalidPatch
		}
		updated, removed, err := removeTokens(node[idx], tokens[1:])
		if err != nil {
			return nil, nil, err
		}
		node[idx] = updated
		return node, removed, nil
	default:
		return nil, nil, ErrInvalidPatch
	}
}

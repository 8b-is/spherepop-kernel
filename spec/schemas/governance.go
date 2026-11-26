// Governance amendment procedure for Spherepop kernel
// Implements: quorum, veto, emergency path, rollback

package governance

import (
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrQuorumNotMet      = errors.New("governance: quorum not met")
	ErrVetoBlocked       = errors.New("governance: veto holder rejected")
	ErrNotAuthorized     = errors.New("governance: voter not authorized")
	ErrProposalExpired   = errors.New("governance: proposal expired")
	ErrInvalidPatch      = errors.New("governance: invalid patch")
	ErrAlreadyResolved   = errors.New("governance: proposal already resolved")
)

// Proposal represents a governance amendment proposal
type Proposal struct {
	ProposalID   string                 `json:"proposal_id"`
	Rationale    string                 `json:"rationale"`
	Patch        json.RawMessage        `json:"patch"`        // RFC 6902 JSON Patch
	Quorum       float64                `json:"quorum"`       // 0.5 to 1.0, default 0.67
	VetoHolders  []string               `json:"veto_holders"` // DIDs
	Emergency    bool                   `json:"emergency"`
	Status       string                 `json:"status"`       // pending, accepted, rejected, expired
	Votes        map[string]string      `json:"votes"`        // DID -> accept/reject/abstain
	CreatedAt    time.Time              `json:"created_at"`
	ResolvedAt   *time.Time             `json:"resolved_at,omitempty"`
	Authorizers  []string               `json:"authorizers"`  // DIDs of current governance body
}

// Vote records a vote on a proposal
func (p *Proposal) Vote(voterDID, vote string) error {
	if p.Status != "pending" {
		return ErrAlreadyResolved
	}

	// Check if voter is authorized
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

	p.Votes[voterDID] = vote
	return nil
}

// Resolve evaluates the proposal and updates status
func (p *Proposal) Resolve(now time.Time) error {
	if p.Status != "pending" {
		return ErrAlreadyResolved
	}

	// Check veto holders first
	for _, vetoDID := range p.VetoHolders {
		vote, voted := p.Votes[vetoDID]
		if voted && vote == "reject" {
			p.Status = "rejected"
			p.ResolvedAt = &now
			return ErrVetoBlocked
		}
	}

	// Count accept votes from authorizers
	acceptCount := 0
	totalAuthorizers := len(p.Authorizers)
	for _, a := range p.Authorizers {
		if v, ok := p.Votes[a]; ok && v == "accept" {
			acceptCount++
		}
	}

	// Determine required quorum
	requiredQuorum := p.Quorum
	if requiredQuorum == 0 {
		requiredQuorum = 0.67 // default
	}
	if p.Emergency {
		requiredQuorum = 0.50 // emergency path: 50%
	}

	// Check quorum
	achievedQuorum := float64(acceptCount) / float64(totalAuthorizers)
	if achievedQuorum >= requiredQuorum {
		p.Status = "accepted"
	} else {
		p.Status = "rejected"
		return ErrQuorumNotMet
	}

	p.ResolvedAt = &now
	return nil
}

// GovernanceEvent records the outcome of a proposal
type GovernanceEvent struct {
	EventID     string    `json:"event_id"`     // CID
	ProposalID  string    `json:"proposal_id"`
	Outcome     string    `json:"outcome"`      // accepted, rejected
	AppliedAt   time.Time `json:"applied_at"`
	AppliedBy   string    `json:"applied_by"`   // DID of executor
	PatchHash   string    `json:"patch_hash"`   // SHA-256 of applied patch
}

// ApplyPatch applies the JSON patch to the governance policy
// Returns the new policy and an event recording the application
func (p *Proposal) ApplyPatch(currentPolicy json.RawMessage, executorDID string) (json.RawMessage, *GovernanceEvent, error) {
	if p.Status != "accepted" {
		return nil, nil, errors.New("governance: proposal not accepted")
	}

	// In practice, would use a JSON Patch library (rfc6902)
	// For now, return the patch as the new policy (simplified)
	newPolicy := p.Patch

	event := &GovernanceEvent{
		EventID:    "cid:placeholder", // Would compute CID of event
		ProposalID: p.ProposalID,
		Outcome:    "accepted",
		AppliedAt:  time.Now().UTC(),
		AppliedBy:  executorDID,
		PatchHash:  "sha256:placeholder", // Would compute hash of patch
	}

	return newPolicy, event, nil
}
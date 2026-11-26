// Authorization verification logic for Spherepop kernel
// Implements the head-check rule: authorization.head MUST match kernel's current head at collapse time

package authorization

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrInvalidSignature   = errors.New("authorization: invalid signature")
	ErrHeadMismatch       = errors.New("authorization: head does not match kernel current head")
	ErrExpired            = errors.New("authorization: expired")
	ErrNotYetValid        = errors.New("authorization: not yet valid")
	ErrInvalidSubject     = errors.New("authorization: invalid subject format")
	ErrNonceReused        = errors.New("authorization: nonce already used")
	ErrInvalidCapability  = errors.New("authorization: invalid capability for operation")
)

// Authorization represents a capability-based authorization held against a head
type Authorization struct {
	AuthorizationID string    `json:"authorization_id"`
	Subject         string    `json:"subject"`
	Capability      string    `json:"capability"`
	Scope           string    `json:"scope"`
	Head            string    `json:"head"`
	IssuedAt        time.Time `json:"issued_at"`
	ExpiresAt       time.Time `json:"expires_at"`
	Signature       Signature `json:"signature"`
	Nonce           string    `json:"nonce"`
}

// Signature is an Ed25519 signature over canonical authorization JSON
type Signature struct {
	Algorithm  string `json:"algorithm"`
	PublicKey  string `json:"public_key"`  // base64
	Value      string `json:"value"`       // base64
}

// Verify checks the authorization signature, expiry, head match, and nonce uniqueness
func (a *Authorization) Verify(currentHead string, usedNonces map[string]bool) error {
	// Check nonce uniqueness
	if usedNonces[a.Nonce] {
		return ErrNonceReused
	}

	// Check expiry
	now := time.Now().UTC()
	if now.Before(a.IssuedAt) {
		return ErrNotYetValid
	}
	if now.After(a.ExpiresAt) {
		return ErrExpired
	}

	// Check head match (THE critical rule from the protocol)
	if a.Head != currentHead {
		return ErrHeadMismatch
	}

	// Verify signature
	canonical := a.canonicalJSON()
	pubKey, err := base64.StdEncoding.DecodeString(a.Signature.PublicKey)
	if err != nil {
		return err
	}
	sig, err := base64.StdEncoding.DecodeString(a.Signature.Value)
	if err != nil {
		return err
	}
	if !ed25519.Verify(pubKey, canonical, sig) {
		return ErrInvalidSignature
	}

	return nil
}

// canonicalJSON returns the authorization JSON with signature field omitted,
// using a deterministic key order for reproducible signing
func (a *Authorization) canonicalJSON() []byte {
	// Create a copy without signature for canonicalization
	type canonicalAuth struct {
		AuthorizationID string    `json:"authorization_id"`
		Subject         string    `json:"subject"`
		Capability      string    `json:"capability"`
		Scope           string    `json:"scope"`
		Head            string    `json:"head"`
		IssuedAt        time.Time `json:"issued_at"`
		ExpiresAt       time.Time `json:"expires_at"`
		Nonce           string    `json:"nonce"`
	}
	ca := canonicalAuth{
		AuthorizationID: a.AuthorizationID,
		Subject:         a.Subject,
		Capability:      a.Capability,
		Scope:           a.Scope,
		Head:            a.Head,
		IssuedAt:        a.IssuedAt,
		ExpiresAt:       a.ExpiresAt,
		Nonce:           a.Nonce,
	}
	data, _ := json.Marshal(ca)
	return data
}

// CapabilityForOperation maps kernel operations to required capabilities
func CapabilityForOperation(op string) (string, error) {
	switch op {
	case "pop":
		return "pop:sphere", nil
	case "bind":
		return "pop:sphere", nil // bind is part of pop in the protocol
	case "transform":
		return "pop:sphere", nil // transform is internal to sphere
	case "verify":
		return "verify:evidence", nil
	case "collapse":
		return "commit:sphere", nil
	case "refuse":
		return "pop:sphere", nil
	case "govern":
		return "govern:policy", nil
	default:
		return "", ErrInvalidCapability
	}
}
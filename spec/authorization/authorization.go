// Authorization verification logic for Spherepop kernel (Protocol §5.3).
// Enforces the head-check rule: authorization.head MUST match the kernel's
// current event head at collapse time, backed by Ed25519 signature verification
// and single-use replay-nonce tracking.

package authorization

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"time"
)

var (
	ErrInvalidSignature  = errors.New("authorization: invalid signature")
	ErrHeadMismatch      = errors.New("authorization: head does not match kernel current head")
	ErrExpired           = errors.New("authorization: expired")
	ErrNotYetValid       = errors.New("authorization: not yet valid")
	ErrInvalidSubject    = errors.New("authorization: invalid subject format")
	ErrNonceReused       = errors.New("authorization: nonce missing, malformed, or already used")
	ErrInvalidCapability = errors.New("authorization: invalid capability for operation")
)

var (
	subjectPattern = regexp.MustCompile(`^(did:key:z[a-zA-Z0-9]+|github:[a-zA-Z0-9_-]+/[a-zA-Z0-9_-]+#(maintainers|admins|owners))$`)
	noncePattern   = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

// Authorization represents a capability-based authorization bound to an event head.
type Authorization struct {
	AuthorizationID string    `json:"authorization_id"`
	Subject         string    `json:"subject"`
	Capability      string    `json:"capability"`
	Scope           string    `json:"scope"`
	Head            string    `json:"head"`
	IssuedAt        time.Time `json:"issued_at"`
	ExpiresAt       time.Time `json:"expires_at"`
	Signature       Signature `json:"signature"`
	Nonce           string    `json:"nonce,omitempty"`
}

// Signature is an Ed25519 signature over canonical authorization JSON.
type Signature struct {
	Algorithm string `json:"algorithm"`
	PublicKey string `json:"public_key"` // base64
	Value     string `json:"value"`      // base64
}

// Verify checks subject syntax, nonce uniqueness, validity window against
// time.Now().UTC(), head equality, and Ed25519 cryptographic signature.
func (a *Authorization) Verify(currentHead string, usedNonces map[string]bool) error {
	return a.VerifyAt(currentHead, usedNonces, time.Now().UTC())
}

// VerifyAt performs deterministic verification at a caller-supplied timestamp.
// If verification succeeds and usedNonces is non-nil, a.Nonce is recorded as consumed.
func (a *Authorization) VerifyAt(currentHead string, usedNonces map[string]bool, now time.Time) error {
	if a == nil {
		return ErrInvalidSignature
	}
	if !subjectPattern.MatchString(a.Subject) {
		return ErrInvalidSubject
	}
	switch a.Capability {
	case "commit:sphere", "pop:sphere", "verify:evidence", "govern:policy":
	default:
		return ErrInvalidCapability
	}
	if a.Nonce != "" {
		if !noncePattern.MatchString(a.Nonce) {
			return ErrNonceReused
		}
		if usedNonces != nil && usedNonces[a.Nonce] {
			return ErrNonceReused
		}
	} else if usedNonces != nil {
		return ErrNonceReused
	}

	if now.Before(a.IssuedAt) {
		return ErrNotYetValid
	}
	if !now.Before(a.ExpiresAt) {
		return ErrExpired
	}
	if a.Head != currentHead {
		return ErrHeadMismatch
	}

	if a.Signature.Algorithm != "ed25519" {
		return ErrInvalidSignature
	}
	pubKey, err := base64.StdEncoding.DecodeString(a.Signature.PublicKey)
	if err != nil || len(pubKey) != ed25519.PublicKeySize {
		return ErrInvalidSignature
	}
	sig, err := base64.StdEncoding.DecodeString(a.Signature.Value)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return ErrInvalidSignature
	}
	if !ed25519.Verify(ed25519.PublicKey(pubKey), a.canonicalJSON(), sig) {
		return ErrInvalidSignature
	}

	if usedNonces != nil && a.Nonce != "" {
		usedNonces[a.Nonce] = true
	}
	return nil
}

// VerifyForOperation checks both operation-capability alignment and full VerifyAt invariants.
func (a *Authorization) VerifyForOperation(op, currentHead string, usedNonces map[string]bool, now time.Time) error {
	wantCap, err := CapabilityForOperation(op)
	if err != nil {
		return err
	}
	if a == nil || a.Capability != wantCap {
		return ErrInvalidCapability
	}
	return a.VerifyAt(currentHead, usedNonces, now)
}

// ComputeID computes a deterministic SHA-256 CID over canonical authorization fields.
func (a *Authorization) ComputeID() string {
	sum := sha256.Sum256(a.canonicalJSON())
	return "cid:" + hex.EncodeToString(sum[:])
}

// Sign populates AuthorizationID (if empty) and signs canonicalJSON() with priv.
func (a *Authorization) Sign(priv ed25519.PrivateKey) error {
	if len(priv) != ed25519.PrivateKeySize {
		return ErrInvalidSignature
	}
	if a.AuthorizationID == "" {
		a.AuthorizationID = a.ComputeID()
	}
	pub := priv.Public().(ed25519.PublicKey)
	sig := ed25519.Sign(priv, a.canonicalJSON())
	a.Signature = Signature{
		Algorithm: "ed25519",
		PublicKey: base64.StdEncoding.EncodeToString(pub),
		Value:     base64.StdEncoding.EncodeToString(sig),
	}
	return nil
}

func (a *Authorization) canonicalJSON() []byte {
	type canonicalAuth struct {
		AuthorizationID string    `json:"authorization_id"`
		Subject         string    `json:"subject"`
		Capability      string    `json:"capability"`
		Scope           string    `json:"scope"`
		Head            string    `json:"head"`
		IssuedAt        time.Time `json:"issued_at"`
		ExpiresAt       time.Time `json:"expires_at"`
		Nonce           string    `json:"nonce,omitempty"`
	}
	ca := canonicalAuth{
		AuthorizationID: a.AuthorizationID,
		Subject:         a.Subject,
		Capability:      a.Capability,
		Scope:           a.Scope,
		Head:            a.Head,
		IssuedAt:        a.IssuedAt.UTC(),
		ExpiresAt:       a.ExpiresAt.UTC(),
		Nonce:           a.Nonce,
	}
	data, _ := json.Marshal(ca)
	return data
}

// CapabilityForOperation maps kernel operations to required capabilities.
func CapabilityForOperation(op string) (string, error) {
	switch op {
	case "pop", "bind", "transform", "refuse":
		return "pop:sphere", nil
	case "verify":
		return "verify:evidence", nil
	case "collapse":
		return "commit:sphere", nil
	case "govern":
		return "govern:policy", nil
	default:
		return "", ErrInvalidCapability
	}
}

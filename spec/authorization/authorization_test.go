package authorization

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"os"
	"testing"
	"time"
)

func makeTestAuth(t *testing.T) (*Authorization, ed25519.PrivateKey) {
	t.Helper()
	seed := sha256.Sum256([]byte("authorization-unit-test-seed"))
	priv := ed25519.NewKeyFromSeed(seed[:])
	a := &Authorization{
		Subject:    "did:key:z6MkhaXgBZDvotDkL5257faiztiGiC2QtKLGpbnnEGta2doK",
		Capability: "commit:sphere",
		Scope:      "project:cid:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Head:       "event:cid:fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210",
		IssuedAt:   time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC),
		ExpiresAt:  time.Date(2026, 12, 26, 0, 0, 0, 0, time.UTC),
		Nonce:      "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}
	if err := a.Sign(priv); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return a, priv
}

func TestVerifyHeadExpiryNonceAndSignature(t *testing.T) {
	a, _ := makeTestAuth(t)
	now := time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC)
	nonces := make(map[string]bool)

	if err := a.VerifyForOperation("collapse", a.Head, nonces, now); err != nil {
		t.Fatalf("valid authorization rejected: %v", err)
	}
	// Replay with same nonce must fail
	if err := a.VerifyAt(a.Head, nonces, now); !errors.Is(err, ErrNonceReused) {
		t.Fatalf("expected ErrNonceReused on replay, got %v", err)
	}

	// Head mismatch must fail without consuming a fresh nonce
	a2, _ := makeTestAuth(t)
	a2.Nonce = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	a2.AuthorizationID = ""
	seed := sha256.Sum256([]byte("authorization-unit-test-seed"))
	_ = a2.Sign(ed25519.NewKeyFromSeed(seed[:]))
	if err := a2.VerifyAt("event:cid:wronghead", nonces, now); !errors.Is(err, ErrHeadMismatch) {
		t.Fatalf("expected ErrHeadMismatch, got %v", err)
	}
	if nonces[a2.Nonce] {
		t.Fatal("failed verification must not mark nonce as used")
	}

	// Expiry & NotYetValid
	if err := a2.VerifyAt(a2.Head, nonces, a2.IssuedAt.Add(-time.Second)); !errors.Is(err, ErrNotYetValid) {
		t.Fatalf("expected ErrNotYetValid, got %v", err)
	}
	if err := a2.VerifyAt(a2.Head, nonces, a2.ExpiresAt); !errors.Is(err, ErrExpired) {
		t.Fatalf("expected ErrExpired at exact ExpiresAt, got %v", err)
	}

	// Malformed short public key must not panic in ed25519.Verify
	a3 := *a2
	a3.Signature.PublicKey = base64.StdEncoding.EncodeToString([]byte("short-key"))
	if err := a3.VerifyAt(a3.Head, nil, now); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("expected ErrInvalidSignature for short pubkey, got %v", err)
	}

	// Invalid subject
	a4 := *a2
	a4.Subject = "invalid-subject"
	if err := a4.VerifyAt(a4.Head, nil, now); !errors.Is(err, ErrInvalidSubject) {
		t.Fatalf("expected ErrInvalidSubject, got %v", err)
	}
}

func TestExampleCommitFixture(t *testing.T) {
	raw, err := os.ReadFile("example-commit.json")
	if err != nil {
		t.Fatalf("read example-commit.json: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("empty example-commit.json")
	}
}

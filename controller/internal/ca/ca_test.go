package ca

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsurePersistsAndReusesCA(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	first, err := Ensure(dir)
	if err != nil {
		t.Fatalf("Ensure() first error = %v", err)
	}
	if !strings.Contains(first.CAPEM(), "BEGIN CERTIFICATE") {
		t.Fatalf("CAPEM() = %q, want certificate PEM", first.CAPEM())
	}
	if _, err := Ensure(dir); err != nil {
		t.Fatalf("Ensure() second error = %v", err)
	}
	if _, err := Ensure(filepath.Join(dir, "nested")); err != nil {
		t.Fatalf("Ensure() nested error = %v", err)
	}
}

func TestSignSHA256DigestProducesVerifiableSignature(t *testing.T) {
	t.Parallel()

	authority, err := Ensure(t.TempDir())
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}

	digest := sha256.Sum256([]byte("strom-ota-payload"))
	signature, err := authority.SignSHA256Digest(digest[:])
	if err != nil {
		t.Fatalf("SignSHA256Digest() error = %v", err)
	}
	if len(signature) == 0 {
		t.Fatal("signature is empty")
	}

	block, _ := pem.Decode([]byte(authority.CAPEM()))
	if block == nil {
		t.Fatal("decode CAPEM failed")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("ParseCertificate() error = %v", err)
	}
	publicKey, ok := certificate.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		t.Fatalf("public key type = %T, want *ecdsa.PublicKey", certificate.PublicKey)
	}
	if !ecdsa.VerifyASN1(publicKey, digest[:], signature) {
		t.Fatal("signature verification failed")
	}
}

package secrets

import (
	"bytes"
	"strings"
	"testing"
)

func box(t *testing.T, b byte) *Box {
	t.Helper()
	x, err := New(bytes.Repeat([]byte{b}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return x
}

func TestSealOpenRoundTripAndBinding(t *testing.T) {
	b := box(t, 1)
	sealed, err := b.Seal("fake-hermes-key-0001", "binding-A")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("fake-hermes-key")) {
		t.Fatal("ciphertext contains plaintext")
	}
	if got, err := b.Open(sealed, "binding-A"); err != nil || got != "fake-hermes-key-0001" {
		t.Fatalf("round trip: %q %v", got, err)
	}
	if _, err := b.Open(sealed, "binding-B"); err == nil {
		t.Fatal("blob moved to another row must not open")
	}
	if _, err := box(t, 2).Open(sealed, "binding-A"); err == nil {
		t.Fatal("wrong master key must not open")
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := b.Open(sealed, "binding-A"); err == nil {
		t.Fatal("tampered ciphertext must not open")
	}
}

func TestSealIsRandomisedAndErrorsCarryNoSecret(t *testing.T) {
	b := box(t, 1)
	a, _ := b.Seal("same", "x")
	c, _ := b.Seal("same", "x")
	if bytes.Equal(a, c) {
		t.Fatal("nonce reuse")
	}
	_, err := b.Open([]byte("short"), "x")
	if err == nil || strings.Contains(err.Error(), "same") {
		t.Fatalf("bad error: %v", err)
	}
}

func TestFingerprintStableAndKeyed(t *testing.T) {
	if box(t, 1).Fingerprint("k") != box(t, 1).Fingerprint("k") {
		t.Fatal("fingerprint not stable")
	}
	if box(t, 1).Fingerprint("k") == box(t, 2).Fingerprint("k") {
		t.Fatal("fingerprint must depend on master key")
	}
	if box(t, 1).Fingerprint("k") == box(t, 1).Fingerprint("j") {
		t.Fatal("collision")
	}
}

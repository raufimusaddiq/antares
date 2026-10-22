// Package secrets seals Hermes API keys for storage in SQLite.
//
// Keys are encrypted with AES-256-GCM under a subkey derived from the master
// key (ANTARES_SECRET_KEY). The master key lives only in runtime secrets, never
// in the database. A ciphertext is bound to its row through additional data,
// so one row's blob cannot be moved to another.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

// Version identifies the key and format used to seal. It is stored beside the
// ciphertext so a future rotation can tell which key opens which row.
const Version = 1

var errOpen = errors.New("secrets: cannot open ciphertext")

type Box struct {
	aead cipher.AEAD
	fp   []byte
}

func derive(master []byte, label string) []byte {
	m := hmac.New(sha256.New, master)
	m.Write([]byte(label))
	return m.Sum(nil)
}

// New builds a Box from a 32-byte master key.
func New(master []byte) (*Box, error) {
	if len(master) != 32 {
		return nil, errors.New("secrets: master key must be 32 bytes")
	}
	blk, err := aes.NewCipher(derive(master, "antares/aead/v1"))
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(blk)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead, fp: derive(master, "antares/fingerprint/v1")}, nil
}

// Seal encrypts plaintext; aad ties it to its row (e.g. the binding id).
func (b *Box) Seal(plaintext, aad string) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return b.aead.Seal(nonce, nonce, []byte(plaintext), []byte(aad)), nil
}

// Open decrypts a Seal result. The error never carries key or plaintext data.
func (b *Box) Open(sealed []byte, aad string) (string, error) {
	n := b.aead.NonceSize()
	if len(sealed) < n+b.aead.Overhead() {
		return "", errOpen
	}
	pt, err := b.aead.Open(nil, sealed[:n], sealed[n:], []byte(aad))
	if err != nil {
		return "", errOpen
	}
	return string(pt), nil
}

// Fingerprint is a keyed hash of a credential. A UNIQUE column on it makes the
// database refuse a key reused across users without storing anything useful.
func (b *Box) Fingerprint(plaintext string) string {
	m := hmac.New(sha256.New, b.fp)
	m.Write([]byte(plaintext))
	return hex.EncodeToString(m.Sum(nil))
}

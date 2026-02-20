// Package crypto provides NaCl secretbox encryption for suffuse messages.
//
// A 32-byte symmetric key is derived from the shared token using HKDF-SHA256.
// Every message is encrypted with a random 24-byte nonce prepended to the
// ciphertext:
//
//	[ 24-byte nonce ][ ciphertext ]
//
// If the token is empty, callers should not use this package — the wire layer
// passes a nil key and messages are sent as plain JSON.
package crypto

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
	"golang.org/x/crypto/nacl/secretbox"
)

const (
	keySize   = 32
	nonceSize = 24
)

var hkdfInfo = []byte("suffuse-v1")

// DeriveKey derives a 32-byte NaCl secretbox key from a token string using
// HKDF-SHA256. Both sides must use the same token to derive the same key.
func DeriveKey(token string) (*[keySize]byte, error) {
	h := hkdf.New(sha256.New, []byte(token), nil, hkdfInfo)
	var key [keySize]byte
	if _, err := io.ReadFull(h, key[:]); err != nil {
		return nil, fmt.Errorf("key derivation: %w", err)
	}
	return &key, nil
}

// Seal encrypts plaintext with key, prepending a random nonce.
// Returns nonce+ciphertext.
func Seal(plaintext []byte, key *[keySize]byte) ([]byte, error) {
	var nonce [nonceSize]byte
	if _, err := io.ReadFull(rand.Reader, nonce[:]); err != nil {
		return nil, fmt.Errorf("nonce generation: %w", err)
	}
	ct := secretbox.Seal(nonce[:], plaintext, &nonce, key)
	return ct, nil
}

// Open decrypts ciphertext (nonce+ciphertext) with key.
func Open(ciphertext []byte, key *[keySize]byte) ([]byte, error) {
	if len(ciphertext) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short")
	}
	var nonce [nonceSize]byte
	copy(nonce[:], ciphertext[:nonceSize])
	plain, ok := secretbox.Open(nil, ciphertext[nonceSize:], &nonce, key)
	if !ok {
		return nil, fmt.Errorf("decryption failed (wrong token?)")
	}
	return plain, nil
}

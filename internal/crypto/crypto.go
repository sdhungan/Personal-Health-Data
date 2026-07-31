// Package crypto provides the encryption primitives healthd uses to keep
// everything under its root opaque at rest: the OAuth token file and the
// working copy of the SQLite database are both encrypted as whole-file
// blobs with the same AES-256-GCM key, derived once from a passphrase via
// Argon2id at "healthd db init" time (see ARCHITECTURE.md §6).
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
)

// KeySize is the AES-256 key length in bytes.
const KeySize = 32

// nonceSize is the standard GCM nonce length.
const nonceSize = 12

// Key is a derived AES-256 key.
type Key [KeySize]byte

// Encrypt seals plaintext with AES-256-GCM under key, returning a blob with
// a fresh random nonce prepended so Decrypt is self-contained.
func Encrypt(key Key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("creating cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("creating GCM: %w", err)
	}

	nonce := make([]byte, nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generating nonce: %w", err)
	}

	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt reverses Encrypt. It returns an error if the blob is malformed or
// the key/data don't match (authentication failure).
func Decrypt(key Key, blob []byte) ([]byte, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("creating cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("creating GCM: %w", err)
	}

	if len(blob) < nonceSize {
		return nil, errors.New("ciphertext too short")
	}
	nonce, ciphertext := blob[:nonceSize], blob[nonceSize:]

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypting: %w", err)
	}
	return plaintext, nil
}

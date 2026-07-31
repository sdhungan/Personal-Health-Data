package crypto

import (
	"crypto/rand"
	"fmt"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters. Chosen for a once-at-init cost (this runs once when
// "healthd db init" creates the key, never on the hot path), sized above
// OWASP's baseline recommendation (m=19MiB,t=2,p=1) for extra headroom.
const (
	argonTimeCost  = 3
	argonMemoryKiB = 64 * 1024 // 64 MiB
	argonThreads   = 4
	saltSize       = 16
)

// GenerateSalt returns a fresh random salt for DeriveKey. It is not secret —
// only unique — and is stored alongside the derived key so the same
// passphrase can reproduce the same key later if the key file is ever lost.
func GenerateSalt() ([]byte, error) {
	salt := make([]byte, saltSize)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("generating salt: %w", err)
	}
	return salt, nil
}

// DeriveKey derives an AES-256 key from a passphrase and salt via Argon2id.
func DeriveKey(passphrase string, salt []byte) Key {
	raw := argon2.IDKey([]byte(passphrase), salt, argonTimeCost, argonMemoryKiB, argonThreads, KeySize)
	var key Key
	copy(key[:], raw)
	return key
}

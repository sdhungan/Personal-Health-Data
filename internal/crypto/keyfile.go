package crypto

import (
	"encoding/json"
	"fmt"
	"os"
)

// keyFile is the on-disk format of keys/db.key. The salt is kept alongside
// the derived key (not secret, just unique) so the same passphrase can
// regenerate an identical key if this file is ever lost; day-to-day the key
// itself is what gets read, so unattended runs never need the passphrase
// again after init. []byte fields marshal as base64 automatically.
type keyFile struct {
	Salt []byte `json:"salt"`
	Key  []byte `json:"key"`
}

// GenerateAndSaveKey derives a key from passphrase with a fresh salt and
// writes both to path with owner-only permissions.
func GenerateAndSaveKey(passphrase, path string) (Key, error) {
	salt, err := GenerateSalt()
	if err != nil {
		return Key{}, err
	}
	key := DeriveKey(passphrase, salt)

	kf := keyFile{Salt: salt, Key: key[:]}
	data, err := json.Marshal(kf)
	if err != nil {
		return Key{}, fmt.Errorf("marshaling key file: %w", err)
	}

	if err := os.WriteFile(path, data, 0o600); err != nil {
		return Key{}, fmt.Errorf("writing key file %s: %w", path, err)
	}
	// os.WriteFile only applies the given mode when creating a new file; if
	// path already existed with looser permissions, tighten it explicitly.
	if err := os.Chmod(path, 0o600); err != nil {
		return Key{}, fmt.Errorf("setting permissions on %s: %w", path, err)
	}

	return key, nil
}

// LoadKey reads a key file previously written by GenerateAndSaveKey.
func LoadKey(path string) (Key, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Key{}, fmt.Errorf("reading key file %s: %w", path, err)
	}

	var kf keyFile
	if err := json.Unmarshal(data, &kf); err != nil {
		return Key{}, fmt.Errorf("parsing key file %s: %w", path, err)
	}
	if len(kf.Key) != KeySize {
		return Key{}, fmt.Errorf("key file %s: key is %d bytes, want %d", path, len(kf.Key), KeySize)
	}

	var key Key
	copy(key[:], kf.Key)
	return key, nil
}

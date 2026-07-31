package crypto

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	var key Key
	copy(key[:], []byte("0123456789abcdef0123456789abcdef"))

	plaintext := []byte("this is definitely health data, not a public record")

	blob, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if string(blob) == string(plaintext) {
		t.Fatal("Encrypt returned plaintext unchanged")
	}

	got, err := Decrypt(key, blob)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(got) != string(plaintext) {
		t.Errorf("Decrypt = %q, want %q", got, plaintext)
	}
}

func TestDecryptWrongKeyFails(t *testing.T) {
	var key1, key2 Key
	copy(key1[:], []byte("0123456789abcdef0123456789abcdef"))
	copy(key2[:], []byte("zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"))

	blob, err := Encrypt(key1, []byte("secret"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	if _, err := Decrypt(key2, blob); err == nil {
		t.Error("Decrypt with wrong key: expected error, got none")
	}
}

func TestDecryptTruncatedBlobFails(t *testing.T) {
	var key Key
	if _, err := Decrypt(key, []byte("short")); err == nil {
		t.Error("Decrypt with truncated blob: expected error, got none")
	}
}

func TestDeriveKeyIsDeterministicPerSalt(t *testing.T) {
	salt, err := GenerateSalt()
	if err != nil {
		t.Fatalf("GenerateSalt: %v", err)
	}

	k1 := DeriveKey("correct horse battery staple", salt)
	k2 := DeriveKey("correct horse battery staple", salt)
	if k1 != k2 {
		t.Error("DeriveKey with same passphrase+salt produced different keys")
	}

	k3 := DeriveKey("a different passphrase", salt)
	if k1 == k3 {
		t.Error("DeriveKey with different passphrases produced the same key")
	}

	otherSalt, err := GenerateSalt()
	if err != nil {
		t.Fatalf("GenerateSalt: %v", err)
	}
	k4 := DeriveKey("correct horse battery staple", otherSalt)
	if k1 == k4 {
		t.Error("DeriveKey with different salts produced the same key")
	}
}

func TestGenerateAndSaveKeyThenLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.key")

	key, err := GenerateAndSaveKey("my passphrase", path)
	if err != nil {
		t.Fatalf("GenerateAndSaveKey: %v", err)
	}

	loaded, err := LoadKey(path)
	if err != nil {
		t.Fatalf("LoadKey: %v", err)
	}

	if key != loaded {
		t.Error("LoadKey did not return the key that GenerateAndSaveKey wrote")
	}
}

func TestLoadKeyRejectsBadFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.key")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := LoadKey(path); err == nil {
		t.Error("LoadKey with malformed file: expected error, got none")
	}
}

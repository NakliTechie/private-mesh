package crypto

import (
	"bytes"
	"testing"
)

func TestHKDFDeterministic(t *testing.T) {
	secret := []byte("master-key-material")
	salt := []byte("salt-bytes")
	info := []byte("namespace=list")

	a, err := DeriveKey(secret, salt, info)
	if err != nil {
		t.Fatalf("DeriveKey: %v", err)
	}
	b, err := DeriveKey(secret, salt, info)
	if err != nil {
		t.Fatalf("DeriveKey: %v", err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("HKDF should be deterministic for identical inputs")
	}
	if len(a) != KeySize {
		t.Fatalf("derived length: got %d, want %d", len(a), KeySize)
	}
}

func TestHKDFInfoSeparates(t *testing.T) {
	secret := []byte("master-key-material")
	salt := []byte("salt-bytes")

	a, _ := DeriveKey(secret, salt, []byte("namespace=list"))
	b, _ := DeriveKey(secret, salt, []byte("namespace=bahi"))
	if bytes.Equal(a, b) {
		t.Fatal("different info should yield different keys")
	}
}

func TestHKDFSaltSeparates(t *testing.T) {
	secret := []byte("master-key-material")
	info := []byte("namespace=list")

	a, _ := DeriveKey(secret, []byte("salt-a"), info)
	b, _ := DeriveKey(secret, []byte("salt-b"), info)
	if bytes.Equal(a, b) {
		t.Fatal("different salt should yield different keys")
	}
}

func TestArgon2idDeterministic(t *testing.T) {
	salt := []byte("0123456789abcdef")
	p := DefaultArgon2idParams()

	a := DeriveKeyArgon2id("correct horse battery staple", salt, p)
	b := DeriveKeyArgon2id("correct horse battery staple", salt, p)
	if !bytes.Equal(a, b) {
		t.Fatal("Argon2id should be deterministic for identical inputs")
	}
	if uint32(len(a)) != p.KeyLen {
		t.Fatalf("derived length: got %d, want %d", len(a), p.KeyLen)
	}
}

func TestArgon2idPassphraseSeparates(t *testing.T) {
	salt := []byte("0123456789abcdef")
	p := DefaultArgon2idParams()
	a := DeriveKeyArgon2id("pass-a", salt, p)
	b := DeriveKeyArgon2id("pass-b", salt, p)
	if bytes.Equal(a, b) {
		t.Fatal("different passphrases should yield different keys")
	}
}

func TestArgon2idSaltSeparates(t *testing.T) {
	p := DefaultArgon2idParams()
	a := DeriveKeyArgon2id("pass", []byte("salt-a-padded-16"), p)
	b := DeriveKeyArgon2id("pass", []byte("salt-b-padded-16"), p)
	if bytes.Equal(a, b) {
		t.Fatal("different salts should yield different keys")
	}
}

func TestDefaultArgon2idParamsMatchSpec(t *testing.T) {
	// fabric-spec-001-v1.0.md §"Fabric Identity File" sets these defaults for
	// envelope_type="passphrase-only".
	p := DefaultArgon2idParams()
	if p.Time != 3 {
		t.Errorf("Time: got %d, want 3", p.Time)
	}
	if p.Memory != 65536 {
		t.Errorf("Memory: got %d, want 65536", p.Memory)
	}
	if p.Threads != 4 {
		t.Errorf("Threads: got %d, want 4", p.Threads)
	}
	if p.KeyLen != KeySize {
		t.Errorf("KeyLen: got %d, want %d", p.KeyLen, KeySize)
	}
}

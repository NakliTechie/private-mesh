package crypto

import (
	"bytes"
	"testing"
)

func mustKey(t *testing.T) []byte {
	t.Helper()
	k, err := RandomBytes(KeySize)
	if err != nil {
		t.Fatalf("RandomBytes: %v", err)
	}
	return k
}

func mustNonce(t *testing.T) []byte {
	t.Helper()
	n, err := RandomNonce()
	if err != nil {
		t.Fatalf("RandomNonce: %v", err)
	}
	return n
}

func TestAEADRoundTrip(t *testing.T) {
	key := mustKey(t)
	nonce := mustNonce(t)
	plaintext := []byte("Bhai's vault payload")
	aad := []byte("namespace=list,stream=001")

	ct, err := Seal(key, nonce, plaintext, aad)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if len(ct) != len(plaintext)+TagSize {
		t.Fatalf("ciphertext length: got %d, want %d", len(ct), len(plaintext)+TagSize)
	}

	got, err := Open(key, nonce, ct, aad)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("plaintext mismatch: got %q, want %q", got, plaintext)
	}
}

func TestAEADWrongKeyFails(t *testing.T) {
	key1 := mustKey(t)
	key2 := mustKey(t)
	nonce := mustNonce(t)
	ct, err := Seal(key1, nonce, []byte("x"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(key2, nonce, ct, nil); err == nil {
		t.Fatal("Open with wrong key should fail")
	}
}

func TestAEADWrongNonceFails(t *testing.T) {
	key := mustKey(t)
	n1 := mustNonce(t)
	n2 := mustNonce(t)
	ct, err := Seal(key, n1, []byte("x"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(key, n2, ct, nil); err == nil {
		t.Fatal("Open with wrong nonce should fail")
	}
}

func TestAEADTamperedCiphertextFails(t *testing.T) {
	key := mustKey(t)
	nonce := mustNonce(t)
	ct, err := Seal(key, nonce, []byte("hello"), nil)
	if err != nil {
		t.Fatal(err)
	}
	ct[0] ^= 0x01
	if _, err := Open(key, nonce, ct, nil); err == nil {
		t.Fatal("Open with tampered ciphertext should fail")
	}
}

func TestAEADWrongAADFails(t *testing.T) {
	key := mustKey(t)
	nonce := mustNonce(t)
	ct, err := Seal(key, nonce, []byte("hello"), []byte("aad-a"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(key, nonce, ct, []byte("aad-b")); err == nil {
		t.Fatal("Open with wrong AAD should fail")
	}
}

func TestSealRejectsBadKeyAndNonce(t *testing.T) {
	if _, err := Seal(make([]byte, 16), make([]byte, NonceSize), nil, nil); err != ErrInvalidKeySize {
		t.Errorf("short key: got %v, want ErrInvalidKeySize", err)
	}
	if _, err := Seal(make([]byte, KeySize), make([]byte, 12), nil, nil); err != ErrInvalidNonceSize {
		t.Errorf("short nonce: got %v, want ErrInvalidNonceSize", err)
	}
}

func TestRandomNonceUniqueness(t *testing.T) {
	seen := map[string]struct{}{}
	for i := 0; i < 64; i++ {
		n, err := RandomNonce()
		if err != nil {
			t.Fatal(err)
		}
		if len(n) != NonceSize {
			t.Fatalf("len=%d, want %d", len(n), NonceSize)
		}
		if _, dup := seen[string(n)]; dup {
			t.Fatal("duplicate nonce — random source broken")
		}
		seen[string(n)] = struct{}{}
	}
}

package identity

import (
	"bytes"
	"crypto/ed25519"
	cryptorand "crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func sampleInner(t *testing.T) *InnerFIF {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(cryptorand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return NewInnerFIF(
		Principal{
			Type:        PrincipalHuman,
			ID:          "01JFXAMPLETESTULID000001",
			DisplayName: "Bhai",
			CreatedAt:   time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC),
		},
		KeyPair{
			Algorithm:  KeyAlgEd25519,
			PublicKey:  pub,
			PrivateKey: priv,
		},
	)
}

func TestFIFRoundTrip(t *testing.T) {
	inner := sampleInner(t)
	const pass = "correct horse battery staple"

	fif, err := NewFIF(pass, inner)
	if err != nil {
		t.Fatalf("NewFIF: %v", err)
	}

	var buf bytes.Buffer
	if err := fif.Serialize(&buf); err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("Serialize wrote nothing")
	}

	parsed, err := ParseFIF(&buf)
	if err != nil {
		t.Fatalf("ParseFIF: %v", err)
	}
	if got, want := parsed.EnvelopeType(), EnvelopePassphraseOnly; got != want {
		t.Fatalf("EnvelopeType: got %q, want %q", got, want)
	}
	if parsed.IsUnlocked() {
		t.Fatal("freshly parsed FIF should be locked")
	}

	if err := parsed.Unlock(pass); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	if parsed.Inner == nil {
		t.Fatal("Inner is nil after Unlock")
	}
	if parsed.Inner.Principal.DisplayName != "Bhai" {
		t.Errorf("DisplayName: got %q", parsed.Inner.Principal.DisplayName)
	}
	if !bytes.Equal(parsed.Inner.RootKeypair.PublicKey, inner.RootKeypair.PublicKey) {
		t.Error("root public key mismatch")
	}
	if !bytes.Equal(parsed.Inner.RootKeypair.PrivateKey, inner.RootKeypair.PrivateKey) {
		t.Error("root private key mismatch")
	}
}

func TestFIFWrongPassphraseFails(t *testing.T) {
	inner := sampleInner(t)
	fif, err := NewFIF("right-passphrase", inner)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := fif.Serialize(&buf); err != nil {
		t.Fatal(err)
	}

	parsed, err := ParseFIF(&buf)
	if err != nil {
		t.Fatal(err)
	}
	err = parsed.Unlock("wrong-passphrase")
	if err == nil {
		t.Fatal("Unlock with wrong passphrase should fail")
	}
	if AsCode(err) != ErrFIFAuth.Code {
		t.Errorf("error code: got %q, want %q", AsCode(err), ErrFIFAuth.Code)
	}
	if !errors.Is(err, ErrFIFAuth) {
		t.Errorf("errors.Is(err, ErrFIFAuth) should be true")
	}
}

func TestFIFTamperedHeaderFails(t *testing.T) {
	// Verifies that the envelope header is bound to the body via AAD: if we
	// tweak any byte of the on-wire header, MAC verification must fail.
	inner := sampleInner(t)
	fif, err := NewFIF("pass", inner)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := fif.Serialize(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.Bytes()

	// Locate the "envelope_type" value byte and flip one bit inside the JSON.
	// We pick a byte safely inside the JSON body, not in the length prefix.
	idx := bytes.Index(out, []byte("passphrase-only"))
	if idx < 0 {
		t.Fatal("could not find envelope_type marker in serialized output")
	}
	out[idx] ^= 0x01 // mutate one byte

	parsed, err := ParseFIF(bytes.NewReader(out))
	if err != nil {
		// Parse may already fail because the mutated JSON no longer matches a
		// known envelope_type. That's an acceptable failure mode (it's still
		// refused; the user gets fif_format or fif_envelope_unsupported).
		if AsCode(err) != ErrFIFFormat.Code && AsCode(err) != ErrFIFEnvelopeUnsupported.Code {
			t.Fatalf("unexpected error code: %q", AsCode(err))
		}
		return
	}
	if err := parsed.Unlock("pass"); err == nil {
		t.Fatal("Unlock with tampered header should fail")
	} else if AsCode(err) != ErrFIFAuth.Code {
		t.Errorf("error code: got %q, want %q", AsCode(err), ErrFIFAuth.Code)
	}
}

func TestFIFRefusesReservedEnvelopeTypes(t *testing.T) {
	// Per the forward-compat hook: v1.0 must refuse reserved envelope types
	// with the specific fif_envelope_unsupported code, not a generic parse fail.
	for _, et := range []EnvelopeType{EnvelopeShamirShares, EnvelopeDeviceQuorum, EnvelopeSocialRecovery} {
		t.Run(string(et), func(t *testing.T) {
			fif := envelopeWithType(t, et)
			_, err := ParseFIF(bytes.NewReader(fif))
			if err == nil {
				t.Fatal("ParseFIF should refuse reserved envelope_type")
			}
			if AsCode(err) != ErrFIFEnvelopeUnsupported.Code {
				t.Errorf("error code: got %q, want %q", AsCode(err), ErrFIFEnvelopeUnsupported.Code)
			}
			if !errors.Is(err, ErrFIFEnvelopeUnsupported) {
				t.Errorf("errors.Is(err, ErrFIFEnvelopeUnsupported) should be true")
			}
		})
	}
}

func TestFIFRefusesUnknownEnvelopeType(t *testing.T) {
	fif := envelopeWithType(t, EnvelopeType("bogus-envelope"))
	_, err := ParseFIF(bytes.NewReader(fif))
	if err == nil {
		t.Fatal("ParseFIF should refuse unknown envelope_type")
	}
	if AsCode(err) != ErrFIFEnvelopeUnsupported.Code {
		t.Errorf("error code: got %q, want %q", AsCode(err), ErrFIFEnvelopeUnsupported.Code)
	}
}

func TestFIFRefusesUnknownFormat(t *testing.T) {
	// A header with format="fif/9.9" should fail parse.
	hdrBytes := mustHeader(t, "fif/9.9", EnvelopePassphraseOnly)
	out := assemble(hdrBytes, make([]byte, 32))
	_, err := ParseFIF(bytes.NewReader(out))
	if err == nil {
		t.Fatal("ParseFIF should refuse unknown format")
	}
	if AsCode(err) != ErrFIFFormat.Code {
		t.Errorf("error code: got %q, want %q", AsCode(err), ErrFIFFormat.Code)
	}
}

func TestFIFLockClearsState(t *testing.T) {
	inner := sampleInner(t)
	fif, err := NewFIF("pass", inner)
	if err != nil {
		t.Fatal(err)
	}
	if !fif.IsUnlocked() {
		t.Fatal("freshly-created FIF should be unlocked")
	}
	fif.Lock()
	if fif.IsUnlocked() {
		t.Fatal("after Lock, FIF should be locked")
	}
	if fif.Inner != nil {
		t.Fatal("after Lock, Inner should be nil")
	}
	var buf bytes.Buffer
	if err := fif.Serialize(&buf); !errors.Is(err, ErrIdentityLocked) {
		t.Errorf("Serialize after Lock: got %v, want ErrIdentityLocked", err)
	}
}

func TestFIFShortBodyRejected(t *testing.T) {
	// Build a valid header followed by a 1-byte body (shorter than AEAD tag).
	hdrBytes := mustHeader(t, FIFFormat, EnvelopePassphraseOnly)
	out := assemble(hdrBytes, []byte{0x00})
	_, err := ParseFIF(bytes.NewReader(out))
	if err == nil || AsCode(err) != ErrFIFFormat.Code {
		t.Fatalf("got %v, want fif_format error", err)
	}
}

func TestFIFTruncatedHeaderRejected(t *testing.T) {
	// Length prefix says 1000 bytes but we provide only 10.
	out := append([]byte{0, 0, 3, 0xe8}, []byte("not enough")...)
	_, err := ParseFIF(bytes.NewReader(out))
	if err == nil || AsCode(err) != ErrFIFFormat.Code {
		t.Fatalf("got %v, want fif_format error", err)
	}
}

// envelopeWithType produces a syntactically valid FIF whose envelope_type is
// the given value. The body is junk; parsing should fail before any decrypt.
func envelopeWithType(t *testing.T, et EnvelopeType) []byte {
	t.Helper()
	hdrBytes := mustHeader(t, FIFFormat, et)
	return assemble(hdrBytes, make([]byte, 32))
}

func mustHeader(t *testing.T, format string, et EnvelopeType) []byte {
	t.Helper()
	salt := bytes.Repeat([]byte{1}, 16)
	nonce := bytes.Repeat([]byte{2}, 24)
	hdr := envelopeHeader{
		Format:       format,
		EnvelopeType: et,
		EnvelopeParams: passphraseOnlyEnvParams{
			KDF:       "argon2id",
			KDFParams: argon2idParams{MCost: 65536, TCost: 3, Parallelism: 4},
			Salt:      salt,
			Nonce:     nonce,
		},
	}
	b, err := json.Marshal(hdr)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func assemble(hdrJSON, body []byte) []byte {
	out := make([]byte, 4+len(hdrJSON)+len(body))
	binary.BigEndian.PutUint32(out[:4], uint32(len(hdrJSON)))
	copy(out[4:], hdrJSON)
	copy(out[4+len(hdrJSON):], body)
	return out
}

func TestSerializedFIFContainsHeaderFormat(t *testing.T) {
	// Sanity check: ensure the on-wire header is JSON containing the format
	// string, so other implementations can inspect it without unlocking.
	fif, err := NewFIF("pass", sampleInner(t))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := fif.Serialize(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"format":"fif/1.0"`) {
		t.Fatal(`expected "format":"fif/1.0" in on-wire header`)
	}
	if !strings.Contains(buf.String(), `"envelope_type":"passphrase-only"`) {
		t.Fatal(`expected "envelope_type":"passphrase-only" in on-wire header`)
	}
}

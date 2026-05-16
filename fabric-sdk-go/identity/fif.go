package identity

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"

	"github.com/NakliTechie/private-mesh/fabric-sdk-go/crypto"
)

// FIFFormat is the format string carried by the FIF envelope header.
const FIFFormat = "fif/1.0"

// EnvelopeType is the user-recovery envelope wrapping the inner FIF.
type EnvelopeType string

const (
	EnvelopePassphraseOnly EnvelopeType = "passphrase-only"
	// Reserved for v1.x. v1.0 refuses these with ErrFIFEnvelopeUnsupported.
	EnvelopeShamirShares   EnvelopeType = "shamir-shares"
	EnvelopeDeviceQuorum   EnvelopeType = "device-quorum"
	EnvelopeSocialRecovery EnvelopeType = "social-recovery"
)

// reservedEnvelopeTypes are valid envelope_type values that v1.0 implementations
// must explicitly refuse with ErrFIFEnvelopeUnsupported (handoff §"Forward-compatibility hooks").
var reservedEnvelopeTypes = map[EnvelopeType]struct{}{
	EnvelopeShamirShares:   {},
	EnvelopeDeviceQuorum:   {},
	EnvelopeSocialRecovery: {},
}

// envelopeHeader is the JSON shape of the envelope's leading header.
type envelopeHeader struct {
	Format         string                  `json:"format"`
	EnvelopeType   EnvelopeType            `json:"envelope_type"`
	EnvelopeParams passphraseOnlyEnvParams `json:"envelope_params"`
}

// passphraseOnlyEnvParams matches envelope_params for envelope_type="passphrase-only".
type passphraseOnlyEnvParams struct {
	KDF       string         `json:"kdf"`
	KDFParams argon2idParams `json:"kdf_params"`
	Salt      []byte         `json:"salt"`
	Nonce     []byte         `json:"nonce"`
}

// argon2idParams is the JSON shape of envelope_params.kdf_params.
type argon2idParams struct {
	MCost       uint32 `json:"m_cost"`
	TCost       uint32 `json:"t_cost"`
	Parallelism uint8  `json:"parallelism"`
}

func (p argon2idParams) toCrypto() crypto.Argon2idParams {
	return crypto.Argon2idParams{
		Time:    p.TCost,
		Memory:  p.MCost,
		Threads: p.Parallelism,
		KeyLen:  crypto.KeySize,
	}
}

func argon2idParamsFrom(p crypto.Argon2idParams) argon2idParams {
	return argon2idParams{
		MCost:       p.Memory,
		TCost:       p.Time,
		Parallelism: p.Threads,
	}
}

// FIF is a parsed Fabric Identity File. After Unlock or NewFIF, Inner is populated.
type FIF struct {
	// header holds the parsed envelope header.
	header envelopeHeader
	// headerBytes is the raw on-wire envelope-header (4-byte length + JSON).
	// It is used as the AEAD AAD so a tampered header invalidates the MAC.
	headerBytes []byte
	// body is the encrypted body || tag (still encrypted when freshly parsed).
	body []byte
	// key is the derived envelope key, cached after Unlock so Serialize can
	// re-encrypt without re-running the KDF. Zeroed by Lock.
	key []byte
	// Inner is the decrypted body. Nil until Unlock or NewFIF.
	Inner *InnerFIF
}

// EnvelopeType returns the envelope type of this FIF (without unlocking).
func (f *FIF) EnvelopeType() EnvelopeType { return f.header.EnvelopeType }

// IsUnlocked reports whether Inner is populated and the envelope key is cached.
func (f *FIF) IsUnlocked() bool { return f.Inner != nil && len(f.key) == crypto.KeySize }

// NewFIF builds an unlocked FIF in memory using a fresh passphrase-only envelope.
// Call Serialize to write it out.
func NewFIF(passphrase string, inner *InnerFIF) (*FIF, error) {
	if inner == nil {
		return nil, fmt.Errorf("identity.NewFIF: inner is nil")
	}
	salt, err := crypto.RandomSalt()
	if err != nil {
		return nil, err
	}
	nonce, err := crypto.RandomNonce()
	if err != nil {
		return nil, err
	}
	kdfParams := crypto.DefaultArgon2idParams()
	key := crypto.DeriveKeyArgon2id(passphrase, salt, kdfParams)

	return &FIF{
		header: envelopeHeader{
			Format:       FIFFormat,
			EnvelopeType: EnvelopePassphraseOnly,
			EnvelopeParams: passphraseOnlyEnvParams{
				KDF:       "argon2id",
				KDFParams: argon2idParamsFrom(kdfParams),
				Salt:      salt,
				Nonce:     nonce,
			},
		},
		key:   key,
		Inner: inner,
	}, nil
}

// ParseFIF reads the envelope header and retains the encrypted body. Call Unlock
// to decrypt. ParseFIF refuses unknown or reserved envelope_type values with
// ErrFIFEnvelopeUnsupported.
func ParseFIF(r io.Reader) (*FIF, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return nil, codedError(ErrFIFFormat, fmt.Errorf("read header length: %w", err))
	}
	headerLen := binary.BigEndian.Uint32(lenBuf[:])
	if headerLen == 0 || headerLen > 1<<20 {
		return nil, codedError(ErrFIFFormat, fmt.Errorf("header length out of range: %d", headerLen))
	}
	headerJSON := make([]byte, headerLen)
	if _, err := io.ReadFull(r, headerJSON); err != nil {
		return nil, codedError(ErrFIFFormat, fmt.Errorf("read header: %w", err))
	}
	body, err := io.ReadAll(r)
	if err != nil {
		return nil, codedError(ErrFIFFormat, fmt.Errorf("read body: %w", err))
	}
	if len(body) < crypto.TagSize {
		return nil, codedError(ErrFIFFormat, fmt.Errorf("body shorter than AEAD tag"))
	}

	var hdr envelopeHeader
	dec := json.NewDecoder(bytes.NewReader(headerJSON))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&hdr); err != nil {
		return nil, codedError(ErrFIFFormat, fmt.Errorf("parse header JSON: %w", err))
	}
	if hdr.Format != FIFFormat {
		return nil, codedError(ErrFIFFormat, fmt.Errorf("unknown format %q", hdr.Format))
	}
	if _, reserved := reservedEnvelopeTypes[hdr.EnvelopeType]; reserved {
		return nil, codedError(ErrFIFEnvelopeUnsupported, fmt.Errorf("envelope_type %q is reserved for v1.x", hdr.EnvelopeType))
	}
	if hdr.EnvelopeType != EnvelopePassphraseOnly {
		return nil, codedError(ErrFIFEnvelopeUnsupported, fmt.Errorf("envelope_type %q is not supported", hdr.EnvelopeType))
	}
	if hdr.EnvelopeParams.KDF != "argon2id" {
		return nil, codedError(ErrFIFFormat, fmt.Errorf("unsupported kdf %q", hdr.EnvelopeParams.KDF))
	}
	if len(hdr.EnvelopeParams.Salt) != crypto.SaltSize {
		return nil, codedError(ErrFIFFormat, fmt.Errorf("salt length %d, want %d", len(hdr.EnvelopeParams.Salt), crypto.SaltSize))
	}
	if len(hdr.EnvelopeParams.Nonce) != crypto.NonceSize {
		return nil, codedError(ErrFIFFormat, fmt.Errorf("nonce length %d, want %d", len(hdr.EnvelopeParams.Nonce), crypto.NonceSize))
	}

	// Rebuild the on-wire header bytes for AEAD AAD. We use the bytes we read
	// rather than re-marshalling, so any whitespace-level differences across
	// implementations are preserved verbatim and the MAC verifies.
	headerBytes := make([]byte, 4+len(headerJSON))
	binary.BigEndian.PutUint32(headerBytes[:4], headerLen)
	copy(headerBytes[4:], headerJSON)

	return &FIF{
		header:      hdr,
		headerBytes: headerBytes,
		body:        body,
	}, nil
}

// Unlock derives the envelope key from passphrase, decrypts the body, and
// populates f.Inner. The key is cached so Serialize can re-encrypt without
// asking for the passphrase again. Returns ErrFIFAuth on wrong passphrase.
func (f *FIF) Unlock(passphrase string) error {
	if f.Inner != nil && f.IsUnlocked() {
		return nil
	}
	params := f.header.EnvelopeParams.KDFParams.toCrypto()
	key := crypto.DeriveKeyArgon2id(passphrase, f.header.EnvelopeParams.Salt, params)

	plaintext, err := crypto.Open(key, f.header.EnvelopeParams.Nonce, f.body, f.headerBytes)
	if err != nil {
		return codedError(ErrFIFAuth, err)
	}
	var inner InnerFIF
	if err := json.Unmarshal(plaintext, &inner); err != nil {
		return codedError(ErrFIFFormat, fmt.Errorf("parse inner JSON: %w", err))
	}
	if inner.Format != InnerFIFFormat {
		return codedError(ErrFIFFormat, fmt.Errorf("unknown inner format %q", inner.Format))
	}
	f.key = key
	f.Inner = &inner
	return nil
}

// Serialize encrypts the current Inner under the cached envelope key and writes
// the full FIF (4-byte length + header JSON + ciphertext+tag) to w. Returns
// ErrIdentityLocked if Unlock or NewFIF have not been called.
func (f *FIF) Serialize(w io.Writer) error {
	if !f.IsUnlocked() {
		return ErrIdentityLocked
	}
	innerJSON, err := json.Marshal(f.Inner)
	if err != nil {
		return fmt.Errorf("marshal inner: %w", err)
	}
	headerJSON, err := json.Marshal(f.header)
	if err != nil {
		return fmt.Errorf("marshal header: %w", err)
	}
	headerBytes := make([]byte, 4+len(headerJSON))
	binary.BigEndian.PutUint32(headerBytes[:4], uint32(len(headerJSON)))
	copy(headerBytes[4:], headerJSON)

	ciphertext, err := crypto.Seal(f.key, f.header.EnvelopeParams.Nonce, innerJSON, headerBytes)
	if err != nil {
		return err
	}
	if _, err := w.Write(headerBytes); err != nil {
		return err
	}
	if _, err := w.Write(ciphertext); err != nil {
		return err
	}
	// Persist the canonical header bytes so subsequent Unlock(serialized) on the
	// same in-memory FIF would use the same AAD if anyone re-reads.
	f.headerBytes = headerBytes
	f.body = ciphertext
	return nil
}

// Lock zeroes the cached envelope key and drops the in-memory Inner. The FIF
// must be Unlock()ed again before any further crypto operations.
func (f *FIF) Lock() {
	for i := range f.key {
		f.key[i] = 0
	}
	f.key = nil
	f.Inner = nil
}

var _ error = (*Error)(nil)

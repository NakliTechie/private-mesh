package identity

import "errors"

// Error is returned by FIF operations and carries a protocol-aligned code
// that matches the error codes in fabric-spec-001-v1.0.md.
type Error struct {
	Code    string
	Message string
	Cause   error
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return e.Code + ": " + e.Message + ": " + e.Cause.Error()
	}
	return e.Code + ": " + e.Message
}

func (e *Error) Unwrap() error { return e.Cause }

// Is matches by Code so callers can write errors.Is(err, identity.ErrFIFAuth).
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	if !ok {
		return false
	}
	return e.Code == t.Code
}

// Sentinel errors.
var (
	// ErrFIFFormat is returned when the FIF bytes are syntactically invalid.
	ErrFIFFormat = &Error{Code: "fif_format", Message: "FIF bytes are malformed"}

	// ErrFIFAuth is returned when MAC verification or KDF authentication fails.
	// Typically a wrong passphrase or tampered envelope body.
	ErrFIFAuth = &Error{Code: "fif_auth", Message: "FIF authentication failed"}

	// ErrFIFEnvelopeUnsupported is returned when the envelope_type is reserved
	// for a future version (shamir-shares, device-quorum, social-recovery) or
	// otherwise unrecognised. v1.0 forward-compat hook: reserved types refuse
	// with this specific code, not a generic parse failure.
	ErrFIFEnvelopeUnsupported = &Error{Code: "fif_envelope_unsupported", Message: "FIF envelope_type is not supported"}

	// ErrIdentityLocked is returned when operations needing key material run
	// against a FIF that has not been Unlock()ed (or was Lock()ed since).
	ErrIdentityLocked = &Error{Code: "identity_locked", Message: "FIF is locked"}
)

// codedError returns a copy of base with a Cause attached.
func codedError(base *Error, cause error) *Error {
	return &Error{Code: base.Code, Message: base.Message, Cause: cause}
}

// AsCode returns the Error code for err if err is an *Error, otherwise "".
func AsCode(err error) string {
	var fe *Error
	if errors.As(err, &fe) {
		return fe.Code
	}
	return ""
}

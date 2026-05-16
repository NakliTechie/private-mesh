package server

import (
	"encoding/base64"
	"errors"
)

// tryBase64 decodes s using standard then URL-safe base64, with and without
// padding. The protocol doesn't pin a single base64 flavor for the
// X-Fabric-Grant header; accept all four to be lenient on input.
func tryBase64(s string) ([]byte, error) {
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, errors.New("server: not valid base64")
}

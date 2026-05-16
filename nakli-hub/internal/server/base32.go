package server

import "encoding/base32"

// baseEncodeBase32 returns std base32 encoding of b.
func baseEncodeBase32(b []byte) string {
	return base32.StdEncoding.EncodeToString(b)
}

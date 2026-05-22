// Bounded response-body reader used by adapters. Defends against
// adversarial upstreams that stream multi-GB responses — the Hub
// previously called io.ReadAll(resp.Body) with no cap, so an
// authorized bridge caller could point an adapter at a server that
// returns a very large body and exhaust memory.

package bridge

import (
	"errors"
	"fmt"
	"io"
)

// DefaultResponseLimitBytes caps a single adapter response body. 4 MiB
// is generous for JSON / RPC / webhook payloads but bounded enough
// that a single in-flight request cannot dominate the process.
const DefaultResponseLimitBytes int64 = 4 << 20

// ErrResponseTooLarge is returned by ReadBodyCapped when the upstream
// body exceeds the supplied limit.
var ErrResponseTooLarge = errors.New("bridge: upstream response exceeded byte limit")

// ReadBodyCapped reads up to max bytes from r. Returns ErrResponseTooLarge
// (wrapped with the actual byte count read so far) when the body would
// exceed max. Adapters MUST use this in place of io.ReadAll(resp.Body)
// on every external HTTP response.
//
// Implementation: read max+1 bytes through io.LimitReader. If the +1 byte
// was reached, the body is over-budget; otherwise truncate to the read
// length and return.
func ReadBodyCapped(r io.Reader, max int64) ([]byte, error) {
	if max <= 0 {
		max = DefaultResponseLimitBytes
	}
	buf, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return buf, err
	}
	if int64(len(buf)) > max {
		return buf[:max], fmt.Errorf("%w: read at least %d bytes, limit %d", ErrResponseTooLarge, len(buf), max)
	}
	return buf, nil
}

package server

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// rateBucket is a single-grant token bucket used to enforce `rate <= N per <window>`.
// Tokens replenish at N tokens per window across the wall-clock; the bucket is
// keyed by grant_id on the Server.
type rateBucket struct {
	mu         sync.Mutex
	capacity   int
	window     time.Duration
	tokens     float64
	lastRefill time.Time
}

func (b *rateBucket) take(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	elapsed := now.Sub(b.lastRefill)
	if elapsed > 0 {
		rate := float64(b.capacity) / b.window.Seconds()
		b.tokens += rate * elapsed.Seconds()
		if b.tokens > float64(b.capacity) {
			b.tokens = float64(b.capacity)
		}
		b.lastRefill = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// rateConsume looks up or creates the bucket for (grantID, capacity, window)
// and consumes one token. Returns true if allowed.
//
// rateBuckets is LRU-capped (rateBucketLRUSize). When the cap is hit the
// least-recently-used grant's bucket is evicted — the next request from
// that grant gets a fresh bucket (full quota), slightly over-permissive
// at the eviction boundary but bounded memory beats unbounded growth.
func (s *Server) rateConsume(grantID string, capacity int, window time.Duration) bool {
	if capacity <= 0 || window <= 0 {
		return true
	}
	s.rateMu.Lock()
	b, ok := s.rateBuckets.Get(grantID)
	if !ok || b.capacity != capacity || b.window != window {
		b = &rateBucket{
			capacity:   capacity,
			window:     window,
			tokens:     float64(capacity),
			lastRefill: s.now(),
		}
		s.rateBuckets.Add(grantID, b)
	}
	s.rateMu.Unlock()
	return b.take(s.now())
}

// cachedDischarge stores a verified discharge by macaroon id with an expiry.
type cachedDischarge struct {
	mac     []byte
	expires time.Time
}

// dischargeRemember stores a parsed discharge macaroon under its caveat
// id. The cache key is an attacker-controlled URL string (the verifier
// url from `discharge-from <url>`), which is why the LRU cap matters:
// without it, an attacker can send unbounded distinct discharge URLs to
// grow the cache. The LRU itself is internally thread-safe; no mutex
// needed.
func (s *Server) dischargeRemember(caveatID string, mac []byte, ttl time.Duration) {
	s.dischargeCache.Add(caveatID, cachedDischarge{
		mac:     append([]byte(nil), mac...),
		expires: s.now().Add(ttl),
	})
}

// dischargeLookup returns a stored discharge if present and unexpired.
func (s *Server) dischargeLookup(caveatID string) ([]byte, bool) {
	d, ok := s.dischargeCache.Get(caveatID)
	if !ok {
		return nil, false
	}
	if s.now().After(d.expires) {
		s.dischargeCache.Remove(caveatID)
		return nil, false
	}
	return d.mac, true
}

// peerReachability probes the configured peer URLs and returns the
// (reachable, unreachable) pair, used by /health to derive `degraded`.
// With zero configured peers, both slices are empty.
func (s *Server) peerReachability(ctx context.Context) (reachable, unreachable []string) {
	peers := s.peerProbeURLs()
	if len(peers) == 0 {
		return nil, nil
	}
	client := &http.Client{Timeout: 750 * time.Millisecond}
	for _, u := range peers {
		ok := probePeerOnce(ctx, client, u)
		if ok {
			reachable = append(reachable, u)
		} else {
			unreachable = append(unreachable, u)
		}
	}
	return
}

// peerProbeURLs returns the peer URLs to probe; empty by default. M7 will read
// these from config — until then, tests can set them via SetPeerProbeURLs.
func (s *Server) peerProbeURLs() []string {
	s.peerMu.Lock()
	defer s.peerMu.Unlock()
	return append([]string(nil), s.peerURLs...)
}

// SetPeerProbeURLs lets tests / the conformance harness configure the peer
// URLs the Hub probes for `/health.degraded`. Real peer config is M7.
func (s *Server) SetPeerProbeURLs(urls []string) {
	s.peerMu.Lock()
	defer s.peerMu.Unlock()
	s.peerURLs = append([]string(nil), urls...)
}

func probePeerOnce(ctx context.Context, client *http.Client, raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(raw, "/")+"/fabric/v1/health", nil)
		if err != nil {
			return false
		}
		resp, err := client.Do(req)
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode < 500
	}
	// Bare host:port — try a TCP dial.
	d := net.Dialer{Timeout: 750 * time.Millisecond}
	conn, err := d.DialContext(ctx, "tcp", raw)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

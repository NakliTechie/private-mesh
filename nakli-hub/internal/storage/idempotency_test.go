package storage_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/NakliTechie/private-mesh/nakli-hub/internal/storage"
)

func openStoreForTest(t *testing.T) *storage.Store {
	t.Helper()
	dir := t.TempDir()
	s, err := storage.Open(filepath.Join(dir, "fabric.db"), filepath.Join(dir, "blobs"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// TestLookupIdempotencyFiltersExpired covers the audit fix: pre-fix, an
// expired idempotency row was returned as a replay, so a request from
// a year ago could be re-served with its stale response. Now expired
// rows are silently dropped from lookup and the request is treated as
// fresh.
func TestLookupIdempotencyFiltersExpired(t *testing.T) {
	s := openStoreForTest(t)
	ctx := context.Background()
	payload := storage.HashPayload([]byte(`{"k":"v"}`))

	// Pin "now" to a known time and put a row that expires in 1 second.
	t0 := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	s.WithClock(func() time.Time { return t0 })
	if err := s.PutIdempotency(ctx, "k1", "g1", "vault/append", payload, 200, []byte(`{"ok":true}`), 1); err != nil {
		t.Fatalf("PutIdempotency: %v", err)
	}

	// Lookup just after — should replay (record is still valid).
	s.WithClock(func() time.Time { return t0.Add(500 * time.Millisecond) })
	res, err := s.LookupIdempotency(ctx, "k1", "g1", payload)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != storage.IdempotencyReplay {
		t.Errorf("inside TTL: outcome=%v, want Replay", res.Outcome)
	}

	// Lookup after expiry — should be treated as fresh (no stale replay).
	s.WithClock(func() time.Time { return t0.Add(2 * time.Second) })
	res, err = s.LookupIdempotency(ctx, "k1", "g1", payload)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != storage.IdempotencyFresh {
		t.Errorf("after expiry: outcome=%v, want Fresh (the stale-replay bug)", res.Outcome)
	}
}

// TestPutIdempotencyReplacesExpiredRow covers the INSERT OR REPLACE
// behavior: when a stale row exists for (key, grant_id), a new
// PutIdempotency must succeed without a UNIQUE-constraint error. The
// handler depends on this — Lookup treats stale rows as Fresh, then
// the handler runs and re-writes the row with a new TTL.
func TestPutIdempotencyReplacesExpiredRow(t *testing.T) {
	s := openStoreForTest(t)
	ctx := context.Background()

	t0 := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	s.WithClock(func() time.Time { return t0 })
	first := storage.HashPayload([]byte("first"))
	if err := s.PutIdempotency(ctx, "k1", "g1", "vault/append", first, 200, []byte(`{"v":1}`), 1); err != nil {
		t.Fatalf("first put: %v", err)
	}

	// Time passes; the row is now expired. A new request with a different
	// payload should be accepted as fresh AND the replacement Put should
	// not collide on the PK.
	s.WithClock(func() time.Time { return t0.Add(2 * time.Second) })
	second := storage.HashPayload([]byte("second"))
	if err := s.PutIdempotency(ctx, "k1", "g1", "vault/append", second, 200, []byte(`{"v":2}`), 86400); err != nil {
		t.Fatalf("replacement put failed (the INSERT OR REPLACE regression): %v", err)
	}

	// Verify the lookup now sees the new payload + body.
	res, err := s.LookupIdempotency(ctx, "k1", "g1", second)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != storage.IdempotencyReplay {
		t.Fatalf("outcome: %v, want Replay", res.Outcome)
	}
	if string(res.ResponseBody) != `{"v":2}` {
		t.Errorf("response body: got %q, want %q", res.ResponseBody, `{"v":2}`)
	}
}

// TestDeleteExpiredIdempotencyReclaimsRows covers the GC method: rows
// past expires_at are removed; live rows survive.
func TestDeleteExpiredIdempotencyReclaimsRows(t *testing.T) {
	s := openStoreForTest(t)
	ctx := context.Background()
	t0 := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	s.WithClock(func() time.Time { return t0 })

	// Two rows: short TTL + long TTL.
	if err := s.PutIdempotency(ctx, "short", "g1", "ep", storage.HashPayload([]byte("a")), 200, nil, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.PutIdempotency(ctx, "long", "g1", "ep", storage.HashPayload([]byte("b")), 200, nil, 86400); err != nil {
		t.Fatal(err)
	}

	// Advance past the short TTL but well before the long.
	s.WithClock(func() time.Time { return t0.Add(10 * time.Second) })
	n, err := s.DeleteExpiredIdempotency(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("expected 1 row reclaimed, got %d", n)
	}

	// The long-TTL row must still be reachable.
	res, err := s.LookupIdempotency(ctx, "long", "g1", storage.HashPayload([]byte("b")))
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != storage.IdempotencyReplay {
		t.Errorf("long row was unexpectedly purged: %v", res.Outcome)
	}
}

package server

import (
	"strconv"
	"testing"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
)

// TestRateBucketsLRUCapped covers the audit fix: an attacker minting
// thousands of short-lived grants used to grow the rateBuckets map
// without bound. Now it's LRU-capped at rateBucketLRUSize.
//
// Internal-package test (no _test suffix) so we can construct a Server
// stub directly without the full hub fixture.
func TestRateBucketsLRUCapped(t *testing.T) {
	rb, err := lru.New[string, *rateBucket](rateBucketLRUSize)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{now: time.Now, rateBuckets: rb}
	for i := 0; i < rateBucketLRUSize+5_000; i++ {
		if !s.rateConsume("g-"+strconv.Itoa(i), 10, time.Second) {
			t.Fatalf("rate consume #%d unexpectedly denied", i)
		}
		if s.rateBuckets.Len() > rateBucketLRUSize {
			t.Fatalf("rateBuckets exceeded cap: len=%d, cap=%d", s.rateBuckets.Len(), rateBucketLRUSize)
		}
	}
	if s.rateBuckets.Len() != rateBucketLRUSize {
		t.Errorf("after insert burst: len=%d, want exactly %d", s.rateBuckets.Len(), rateBucketLRUSize)
	}
}

// TestDischargeCacheLRUCapped is the parallel for the discharge cache,
// whose key is an attacker-controlled URL string.
func TestDischargeCacheLRUCapped(t *testing.T) {
	dc, err := lru.New[string, cachedDischarge](dischargeCacheLRUSize)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{now: time.Now, dischargeCache: dc}
	for i := 0; i < dischargeCacheLRUSize+3_000; i++ {
		s.dischargeRemember("verifier://"+strconv.Itoa(i), []byte{0x01}, time.Hour)
		if s.dischargeCache.Len() > dischargeCacheLRUSize {
			t.Fatalf("dischargeCache exceeded cap: len=%d, cap=%d", s.dischargeCache.Len(), dischargeCacheLRUSize)
		}
	}
	if s.dischargeCache.Len() != dischargeCacheLRUSize {
		t.Errorf("after insert burst: len=%d, want exactly %d", s.dischargeCache.Len(), dischargeCacheLRUSize)
	}
}

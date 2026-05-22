package bridge

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestReadBodyCappedUnderLimit(t *testing.T) {
	body := bytes.NewReader([]byte("hello"))
	out, err := ReadBodyCapped(body, 1024)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if string(out) != "hello" {
		t.Fatalf("got %q", out)
	}
}

func TestReadBodyCappedAtLimit(t *testing.T) {
	// Exactly at the limit must succeed.
	body := bytes.NewReader([]byte("0123456789"))
	out, err := ReadBodyCapped(body, 10)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(out) != 10 {
		t.Fatalf("len=%d, want 10", len(out))
	}
}

func TestReadBodyCappedOverLimit(t *testing.T) {
	// 11 bytes against a 10-byte limit must fail with ErrResponseTooLarge.
	body := bytes.NewReader([]byte("0123456789X"))
	_, err := ReadBodyCapped(body, 10)
	if err == nil {
		t.Fatal("expected ErrResponseTooLarge, got nil")
	}
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Errorf("err is not ErrResponseTooLarge: %v", err)
	}
}

func TestReadBodyCappedDefaultsToWildLargeLimit(t *testing.T) {
	// max <= 0 falls back to DefaultResponseLimitBytes.
	body := strings.NewReader("payload")
	out, err := ReadBodyCapped(body, 0)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if string(out) != "payload" {
		t.Fatalf("got %q", out)
	}
}

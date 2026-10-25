package security

import (
	"testing"
	"time"
)

func TestRateLimiterAllowsWithinLimit(t *testing.T) {
	limiter := NewRateLimiter(3, time.Minute)
	for i := range 3 {
		if !limiter.Allow("user-1") {
			t.Fatalf("expected request %d to be allowed", i+1)
		}
	}
}

func TestRateLimiterRejectsWhenLimitExceeded(t *testing.T) {
	limiter := NewRateLimiter(2, time.Minute)
	if !limiter.Allow("user-2") {
		t.Fatal("expected first request to be allowed")
	}
	if !limiter.Allow("user-2") {
		t.Fatal("expected second request to be allowed")
	}
	if limiter.Allow("user-2") {
		t.Fatal("expected third request to be rejected")
	}
}

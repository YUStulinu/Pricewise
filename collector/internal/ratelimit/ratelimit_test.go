package ratelimit

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestBucketSpacesRequests(t *testing.T) {
	clock := time.Unix(0, 0)
	b := NewBucket(10, 1)
	b.now = func() time.Time { return clock }
	if d := b.reserve(); d != 0 {
		t.Fatalf("first request waits %v", d)
	}
	if d := b.reserve(); d != 100*time.Millisecond {
		t.Fatalf("second request waits %v, want 100ms", d)
	}
	clock = clock.Add(time.Second) // a second later the bucket is full again (burst 1)
	if d := b.reserve(); d != 0 {
		t.Fatalf("after a pause waits %v", d)
	}
}

func TestConcurrentRequestsKeepTheRate(t *testing.T) {
	b := NewBucket(50, 1)
	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 26; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); b.Wait(context.Background()) }()
	}
	wg.Wait()
	// 26 requests at 50 a second need at least 25 gaps of 20 ms.
	if el := time.Since(start); el < 480*time.Millisecond {
		t.Fatalf("26 requests at 50/s took %v", el)
	}
}

func TestCrawlDelayCapsTheRate(t *testing.T) {
	var h Hosts
	if r := h.For("a", 20, 500*time.Millisecond).Rate(); r != 2 {
		t.Errorf("rate %v, want 2", r)
	}
	if r := h.For("b", 20, 0).Rate(); r != 20 {
		t.Errorf("rate %v, want 20", r)
	}
	b := h.For("b", 99, 0)
	b.Slow(1)
	if b.Rate() != 10 {
		t.Errorf("slowed rate %v", b.Rate())
	}
}

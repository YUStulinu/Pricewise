// Package ratelimit keeps the collector polite: one token bucket per host, so hundreds of requests
// can be in flight across many shops while each shop sees at most its own rate.
package ratelimit

import (
	"context"
	"sync"
	"time"
)

// Bucket is a token bucket: Rate tokens a second, at most Burst saved up.
type Bucket struct {
	mu     sync.Mutex
	rate   float64
	burst  float64
	tokens float64
	last   time.Time
	slowed time.Time
	now    func() time.Time
}

func NewBucket(rate float64, burst int) *Bucket {
	if burst < 1 {
		burst = 1
	}
	return &Bucket{rate: rate, burst: float64(burst), tokens: float64(burst), now: time.Now}
}

// reserve takes a token and says how long to wait before using it.
func (b *Bucket) reserve() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	if !b.last.IsZero() {
		b.tokens += now.Sub(b.last).Seconds() * b.rate
		if b.tokens > b.burst {
			b.tokens = b.burst
		}
	}
	b.last = now
	b.tokens--
	if b.tokens >= 0 {
		return 0
	}
	return time.Duration(-b.tokens / b.rate * float64(time.Second))
}

// Allow takes a token if one is there, without waiting (for a server enforcing a limit).
func (b *Bucket) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	if !b.last.IsZero() {
		b.tokens += now.Sub(b.last).Seconds() * b.rate
		if b.tokens > b.burst {
			b.tokens = b.burst
		}
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// Wait blocks until the caller may send one request.
func (b *Bucket) Wait(ctx context.Context) error {
	d := b.reserve()
	if d == 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Slow halves the rate (down to a floor), for when a host answers 429 Too Many Requests. Requests
// already in flight answer 429 together, so the rate is halved at most once a second.
func (b *Bucket) Slow(floor float64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	if now.Sub(b.slowed) < time.Second {
		return
	}
	b.slowed = now
	b.rate /= 2
	if b.rate < floor {
		b.rate = floor
	}
}

func (b *Bucket) Rate() float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.rate
}

// Hosts hands out one bucket per host.
type Hosts struct {
	mu      sync.Mutex
	buckets map[string]*Bucket
	// Rate for a host, given its configured rate and its robots.txt crawl delay.
	Default float64
}

// For returns the bucket of host, creating it at the given rate (requests a second) if needed.
func (h *Hosts) For(host string, rate float64, crawlDelay time.Duration) *Bucket {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.buckets == nil {
		h.buckets = map[string]*Bucket{}
	}
	if b, ok := h.buckets[host]; ok {
		return b
	}
	if rate <= 0 {
		rate = h.Default
	}
	if rate <= 0 {
		rate = 1
	}
	// A crawl delay in robots.txt caps the rate, whatever the configuration says.
	if crawlDelay > 0 && 1/crawlDelay.Seconds() < rate {
		rate = 1 / crawlDelay.Seconds()
	}
	b := NewBucket(rate, 1)
	h.buckets[host] = b
	return b
}

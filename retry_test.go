// ABOUTME: Tests for retry policy, backoff schedule, and injectable clock.
// ABOUTME: Uses a fake clock to verify retry logic runs in microseconds without real delays.

package typesafe

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

// fakeClock advances only when the code under test sleeps, so retry tests
// verify the schedule without spending real time.
type fakeClock struct {
	now   time.Time
	slept time.Duration
}

func (f *fakeClock) Now() time.Time { return f.now }

func (f *fakeClock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.slept += d
	f.now = f.now.Add(d)
	return nil
}

func noJitter() float64 { return 0 }

func fullJitter() float64 { return 1 }

func TestDefaultRetryPolicyMatchesUpstream(t *testing.T) {
	// These values mirror the documented Python and JavaScript SDK defaults.
	p := DefaultRetryPolicy()

	if p.MaxRetries != 2 {
		t.Errorf("MaxRetries = %d, want 2", p.MaxRetries)
	}
	if p.BackoffInitial != 500*time.Millisecond {
		t.Errorf("BackoffInitial = %v, want 500ms", p.BackoffInitial)
	}
	if p.BackoffMax != 5*time.Second {
		t.Errorf("BackoffMax = %v, want 5s", p.BackoffMax)
	}
	if p.BackoffJitter != 0.25 {
		t.Errorf("BackoffJitter = %v, want 0.25", p.BackoffJitter)
	}
	if !p.RespectRetryAfter {
		t.Error("RespectRetryAfter = false, want true")
	}
	if p.Budget != 30*time.Second {
		t.Errorf("Budget = %v, want 30s", p.Budget)
	}
}

func TestRetryableStatuses(t *testing.T) {
	p := DefaultRetryPolicy()

	retry := []int{408, 429, 500, 502, 503, 529, 599}
	for _, s := range retry {
		if !p.Retryable(s) {
			t.Errorf("Retryable(%d) = false, want true", s)
		}
	}

	giveUp := []int{200, 400, 401, 403, 404, 422, 600}
	for _, s := range giveUp {
		if p.Retryable(s) {
			t.Errorf("Retryable(%d) = true, want false", s)
		}
	}
}

func TestBackoffDoublesAndCapsAtMax(t *testing.T) {
	p := DefaultRetryPolicy()
	now := time.Unix(0, 0)

	want := []time.Duration{
		500 * time.Millisecond,
		1 * time.Second,
		2 * time.Second,
		4 * time.Second,
		5 * time.Second,
		5 * time.Second,
	}
	for i, w := range want {
		attempt := i + 1
		if got := p.backoff(attempt, nil, now, noJitter); got != w {
			t.Errorf("backoff(%d) = %v, want %v", attempt, got, w)
		}
	}
}

func TestJitterOnlySubtracts(t *testing.T) {
	// Jitter must never push a delay above the computed backoff, or a burst
	// of clients could drift past the budget.
	p := DefaultRetryPolicy()
	now := time.Unix(0, 0)

	full := p.backoff(1, nil, now, fullJitter)
	none := p.backoff(1, nil, now, noJitter)

	if full != 375*time.Millisecond {
		t.Errorf("full jitter = %v, want 375ms (500ms minus 25%%)", full)
	}
	if full > none {
		t.Errorf("jittered delay %v exceeds base %v", full, none)
	}
}

func TestBackoffPrefersRetryAfterMilliseconds(t *testing.T) {
	p := DefaultRetryPolicy()
	h := http.Header{}
	h.Set("retry-after-ms", "1500")

	if got := p.backoff(1, h, time.Unix(0, 0), noJitter); got != 1500*time.Millisecond {
		t.Errorf("backoff = %v, want 1.5s", got)
	}
}

func TestBackoffReadsRetryAfterSeconds(t *testing.T) {
	p := DefaultRetryPolicy()
	h := http.Header{}
	h.Set("Retry-After", "3")

	if got := p.backoff(1, h, time.Unix(0, 0), noJitter); got != 3*time.Second {
		t.Errorf("backoff = %v, want 3s", got)
	}
}

func TestBackoffReadsRetryAfterHTTPDate(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	h := http.Header{}
	h.Set("Retry-After", now.Add(7*time.Second).Format(http.TimeFormat))

	p := DefaultRetryPolicy()
	if got := p.backoff(1, h, now, noJitter); got != 7*time.Second {
		t.Errorf("backoff = %v, want 7s", got)
	}
}

func TestRetryAfterIgnoredWhenPolicySaysSo(t *testing.T) {
	p := DefaultRetryPolicy()
	p.RespectRetryAfter = false

	h := http.Header{}
	h.Set("Retry-After", "3600")

	if got := p.backoff(1, h, time.Unix(0, 0), noJitter); got != 500*time.Millisecond {
		t.Errorf("backoff = %v, want the computed 500ms", got)
	}
}

func TestGarbageRetryAfterFallsBackToComputedBackoff(t *testing.T) {
	p := DefaultRetryPolicy()
	h := http.Header{}
	h.Set("Retry-After", "soon-ish")

	if got := p.backoff(1, h, time.Unix(0, 0), noJitter); got != 500*time.Millisecond {
		t.Errorf("backoff = %v, want the computed 500ms", got)
	}
}

func TestRealClockSleepStopsOnCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := (realClock{}).Sleep(ctx, time.Hour); err == nil {
		t.Fatal("Sleep returned nil on a cancelled context, want an error")
	}
}

func TestFakeClockSleepAdvancesTimeWithoutWaiting(t *testing.T) {
	base := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	var c clock = &fakeClock{now: base}

	err := c.Sleep(context.Background(), 500*time.Millisecond)
	if err != nil {
		t.Errorf("Sleep returned error: %v", err)
	}

	if c.Now() != base.Add(500*time.Millisecond) {
		t.Errorf("Now() = %v, want %v", c.Now(), base.Add(500*time.Millisecond))
	}

	// Multiple calls accumulate.
	err = c.Sleep(context.Background(), 1*time.Second)
	if err != nil {
		t.Errorf("Sleep returned error: %v", err)
	}

	if c.Now() != base.Add(1500*time.Millisecond) {
		t.Errorf("Now() = %v, want %v", c.Now(), base.Add(1500*time.Millisecond))
	}
}

func TestFakeClockSleepRejectsCancelledContext(t *testing.T) {
	base := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	f := &fakeClock{now: base}
	var c clock = f

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := c.Sleep(ctx, time.Second)
	if err == nil {
		t.Fatal("Sleep returned nil on cancelled context, want an error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Sleep error = %v, want context.Canceled", err)
	}

	// fakeClock should not have advanced.
	if f.now != base {
		t.Errorf("fakeClock.now advanced to %v, want %v (unchanged)", f.now, base)
	}
	if f.slept != 0 {
		t.Errorf("fakeClock.slept = %v, want 0", f.slept)
	}
}

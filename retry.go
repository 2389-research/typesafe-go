// ABOUTME: Retry policy and backoff schedule for transient API failures.
// ABOUTME: Timing goes through an injected clock so tests never sleep.

package typesafe

import (
	"context"
	"math"
	"net/http"
	"strconv"
	"time"
)

// RetryPolicy controls how transient failures are retried.
//
// The zero value retries nothing. Start from DefaultRetryPolicy and adjust.
type RetryPolicy struct {
	// MaxRetries is how many attempts follow the first one.
	MaxRetries int
	// BackoffInitial is the delay before the first retry. It doubles each
	// time, up to BackoffMax.
	BackoffInitial time.Duration
	// BackoffMax caps the computed delay. Retry-After can still exceed it.
	// Zero means no cap: the delay doubles until it saturates at the
	// largest representable duration.
	BackoffMax time.Duration
	// BackoffJitter is the fraction of the delay that may be subtracted at
	// random, spreading out a thundering herd. 0.25 means up to 25% off.
	BackoffJitter float64
	// Statuses lists the response codes worth retrying.
	Statuses []int
	// RespectRetryAfter honors a Retry-After or retry-after-ms header in
	// preference to the computed delay.
	RespectRetryAfter bool
	// Budget caps the total wall time of a call, delays included. Zero
	// means no cap.
	Budget time.Duration
}

// DefaultRetryPolicy mirrors the documented defaults of TypeSafe's Python
// SDK: two retries, 500ms doubling to 5s, 25% jitter, 30s budget, retrying
// 408, 429, and every 5xx.
func DefaultRetryPolicy() RetryPolicy {
	statuses := make([]int, 0, 102)
	statuses = append(statuses, http.StatusRequestTimeout, http.StatusTooManyRequests)
	for status := 500; status < 600; status++ {
		statuses = append(statuses, status)
	}

	return RetryPolicy{
		MaxRetries:        2,
		BackoffInitial:    500 * time.Millisecond,
		BackoffMax:        5 * time.Second,
		BackoffJitter:     0.25,
		Statuses:          statuses,
		RespectRetryAfter: true,
		Budget:            30 * time.Second,
	}
}

// Retryable reports whether a response code is worth another attempt.
func (p RetryPolicy) Retryable(status int) bool {
	for _, s := range p.Statuses {
		if s == status {
			return true
		}
	}
	return false
}

// backoff returns the delay before the given attempt, counting the first
// retry as attempt 1. A usable Retry-After header wins over the schedule.
// jitter must return a value in [0,1); a negative return would push the
// delay above BackoffMax, the opposite of what jitter is for.
func (p RetryPolicy) backoff(attempt int, h http.Header, now time.Time, jitter func() float64) time.Duration {
	if p.RespectRetryAfter {
		if d, ok := retryAfter(h, now); ok {
			return d
		}
	}
	if p.BackoffInitial <= 0 || attempt < 1 {
		return 0
	}

	delay := p.BackoffInitial
	for i := 1; i < attempt; i++ {
		if delay > math.MaxInt64/2 {
			// Doubling again would wrap int64 negative, and a negative
			// delay is reported as 0 below — a hot retry loop instead of
			// a long wait. Saturate at the largest representable delay.
			delay = math.MaxInt64
			break
		}
		delay *= 2
		if p.BackoffMax > 0 && delay >= p.BackoffMax {
			delay = p.BackoffMax
			break
		}
	}
	// Saturation can land above a caller's cap, so enforce it once more on
	// the way out.
	if p.BackoffMax > 0 && delay > p.BackoffMax {
		delay = p.BackoffMax
	}

	if p.BackoffJitter > 0 && jitter != nil {
		delay -= time.Duration(float64(delay) * p.BackoffJitter * jitter())
	}
	if delay < 0 {
		return 0
	}
	return delay
}

// retryAfter reads a server-supplied delay.
//
// It checks retry-after-ms first because it is finer-grained, then
// Retry-After in both of the forms RFC 9110 allows: a count of seconds and an
// HTTP date. An unreadable value is reported as absent so the caller falls
// back to its own schedule.
func retryAfter(h http.Header, now time.Time) (time.Duration, bool) {
	if h == nil {
		return 0, false
	}

	if v := h.Get("retry-after-ms"); v != "" {
		if ms, err := strconv.ParseFloat(v, 64); err == nil && ms >= 0 {
			return clampDuration(ms, time.Millisecond), true
		}
	}

	v := h.Get("Retry-After")
	if v == "" {
		return 0, false
	}
	if seconds, err := strconv.ParseFloat(v, 64); err == nil && seconds >= 0 {
		return clampDuration(seconds, time.Second), true
	}
	if when, err := http.ParseTime(v); err == nil {
		if d := when.Sub(now); d > 0 {
			return d, true
		}
		return 0, true
	}
	return 0, false
}

// clampDuration converts value units of size unit to a time.Duration,
// clamping to the largest representable value instead of overflowing.
//
// value comes from a server-supplied header with no upper bound. The Go
// spec leaves float64-to-integer conversion of an out-of-range value
// implementation-defined, and converting a large enough value straight to
// time.Duration risks landing on a negative number — turning a server's
// request for a long wait into an immediate retry instead.
func clampDuration(value float64, unit time.Duration) time.Duration {
	scaled := value * float64(unit)
	if scaled >= float64(math.MaxInt64) {
		return math.MaxInt64
	}
	return time.Duration(scaled)
}

// clock isolates the package from the wall clock so retry tests run instantly.
type clock interface {
	Now() time.Time
	Sleep(ctx context.Context, d time.Duration) error
}

// realClock is the production clock.
type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// Sleep waits for d, or stops early if the context ends.
func (realClock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if d <= 0 {
		return nil
	}

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

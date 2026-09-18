// ABOUTME: Tests for client configuration, functional options, and the Ask call.
// ABOUTME: Covers env/option precedence, request shape, error mapping, and the retry loop.

package typesafe

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// testClient points a client at a test server, with jitter pinned to zero so
// retry timings are exact.
func testClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	c, err := New(WithAPIKey("test-key"), WithBaseURL(baseURL))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c.jitter = noJitter
	return c
}

func TestNewRequiresAnAPIKey(t *testing.T) {
	t.Setenv(EnvAPIKey, "")

	if _, err := New(); !errors.Is(err, ErrNoAPIKey) {
		t.Fatalf("New = %v, want ErrNoAPIKey", err)
	}
}

func TestNewReadsTheEnvironment(t *testing.T) {
	t.Setenv(EnvAPIKey, "env-key")
	t.Setenv(EnvBaseURL, "https://proxy.example.com")
	t.Setenv(EnvModel, "jev-1.13.0")

	c, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.apiKey != "env-key" {
		t.Errorf("apiKey = %q, want env-key", c.apiKey)
	}
	if c.baseURL != "https://proxy.example.com" {
		t.Errorf("baseURL = %q, want https://proxy.example.com", c.baseURL)
	}
	if c.model != "jev-1.13.0" {
		t.Errorf("model = %q, want jev-1.13.0", c.model)
	}
}

func TestNewFallsBackToDocumentedDefaults(t *testing.T) {
	t.Setenv(EnvAPIKey, "k")
	t.Setenv(EnvBaseURL, "")
	t.Setenv(EnvModel, "")

	c, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.baseURL != DefaultBaseURL {
		t.Errorf("baseURL = %q, want %q", c.baseURL, DefaultBaseURL)
	}
	if c.model != DefaultModel {
		t.Errorf("model = %q, want %q", c.model, DefaultModel)
	}
	if c.http.Timeout != DefaultTimeout {
		t.Errorf("timeout = %v, want %v", c.http.Timeout, DefaultTimeout)
	}
}

func TestOptionsBeatTheEnvironment(t *testing.T) {
	t.Setenv(EnvAPIKey, "env-key")
	t.Setenv(EnvModel, "env-model")

	c, err := New(WithAPIKey("option-key"), WithModel("option-model"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.apiKey != "option-key" || c.model != "option-model" {
		t.Errorf("got %q/%q, want option-key/option-model", c.apiKey, c.model)
	}
}

func TestBaseURLLosesItsTrailingSlash(t *testing.T) {
	// Paths are joined by concatenation, so a trailing slash would produce
	// a double slash in every request.
	c, err := New(WithAPIKey("k"), WithBaseURL("https://proxy.example.com/"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.baseURL != "https://proxy.example.com" {
		t.Errorf("baseURL = %q, want no trailing slash", c.baseURL)
	}
}

func TestWithBaseURLRejectsAValueWithoutAScheme(t *testing.T) {
	if _, err := New(WithAPIKey("k"), WithBaseURL("proxy.example.com")); err == nil {
		t.Fatal("New accepted a schemeless base URL, want an error")
	}
}

func TestWithTimeoutLeavesTheCallersHTTPClientAlone(t *testing.T) {
	mine := &http.Client{Timeout: time.Minute}

	c, err := New(WithAPIKey("k"), WithHTTPClient(mine), WithTimeout(time.Second))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if mine.Timeout != time.Minute {
		t.Errorf("caller's client timeout changed to %v", mine.Timeout)
	}
	if c.http.Timeout != time.Second {
		t.Errorf("client timeout = %v, want 1s", c.http.Timeout)
	}
}

const okResponse = `{
  "model": "jev-1.13.0",
  "answers": {"is_urgent": {"type": "noul", "noul": 0.92}},
  "usage": {"input_tokens": 312, "output_tokens": 48}
}`

func TestAskSendsTheDocumentedRequestShape(t *testing.T) {
	type dept string

	var body []byte
	var gotPath, gotAuth, gotAgent, gotContentType string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotAgent = r.Header.Get("User-Agent")
		gotContentType = r.Header.Get("Content-Type")
		body, _ = io.ReadAll(r.Body)
		_, _ = io.WriteString(w, okResponse)
	}))
	defer srv.Close()

	c := testClient(t, srv.URL)
	urgent := Noul("is_urgent", "Does this convey urgency?").
		WithCriteria("Explicitly time-sensitive", "No urgency expressed")
	team := Choice[dept]("department", "Which team should handle this?", Opts[dept]{
		"billing": "Payments, invoicing, refunds",
	})

	if _, err := c.Ask(context.Background(), "Help! My payouts have been failing for 3 days.", urgent, team); err != nil {
		t.Fatalf("Ask: %v", err)
	}

	if gotPath != "/v1/systemone" {
		t.Errorf("path = %q, want /v1/systemone", gotPath)
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("Authorization = %q, want Bearer test-key", gotAuth)
	}
	if !strings.HasPrefix(gotAgent, "typesafe-go/") {
		t.Errorf("User-Agent = %q, want a typesafe-go/ prefix", gotAgent)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotContentType)
	}

	want := `{
	  "state": "Help! My payouts have been failing for 3 days.",
	  "model": "jev-latest",
	  "questions": {
	    "is_urgent": {
	      "type": "noul",
	      "instructions": "Does this convey urgency?",
	      "criteria": {"true": "Explicitly time-sensitive", "false": "No urgency expressed"}
	    },
	    "department": {
	      "type": "choice",
	      "instructions": "Which team should handle this?",
	      "criteria": {"billing": "Payments, invoicing, refunds"}
	    }
	  }
	}`
	if !jsonEqual(t, body, []byte(want)) {
		t.Errorf("request body\n got: %s\nwant: %s", body, want)
	}
}

func TestAskReturnsModelUsageAndAnswers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, okResponse)
	}))
	defer srv.Close()

	c := testClient(t, srv.URL)
	urgent := Noul("is_urgent", "Does this convey urgency?")

	res, err := c.Ask(context.Background(), "state", urgent)
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if res.Model != "jev-1.13.0" {
		t.Errorf("Model = %q, want the resolved jev-1.13.0", res.Model)
	}
	if res.Usage.InputTokens != 312 || res.Usage.OutputTokens != 48 {
		t.Errorf("Usage = %+v, want {312 48}", res.Usage)
	}

	p, err := urgent.From(res)
	if err != nil {
		t.Fatalf("From: %v", err)
	}
	if p != 0.92 {
		t.Errorf("From = %v, want 0.92", p)
	}
}

func TestAskRejectsBadRequestsBeforeSending(t *testing.T) {
	type dept string

	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		_, _ = io.WriteString(w, okResponse)
	}))
	defer srv.Close()

	c := testClient(t, srv.URL)

	cases := []struct {
		name      string
		questions []Question
		want      error
		wantMsg   string // checked with strings.Contains when non-empty
	}{
		{"no questions", nil, ErrNoQuestions, ""},
		{"nil question", []Question{Noul("q", "?"), nil}, ErrNilQuestion, "index 1"},
		{"empty id", []Question{Noul("", "?")}, ErrEmptyID, ""},
		{"duplicate id", []Question{Noul("q", "?"), Noul("q", "?")}, ErrDuplicateID, ""},
		{"choice with no options", []Question{Choice[dept]("q", "?", Opts[dept]{})}, ErrNoOptions, ""},
		{"score with one level", []Question{Score("q", "?", "only")}, ErrTooFewLevels, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := c.Ask(context.Background(), "state", tc.questions...)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Ask = %v, want %v", err, tc.want)
			}
			if tc.wantMsg != "" && !strings.Contains(err.Error(), tc.wantMsg) {
				t.Fatalf("Ask error = %q, want it to name %q", err.Error(), tc.wantMsg)
			}
		})
	}

	if called {
		t.Error("an invalid request reached the server; validation must happen first")
	}
}

func TestAskMapsUnauthorizedAndKeepsTheBody(t *testing.T) {
	const body = `{"message":"invalid api key"}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()

	c := testClient(t, srv.URL)
	_, err := c.Ask(context.Background(), "state", Noul("q", "?"))

	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Ask = %v, want ErrUnauthorized", err)
	}

	var apiErr *Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("Ask = %v, want a *typesafe.Error", err)
	}
	if apiErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("StatusCode = %d, want 401", apiErr.StatusCode)
	}
	if string(apiErr.Body) != body {
		t.Errorf("Body = %q, want %q", apiErr.Body, body)
	}
}

func TestAskRetriesRateLimitAndHonorsRetryAfter(t *testing.T) {
	var calls int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("retry-after-ms", "1200")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"message":"slow down"}`)
			return
		}
		_, _ = io.WriteString(w, okResponse)
	}))
	defer srv.Close()

	fake := &fakeClock{now: time.Unix(0, 0)}
	c := testClient(t, srv.URL)
	c.clock = fake

	if _, err := c.Ask(context.Background(), "state", Noul("is_urgent", "?")); err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
	if fake.slept != 1200*time.Millisecond {
		t.Errorf("slept %v, want the server's 1.2s", fake.slept)
	}
}

func TestAskGivesUpAfterMaxRetries(t *testing.T) {
	var calls int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(529)
		_, _ = io.WriteString(w, `{"message":"overloaded"}`)
	}))
	defer srv.Close()

	c := testClient(t, srv.URL)
	c.clock = &fakeClock{now: time.Unix(0, 0)}

	_, err := c.Ask(context.Background(), "state", Noul("q", "?"))
	if !errors.Is(err, ErrOverloaded) {
		t.Fatalf("Ask = %v, want ErrOverloaded", err)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3 (one attempt plus two retries)", calls)
	}
}

func TestAskDoesNotRetryValidationFailures(t *testing.T) {
	// A 422 will fail identically every time. Retrying it wastes the
	// caller's budget and the server's capacity.
	var calls int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = io.WriteString(w, `{"message":"bad field"}`)
	}))
	defer srv.Close()

	c := testClient(t, srv.URL)
	c.clock = &fakeClock{now: time.Unix(0, 0)}

	_, err := c.Ask(context.Background(), "state", Noul("q", "?"))
	if !errors.Is(err, ErrUnprocessable) {
		t.Fatalf("Ask = %v, want ErrUnprocessable", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}

func TestWithRetryClonesTheCallersStatuses(t *testing.T) {
	// RetryPolicy.Statuses is a slice, so copying the policy struct alone
	// would leave the client reading the caller's backing array. Editing an
	// index in place after New would then silently retune a live client.
	statuses := make([]int, 1, 4)
	statuses[0] = http.StatusTooManyRequests

	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"message":"slow down"}`)
			return
		}
		w.WriteHeader(http.StatusTeapot)
		_, _ = io.WriteString(w, `{"message":"teapot"}`)
	}))
	defer srv.Close()

	c := testClient(t, srv.URL)
	c.clock = &fakeClock{now: time.Unix(0, 0)}
	if err := WithRetry(RetryPolicy{MaxRetries: 2, Statuses: statuses})(c); err != nil {
		t.Fatalf("WithRetry: %v", err)
	}

	// The caller is entitled to keep using its slice; the client must not
	// notice.
	statuses[0] = http.StatusTeapot

	if !c.retry.Retryable(http.StatusTooManyRequests) {
		t.Error("client stopped retrying 429: it shares the caller's Statuses array")
	}
	if c.retry.Retryable(http.StatusTeapot) {
		t.Error("client retries 418 after the caller edited its own slice")
	}

	// The wire behaviour is what the caller actually feels: the server's
	// 429 is still retried, and the teapot on the second attempt ends the
	// call because it was never in the client's own status list.
	_, err := c.Ask(context.Background(), "state", Noul("q", "?"))
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("Ask = %v, want a *Error", err)
	}
	if apiErr.StatusCode != http.StatusTeapot {
		t.Errorf("status = %d, want 418", apiErr.StatusCode)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2: the 429 was retried once, then 418 ended the call", calls)
	}
}

func TestAskStopsWhenTheBudgetWouldBeExceeded(t *testing.T) {
	var calls int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"message":"come back tomorrow"}`)
	}))
	defer srv.Close()

	c := testClient(t, srv.URL)
	c.clock = &fakeClock{now: time.Unix(0, 0)}

	_, err := c.Ask(context.Background(), "state", Noul("q", "?"))
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("Ask = %v, want ErrRateLimited", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1: an hour-long wait blows the 30s budget", calls)
	}
}

func TestAskStopsOnACancelledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, okResponse)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	c := testClient(t, srv.URL)
	if _, err := c.Ask(ctx, "state", Noul("q", "?")); !errors.Is(err, context.Canceled) {
		t.Fatalf("Ask = %v, want context.Canceled", err)
	}
}

// cancelOnSleepClock is a fakeClock that cancels the context under test when
// do() enters its first sleep. Cancelling from the server handler instead
// races the response: the client can observe the cancelled context while it
// reads the 429 body, take attempt's early return, and never compute a
// backoff at all — a different path than the one this test names.
type cancelOnSleepClock struct {
	fakeClock

	cancel context.CancelFunc
	sleeps int
}

func (c *cancelOnSleepClock) Sleep(ctx context.Context, d time.Duration) error {
	c.sleeps++
	c.cancel()
	return c.fakeClock.Sleep(ctx, d)
}

func TestAskStopsWhenTheContextIsCancelledDuringBackoff(t *testing.T) {
	var calls int

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"message":"slow down"}`)
	}))
	defer srv.Close()

	c := testClient(t, srv.URL)
	clock := &cancelOnSleepClock{fakeClock: fakeClock{now: time.Unix(0, 0)}, cancel: cancel}
	c.clock = clock

	_, err := c.Ask(ctx, "state", Noul("q", "?"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Ask = %v, want context.Canceled", err)
	}
	if clock.sleeps != 1 {
		t.Errorf("Sleep calls = %d, want 1: the cancellation must land in the backoff, not before it", clock.sleeps)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1: cancelling during backoff must stop before the retry", calls)
	}
}

func TestAskFailsOnAnUnreadableSuccessBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"model": "jev-latest", "answers":`)
	}))
	defer srv.Close()

	c := testClient(t, srv.URL)
	if _, err := c.Ask(context.Background(), "state", Noul("q", "?")); err == nil {
		t.Fatal("Ask accepted truncated JSON, want an error")
	}
}

// ABOUTME: The TypeSafe client: configuration, transport, and the Ask call.
// ABOUTME: One synchronous client; run concurrent calls in your own goroutines.

package typesafe

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"
)

// Version is this SDK's version, reported in the User-Agent header.
const Version = "0.1.0"

// Documented defaults.
const (
	// DefaultBaseURL is the TypeSafe API root.
	DefaultBaseURL = "https://api.typesafe.ai"
	// DefaultModel is the alias for the current stable Jev release.
	DefaultModel = "jev-latest"
	// DefaultTimeout bounds one HTTP attempt, not the whole retried call.
	// The retry budget stops the loop before a backoff that would cross
	// it, but the attempt that follows the backoff still runs, so a call
	// can overrun the budget by up to one attempt's timeout.
	DefaultTimeout = 10 * time.Second
)

// Environment variables New reads when the matching option is absent.
const (
	EnvAPIKey  = "TYPESAFE_API_KEY"
	EnvBaseURL = "TYPESAFE_BASE_URL"
	EnvModel   = "TYPESAFE_DEFAULT_MODEL"
)

// userAgent identifies the SDK and the Go release that built it, which is
// what upstream needs to reproduce a report.
var userAgent = fmt.Sprintf("typesafe-go/%s (%s; %s/%s)", Version, runtime.Version(), runtime.GOOS, runtime.GOARCH)

// Client talks to the TypeSafe API. It is safe for concurrent use.
type Client struct {
	apiKey  string
	baseURL string
	model   string
	http    *http.Client
	retry   RetryPolicy
	timeout *time.Duration
	clock   clock
	jitter  func() float64
}

// Option configures a Client. Pass options to New.
type Option func(*Client) error

// New builds a client.
//
// Configuration resolves in three layers, later winning over earlier:
// documented defaults, then TYPESAFE_API_KEY, TYPESAFE_BASE_URL, and
// TYPESAFE_DEFAULT_MODEL, then the options you pass.
//
// An API key is the one hard requirement; without one New returns
// ErrNoAPIKey rather than failing later on the first call.
func New(opts ...Option) (*Client, error) {
	c := &Client{
		apiKey:  os.Getenv(EnvAPIKey),
		baseURL: cmp.Or(os.Getenv(EnvBaseURL), DefaultBaseURL),
		model:   cmp.Or(os.Getenv(EnvModel), DefaultModel),
		http:    &http.Client{Timeout: DefaultTimeout},
		retry:   DefaultRetryPolicy(),
		clock:   realClock{},
		jitter:  rand.Float64,
	}

	for _, opt := range opts {
		if err := opt(c); err != nil {
			return nil, err
		}
	}

	if c.apiKey == "" {
		return nil, ErrNoAPIKey
	}

	// Applied last, against a copy, so WithTimeout never reaches into an
	// http.Client the caller still holds.
	if c.timeout != nil {
		clone := *c.http
		clone.Timeout = *c.timeout
		c.http = &clone
	}

	c.baseURL = strings.TrimRight(c.baseURL, "/")
	return c, nil
}

// WithAPIKey sets the key, overriding TYPESAFE_API_KEY.
func WithAPIKey(key string) Option {
	return func(c *Client) error {
		if key == "" {
			return ErrNoAPIKey
		}
		c.apiKey = key
		return nil
	}
}

// WithBaseURL points the client somewhere other than api.typesafe.ai, which
// is what you want for a proxy or a record-and-replay test server.
func WithBaseURL(raw string) Option {
	return func(c *Client) error {
		parsed, err := url.Parse(raw)
		if err != nil {
			return fmt.Errorf("typesafe: bad base URL %q: %w", raw, err)
		}
		if parsed.Scheme == "" || parsed.Host == "" {
			return fmt.Errorf("typesafe: base URL %q needs a scheme and a host", raw)
		}
		c.baseURL = raw
		return nil
	}
}

// WithModel sets the model sent with every request, overriding
// TYPESAFE_DEFAULT_MODEL. Call Models to see what your account can use.
func WithModel(model string) Option {
	return func(c *Client) error {
		if model == "" {
			return errors.New("typesafe: model cannot be empty")
		}
		c.model = model
		return nil
	}
}

// WithHTTPClient supplies your own transport, for proxies, custom TLS, or
// instrumentation. The client is used as given and never modified.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) error {
		if h == nil {
			return errors.New("typesafe: HTTP client cannot be nil")
		}
		c.http = h
		return nil
	}
}

// WithRetry replaces the retry policy. Start from DefaultRetryPolicy and
// adjust; the zero RetryPolicy retries nothing.
func WithRetry(p RetryPolicy) Option {
	return func(c *Client) error {
		c.retry = p
		return nil
	}
}

// WithTimeout bounds each HTTP attempt. It applies to a copy of the HTTP
// client, so it works alongside WithHTTPClient in either order.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) error {
		if d <= 0 {
			return errors.New("typesafe: timeout must be positive")
		}
		c.timeout = &d
		return nil
	}
}

// askRequest and askResponse are the wire shapes of POST /v1/systemone.
type askRequest struct {
	State     any                        `json:"state"`
	Model     string                     `json:"model"`
	Questions map[string]questionPayload `json:"questions"`
}

type askResponse struct {
	Model   string                     `json:"model"`
	Answers map[string]json.RawMessage `json:"answers"`
	Usage   Usage                      `json:"usage"`
}

// Ask evaluates state against every question in one request.
//
// State is any JSON-encodable value: a string for plain text, or a struct,
// map, or slice for a chat log or a record. Questions are answered
// independently and in parallel; none of them can see another's answer, so
// chain a second Ask when one judgment depends on another.
//
// Ask retries transient failures, and a retry can bill twice for one
// evaluation: the client cannot tell a request that never reached the
// server from one whose response was lost after the server already
// processed it, so it retries either one. If you need at-most-once
// behavior, set WithRetry to a policy with MaxRetries: 0.
//
// Read each answer through the question value you passed in:
//
//	urgent := typesafe.Noul("is_urgent", "Does this convey urgency?")
//	res, err := client.Ask(ctx, ticket, urgent)
//	p, err := urgent.From(res)
func (c *Client) Ask(ctx context.Context, state any, questions ...Question) (*Result, error) {
	request, err := c.buildAsk(state, questions)
	if err != nil {
		return nil, err
	}

	var response askResponse
	if err := c.do(ctx, http.MethodPost, "/v1/systemone", request, &response); err != nil {
		return nil, err
	}

	return &Result{
		Model:   response.Model,
		Usage:   response.Usage,
		answers: response.Answers,
	}, nil
}

// buildAsk turns questions into a request, rejecting anything the API would
// reject so a mistake costs nothing.
func (c *Client) buildAsk(state any, questions []Question) (askRequest, error) {
	if len(questions) == 0 {
		return askRequest{}, ErrNoQuestions
	}

	payloads := make(map[string]questionPayload, len(questions))
	for _, q := range questions {
		id := q.ID()
		if id == "" {
			return askRequest{}, ErrEmptyID
		}
		if _, seen := payloads[id]; seen {
			return askRequest{}, fmt.Errorf("%w: %q", ErrDuplicateID, id)
		}
		if err := q.validate(); err != nil {
			return askRequest{}, err
		}
		payloads[id] = q.payload()
	}

	return askRequest{State: state, Model: c.model, Questions: payloads}, nil
}

// do runs one API call, retrying transient failures until the policy's
// retry count or its wall-clock budget runs out.
func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var payload []byte
	if in != nil {
		var err error
		if payload, err = json.Marshal(in); err != nil {
			return fmt.Errorf("typesafe: encoding request: %w", err)
		}
	}

	deadline := c.clock.Now().Add(c.retry.Budget)
	var (
		lastErr error
		header  http.Header
	)

	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			delay := c.retry.backoff(attempt, header, c.clock.Now(), c.jitter)
			if c.retry.Budget > 0 && c.clock.Now().Add(delay).After(deadline) {
				return lastErr
			}
			if err := c.clock.Sleep(ctx, delay); err != nil {
				return errors.Join(lastErr, err)
			}
		}

		retryable, responseHeader, err := c.attempt(ctx, method, path, payload, out)
		if err == nil {
			return nil
		}
		lastErr, header = err, responseHeader

		if !retryable || attempt >= c.retry.MaxRetries {
			return err
		}
	}
}

// attempt makes one request and reports whether another is worth trying.
func (c *Client) attempt(ctx context.Context, method, path string, payload []byte, out any) (bool, http.Header, error) {
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return false, nil, fmt.Errorf("typesafe: building request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return false, nil, ctxErr
		}
		// The client cannot tell a request that never reached the server
		// from one whose response was lost after the server handled it,
		// so it retries both.
		return true, nil, fmt.Errorf("typesafe: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return false, resp.Header, ctxErr
		}
		return true, resp.Header, fmt.Errorf("typesafe: reading response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return c.retry.Retryable(resp.StatusCode), resp.Header, newError(resp.StatusCode, responseBody)
	}

	if out != nil {
		if err := json.Unmarshal(responseBody, out); err != nil {
			return false, resp.Header, fmt.Errorf("typesafe: decoding response: %w", err)
		}
	}
	return false, resp.Header, nil
}

//go:build e2e

// ABOUTME: End-to-end tests against the live TypeSafe API.
// ABOUTME: Skipped without TYPESAFE_API_KEY; run with: go test -tags=e2e ./...

package typesafe_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/2389-research/typesafe-go"
)

type department string

const (
	billing   department = "billing"
	technical department = "technical"
	sales     department = "sales"
)

const ticket = "Help! My payouts have been failing for 3 days."

// apiKey returns the live key or skips the test.
func apiKey(t *testing.T) string {
	t.Helper()
	key := os.Getenv("TYPESAFE_API_KEY")
	if key == "" {
		t.Skip("set TYPESAFE_API_KEY to run live tests")
	}
	return key
}

// liveClient builds a client against the real API.
func liveClient(t *testing.T) *typesafe.Client {
	t.Helper()
	apiKey(t)

	client, err := typesafe.New(typesafe.WithTimeout(30 * time.Second))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return client
}

// liveContext bounds a live call so a hung API fails the test instead of the
// whole run.
func liveContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestLiveTriage(t *testing.T) {
	client := liveClient(t)

	urgent := typesafe.Noul("is_urgent", "Does this convey urgency?").
		WithCriteria("Explicitly time-sensitive", "No urgency expressed")
	team := typesafe.Choice[department]("department", "Which team should handle this?",
		typesafe.Opts[department]{
			billing:   "Payments, invoicing, refunds",
			technical: "Bugs, outages, integrations",
			sales:     "Pricing, upgrades, new accounts",
		})
	anger := typesafe.Score("frustration", "How frustrated is the customer?",
		"Calm", "Frustrated", "Very angry")

	res, err := client.Ask(liveContext(t), ticket, urgent, team, anger)
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	t.Logf("model %s, %d input tokens", res.Model, res.Usage.InputTokens)

	if res.Model == "" {
		t.Error("Model is empty")
	}
	if res.Usage.InputTokens == 0 {
		t.Error("InputTokens = 0, want a real count")
	}

	urgency, err := urgent.From(res)
	if err != nil {
		t.Fatalf("urgent.From: %v", err)
	}
	t.Logf("is_urgent = %.3f", urgency)
	if urgency <= 0.5 {
		t.Errorf("is_urgent = %.3f, want above 0.5 for an explicit three-day outage", urgency)
	}

	assignment, err := team.From(res)
	if err != nil {
		t.Fatalf("team.From: %v", err)
	}
	t.Logf("department = %s, confidence %.3f, %v", assignment.Value, assignment.Confidence, assignment.Probabilities)

	// The upstream docs publish this exact ticket with "choice": "technical"
	// at 0.85, so that is what the SDK should see. If this fails, the finding
	// is that the SDK and the docs disagree — report it and ask before
	// touching the assertion. Widening it to make a live test pass would hide
	// exactly what this suite exists to catch.
	if assignment.Value != technical {
		t.Errorf("department = %q, want %q", assignment.Value, technical)
	}
	if len(assignment.Probabilities) != 3 {
		t.Errorf("got %d probabilities, want one per option", len(assignment.Probabilities))
	}
	if total := sum(assignment.Probabilities); math.Abs(total-1) > 0.01 {
		t.Errorf("probabilities sum to %.4f, want 1", total)
	}
	if assignment.Probabilities[assignment.Value] != maxOf(assignment.Probabilities) {
		t.Errorf("Value %q is not the highest-probability option", assignment.Value)
	}

	frustration, err := anger.From(res)
	if err != nil {
		t.Fatalf("anger.From: %v", err)
	}
	t.Logf("frustration = %.3f, legend %v, %v", frustration.Value, frustration.Legend, frustration.Probabilities)

	if len(frustration.Legend) != 3 {
		t.Errorf("legend has %d levels, want 3", len(frustration.Legend))
	}
	if frustration.Legend[0] != "Calm" || frustration.Legend[2] != "Very angry" {
		t.Errorf("legend = %v, want the levels sent, in order", frustration.Legend)
	}
	if frustration.Value < 0 || frustration.Value > 2 {
		t.Errorf("score = %.3f, want within the level range 0 to 2", frustration.Value)
	}
}

func sum[K comparable](m map[K]float64) float64 {
	var total float64
	for _, v := range m {
		total += v
	}
	return total
}

func maxOf[K comparable](m map[K]float64) float64 {
	var highest float64
	for _, v := range m {
		if v > highest {
			highest = v
		}
	}
	return highest
}

func TestLiveModels(t *testing.T) {
	models, err := liveClient(t).Models(liveContext(t))
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("got no models, want at least the jev-latest alias")
	}

	for _, m := range models {
		t.Logf("%s (%s) %s", m.Name, m.ReleaseDate, m.Description)
		if m.Name == "" {
			t.Error("a model has an empty Name")
		}
	}
}

func TestLiveStructuredState(t *testing.T) {
	// State accepts objects and arrays, not only strings.
	conversation := []map[string]string{
		{"role": "customer", "text": "My payouts have been failing for 3 days."},
		{"role": "agent", "text": "Let me check on that."},
		{"role": "customer", "text": "You said that yesterday."},
	}

	repeated := typesafe.Noul("is_repeat_contact",
		"Has the customer raised this before in the conversation?")

	res, err := liveClient(t).Ask(liveContext(t), conversation, repeated)
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}

	p, err := repeated.From(res)
	if err != nil {
		t.Fatalf("From: %v", err)
	}
	t.Logf("is_repeat_contact = %.3f", p)
	if p < 0 || p > 1 {
		t.Errorf("probability = %.3f, want between 0 and 1", p)
	}
}

// capture records a real API response body under testdata/, so the Error type
// can later be tightened against evidence instead of a guess.
func capture(t *testing.T, name string, body []byte) {
	t.Helper()
	if err := os.MkdirAll("testdata", 0o755); err != nil {
		t.Fatalf("mkdir testdata: %v", err)
	}
	path := filepath.Join("testdata", name)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	t.Logf("captured %s: %s", path, body)
}

func TestLiveUnauthorizedBody(t *testing.T) {
	apiKey(t) // only run where a live API is reachable

	client, err := typesafe.New(typesafe.WithAPIKey("sk-deliberately-invalid"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = client.Ask(liveContext(t), "hello", typesafe.Noul("q", "Is this a greeting?"))
	if !errors.Is(err, typesafe.ErrUnauthorized) {
		t.Fatalf("Ask = %v, want ErrUnauthorized", err)
	}

	var apiErr *typesafe.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("Ask = %v, want a *typesafe.Error", err)
	}
	if apiErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("StatusCode = %d, want 401", apiErr.StatusCode)
	}
	if len(apiErr.Body) == 0 {
		t.Error("Body is empty; the raw response must be preserved")
	}
	capture(t, "error_401.json", apiErr.Body)
}

// postRawResponse sends a hand-built request body, bypassing the SDK, and
// returns the status and raw response.
//
// Some tests need to send what the SDK correctly refuses to send. That is the
// only reason to reach past the public API.
func postRawResponse(t *testing.T, key, body string) (int, []byte) {
	t.Helper()

	req, err := http.NewRequestWithContext(liveContext(t), http.MethodPost,
		"https://api.typesafe.ai/v1/systemone", bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading response: %v", err)
	}
	return resp.StatusCode, raw
}

func TestLiveUnprocessableBody(t *testing.T) {
	// The API documents criteria as required on a Choice, and the SDK
	// enforces that client-side, so a 422 cannot be provoked through the
	// public API. This posts the malformed body directly to learn the error
	// schema. The SDK's own 422 handling is covered hermetically by
	// TestAskDoesNotRetryValidationFailures in Task 5.
	key := apiKey(t)

	const malformed = `{"state":"hello","model":"jev-latest",` +
		`"questions":{"department":{"type":"choice","instructions":"Which team?"}}}`

	status, body := postRawResponse(t, key, malformed)
	t.Logf("a choice with no criteria returned status %d: %s", status, body)

	if status != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", status)
	}
	if len(body) == 0 {
		t.Fatal("body is empty; there is nothing to learn from")
	}
	capture(t, "error_unprocessable.json", body)
}

// postRaw sends a hand-built request body, bypassing the SDK, and returns the
// probability distribution for the named question.
//
// The SDK cannot express this test: Opts[T] is a Go map, and encoding/json
// sorts map keys, so every Choice the SDK sends arrives alphabetically
// ordered. Answering whether that matters requires raw JSON.
func postRaw(t *testing.T, key, body, questionID string) map[string]float64 {
	t.Helper()

	status, raw := postRawResponse(t, key, body)
	if status != http.StatusOK {
		t.Fatalf("status %d: %s", status, raw)
	}

	var decoded struct {
		Answers map[string]struct {
			Probabilities map[string]float64 `json:"probabilities"`
		} `json:"answers"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decoding %s: %v", raw, err)
	}
	return decoded.Answers[questionID].Probabilities
}

// spread is the largest per-option difference between two distributions.
func spread(a, b map[string]float64) float64 {
	var widest float64
	for option, p := range a {
		if d := math.Abs(p - b[option]); d > widest {
			widest = d
		}
	}
	return widest
}

func TestLiveChoiceOptionOrderDoesNotMoveTheDistribution(t *testing.T) {
	// Go maps have no insertion order and encoding/json sorts their keys, so
	// every Choice this SDK sends is alphabetically ordered. JavaScript
	// objects keep insertion order, so the JS SDK sends whatever the caller
	// wrote. If Jev's distribution depends on that order, then Opts[T] as a
	// map is the wrong type and must become an ordered builder.
	//
	// Jev is not deterministic, so ordering cannot be compared against a
	// single pair of calls. This measures same-order variation first and
	// judges the cross-order gap against it.
	key := apiKey(t)

	const forwardOptions = `"billing":"Payments, invoicing, refunds","technical":"Bugs, outages, integrations","sales":"Pricing, upgrades, new accounts"`
	const reverseOptions = `"sales":"Pricing, upgrades, new accounts","technical":"Bugs, outages, integrations","billing":"Payments, invoicing, refunds"`

	build := func(options string) string {
		return `{"state":"` + ticket + `","model":"jev-latest","questions":{` +
			`"department":{"type":"choice","instructions":"Which team should handle this?","criteria":{` +
			options + `}}}}`
	}

	forwardA := postRaw(t, key, build(forwardOptions), "department")
	forwardB := postRaw(t, key, build(forwardOptions), "department")
	reverseA := postRaw(t, key, build(reverseOptions), "department")
	reverseB := postRaw(t, key, build(reverseOptions), "department")

	t.Logf("forward A: %v", forwardA)
	t.Logf("forward B: %v", forwardB)
	t.Logf("reverse A: %v", reverseA)
	t.Logf("reverse B: %v", reverseB)

	sameOrder := math.Max(spread(forwardA, forwardB), spread(reverseA, reverseB))
	crossOrder := math.Max(spread(forwardA, reverseA), spread(forwardB, reverseB))
	t.Logf("same-order spread %.4f, cross-order spread %.4f", sameOrder, crossOrder)

	// A cross-order gap inside the noise floor means order does not matter.
	// The 0.05 floor keeps a perfectly deterministic model from failing on
	// a rounding difference.
	limit := math.Max(sameOrder*3, 0.05)
	if crossOrder > limit {
		t.Errorf("option order moved the distribution: cross-order spread %.4f exceeds %.4f.\n"+
			"Opts[T] must become an ordered type before tagging a release; see gotchas.md.",
			crossOrder, limit)
	}
}

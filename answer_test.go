// ABOUTME: Tests for typed answer reading through question handles.
// ABOUTME: Verify lazy decoding, type checking, and key conversion from wire format.

package typesafe

import (
	"encoding/json"
	"errors"
	"testing"
)

// resultWith builds a Result from alternating id and raw-JSON arguments.
func resultWith(t *testing.T, pairs ...string) *Result {
	t.Helper()
	if len(pairs)%2 != 0 {
		t.Fatalf("resultWith needs id/json pairs, got %d arguments", len(pairs))
	}
	answers := make(map[string]json.RawMessage, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		answers[pairs[i]] = json.RawMessage(pairs[i+1])
	}
	return &Result{Model: "jev-latest", answers: answers}
}

func TestNoulFromReadsProbability(t *testing.T) {
	q := Noul("is_urgent", "Does this convey urgency?")
	res := resultWith(t, "is_urgent", `{"type":"noul","noul":0.92}`)

	got, err := q.From(res)
	if err != nil {
		t.Fatalf("From: %v", err)
	}
	if got != 0.92 {
		t.Errorf("From = %v, want 0.92", got)
	}
}

func TestChoiceFromReturnsCallerTypes(t *testing.T) {
	type dept string
	const (
		billing   dept = "billing"
		technical dept = "technical"
		sales     dept = "sales"
	)

	q := Choice[dept]("department", "Which team?", Opts[dept]{
		billing: nil, technical: nil, sales: nil,
	})
	res := resultWith(t, "department", `{
	  "type": "choice",
	  "choice": "technical",
	  "probabilities": {"billing": 0.08, "technical": 0.85, "sales": 0.07},
	  "confidence": 0.82
	}`)

	got, err := q.From(res)
	if err != nil {
		t.Fatalf("From: %v", err)
	}
	if got.Value != technical {
		t.Errorf("Value = %q, want %q", got.Value, technical)
	}
	if got.Confidence != 0.82 {
		t.Errorf("Confidence = %v, want 0.82", got.Confidence)
	}
	if len(got.Probabilities) != 3 {
		t.Fatalf("Probabilities has %d entries, want 3", len(got.Probabilities))
	}
	if got.Probabilities[billing] != 0.08 {
		t.Errorf("Probabilities[billing] = %v, want 0.08", got.Probabilities[billing])
	}
}

func TestScoreFromConvertsStringKeysToLevels(t *testing.T) {
	// The wire format keys legend and probabilities by stringified level
	// index. Callers should never have to do that conversion.
	q := Score("frustration", "How frustrated?", "Calm", "Frustrated", "Very angry")
	res := resultWith(t, "frustration", `{
	  "type": "score",
	  "score": 1.6,
	  "legend": {"0": "Calm", "1": "Frustrated", "2": "Very angry"},
	  "probabilities": {"0": 0.05, "1": 0.3, "2": 0.65},
	  "confidence": 0.78
	}`)

	got, err := q.From(res)
	if err != nil {
		t.Fatalf("From: %v", err)
	}
	if got.Value != 1.6 {
		t.Errorf("Value = %v, want 1.6", got.Value)
	}
	if got.Legend[2] != "Very angry" {
		t.Errorf("Legend[2] = %q, want %q", got.Legend[2], "Very angry")
	}
	if got.Probabilities[2] != 0.65 {
		t.Errorf("Probabilities[2] = %v, want 0.65", got.Probabilities[2])
	}
	if got.Confidence != 0.78 {
		t.Errorf("Confidence = %v, want 0.78", got.Confidence)
	}
}

func TestFromReportsMissingAnswer(t *testing.T) {
	q := Noul("absent", "?")
	res := resultWith(t, "present", `{"type":"noul","noul":0.5}`)

	if _, err := q.From(res); !errors.Is(err, ErrNoAnswer) {
		t.Fatalf("From = %v, want ErrNoAnswer", err)
	}
}

func TestFromRejectsMismatchedAnswerType(t *testing.T) {
	// A Score handle pointed at a Noul answer must fail loudly rather than
	// decode into a zero value.
	q := Score("frustration", "How frustrated?", "Calm", "Angry")
	res := resultWith(t, "frustration", `{"type":"noul","noul":0.9}`)

	if _, err := q.From(res); !errors.Is(err, ErrWrongType) {
		t.Fatalf("From = %v, want ErrWrongType", err)
	}
}

func TestFromOnNilResultReportsMissingAnswer(t *testing.T) {
	q := Noul("q", "?")
	if _, err := q.From(nil); !errors.Is(err, ErrNoAnswer) {
		t.Fatalf("From = %v, want ErrNoAnswer", err)
	}
}

func TestScoreFromRejectsNonNumericLevelKeys(t *testing.T) {
	q := Score("frustration", "How frustrated?", "Calm", "Angry")
	res := resultWith(t, "frustration", `{
	  "type": "score",
	  "score": 1.0,
	  "legend": {"low": "Calm"},
	  "probabilities": {"0": 1.0},
	  "confidence": 0.5
	}`)

	if _, err := q.From(res); err == nil {
		t.Fatal("From succeeded on a non-numeric legend key, want an error")
	}
}

func TestSeparateHandlesReadSeparateAnswers(t *testing.T) {
	type dept string

	urgent := Noul("is_urgent", "?")
	team := Choice[dept]("department", "?", Opts[dept]{"technical": nil})

	res := resultWith(t,
		"is_urgent", `{"type":"noul","noul":0.92}`,
		"department", `{"type":"choice","choice":"technical","probabilities":{"technical":1.0},"confidence":0.9}`,
	)

	u, err := urgent.From(res)
	if err != nil {
		t.Fatalf("urgent.From: %v", err)
	}
	d, err := team.From(res)
	if err != nil {
		t.Fatalf("team.From: %v", err)
	}
	if u != 0.92 || d.Value != dept("technical") {
		t.Errorf("got %v and %q, want 0.92 and technical", u, d.Value)
	}
}

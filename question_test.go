// ABOUTME: Tests for the three TypeSafe question primitives and their wire encoding.
// ABOUTME: Verify JSON marshaling, validation, immutability, and the sealed interface.

package typesafe

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

// jsonEqual compares two JSON documents by value, ignoring key order.
func jsonEqual(t *testing.T, got, want []byte) bool {
	t.Helper()
	var gotVal, wantVal any
	if err := json.Unmarshal(got, &gotVal); err != nil {
		t.Fatalf("got is not JSON (%s): %v", got, err)
	}
	if err := json.Unmarshal(want, &wantVal); err != nil {
		t.Fatalf("want is not JSON (%s): %v", want, err)
	}
	return reflect.DeepEqual(gotVal, wantVal)
}

func TestNoulEncodesWithoutCriteria(t *testing.T) {
	q := Noul("is_urgent", "Does this convey urgency?")

	got, err := json.Marshal(q.payload())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	want := `{"type":"noul","instructions":"Does this convey urgency?"}`
	if !jsonEqual(t, got, []byte(want)) {
		t.Errorf("payload\n got: %s\nwant: %s", got, want)
	}
}

func TestNoulEncodesWithCriteria(t *testing.T) {
	q := Noul("is_urgent", "Does this convey urgency?").
		WithCriteria("Explicitly time-sensitive", "No urgency expressed")

	got, err := json.Marshal(q.payload())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	want := `{
	  "type": "noul",
	  "instructions": "Does this convey urgency?",
	  "criteria": {"true": "Explicitly time-sensitive", "false": "No urgency expressed"}
	}`
	if !jsonEqual(t, got, []byte(want)) {
		t.Errorf("payload\n got: %s\nwant: %s", got, want)
	}
}

func TestWithCriteriaDoesNotMutateTheOriginal(t *testing.T) {
	base := Noul("q", "Yes?")
	_ = base.WithCriteria("yes means", "no means")

	got, err := json.Marshal(base.payload())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if jsonEqual(t, got, []byte(`{"type":"noul","instructions":"Yes?","criteria":{}}`)) {
		t.Error("WithCriteria mutated the receiver; question values must be immutable")
	}
	if !jsonEqual(t, got, []byte(`{"type":"noul","instructions":"Yes?"}`)) {
		t.Errorf("base changed: %s", got)
	}
}

func TestChoiceEncodesOptionsAsCriteria(t *testing.T) {
	type dept string

	q := Choice[dept]("department", "Which team should handle this?", Opts[dept]{
		"billing":   "Payments, invoicing, refunds",
		"technical": "Bugs, outages, integrations",
		"sales":     "Pricing, upgrades, new accounts",
	})

	got, err := json.Marshal(q.payload())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	want := `{
	  "type": "choice",
	  "instructions": "Which team should handle this?",
	  "criteria": {
	    "billing": "Payments, invoicing, refunds",
	    "technical": "Bugs, outages, integrations",
	    "sales": "Pricing, upgrades, new accounts"
	  }
	}`
	if !jsonEqual(t, got, []byte(want)) {
		t.Errorf("payload\n got: %s\nwant: %s", got, want)
	}
}

func TestChoiceAllowsNilOptionDescription(t *testing.T) {
	// The API documents criteria as map<string, string | null>: null means
	// the option needs no extra detail.
	type color string

	q := Choice[color]("hue", "Which?", Opts[color]{"red": nil, "blue": "the cold one"})

	got, err := json.Marshal(q.payload())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	want := `{"type":"choice","instructions":"Which?","criteria":{"red":null,"blue":"the cold one"}}`
	if !jsonEqual(t, got, []byte(want)) {
		t.Errorf("payload\n got: %s\nwant: %s", got, want)
	}
}

func TestScoreEncodesLevelsAsOrderedArray(t *testing.T) {
	q := Score("frustration", "How frustrated is the customer?",
		"Calm", "Frustrated", "Very angry")

	got, err := json.Marshal(q.payload())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	want := `{
	  "type": "score",
	  "instructions": "How frustrated is the customer?",
	  "criteria": ["Calm", "Frustrated", "Very angry"]
	}`
	if !jsonEqual(t, got, []byte(want)) {
		t.Errorf("payload\n got: %s\nwant: %s", got, want)
	}
}

func TestStructuredInstructionsSurviveEncoding(t *testing.T) {
	// instructions accepts string | object | array, not just string.
	q := Noul("q", map[string]any{
		"question": "Is this a refund request?",
		"context":  []any{"support inbox", "EU region"},
	})

	got, err := json.Marshal(q.payload())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	want := `{
	  "type": "noul",
	  "instructions": {
	    "question": "Is this a refund request?",
	    "context": ["support inbox", "EU region"]
	  }
	}`
	if !jsonEqual(t, got, []byte(want)) {
		t.Errorf("payload\n got: %s\nwant: %s", got, want)
	}
}

func TestValidateRejectsEmptyChoice(t *testing.T) {
	type dept string

	q := Choice[dept]("department", "Which team?", Opts[dept]{})
	if err := q.validate(); !errors.Is(err, ErrNoOptions) {
		t.Fatalf("validate = %v, want ErrNoOptions", err)
	}
}

func TestValidateRejectsScoreWithOneLevel(t *testing.T) {
	// The API requires at least two levels.
	q := Score("frustration", "How frustrated?", "Calm")
	if err := q.validate(); !errors.Is(err, ErrTooFewLevels) {
		t.Fatalf("validate = %v, want ErrTooFewLevels", err)
	}
}

func TestQuestionsReportTheirIDs(t *testing.T) {
	type dept string

	questions := []Question{
		Noul("a", "?"),
		Choice[dept]("b", "?", Opts[dept]{"x": nil}),
		Score("c", "?", "low", "high"),
	}
	want := []string{"a", "b", "c"}

	for i, q := range questions {
		if q.ID() != want[i] {
			t.Errorf("questions[%d].ID() = %q, want %q", i, q.ID(), want[i])
		}
	}
}

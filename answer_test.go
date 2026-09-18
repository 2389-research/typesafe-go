// ABOUTME: Tests for typed answer reading through question handles.
// ABOUTME: Verify lazy decoding, type checking, and key conversion from wire format.

package typesafe

import (
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
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

// TestFromRejectsTruncatedAnswers covers a response whose payload field is
// absent or null. Zero is a meaningful answer in all three shapes — a Noul
// of 0 is "certainly not", a confidence of 0 is no confidence, and an empty
// option string is a real value the API could not send — so a decode into
// the zero value would read a truncated body as a confident judgment.
func TestFromRejectsTruncatedAnswers(t *testing.T) {
	type dept string

	tests := []struct {
		name  string
		read  func(*Result) error
		raw   string
		field string
	}{
		{
			name:  "noul payload absent",
			read:  func(r *Result) error { _, err := Noul("q", "?").From(r); return err },
			raw:   `{"type":"noul"}`,
			field: "noul",
		},
		{
			name:  "noul payload null",
			read:  func(r *Result) error { _, err := Noul("q", "?").From(r); return err },
			raw:   `{"type":"noul","noul":null}`,
			field: "noul",
		},
		{
			name:  "choice option absent",
			read:  func(r *Result) error { _, err := Choice[dept]("q", "?", Opts[dept]{"a": nil}).From(r); return err },
			raw:   `{"type":"choice","probabilities":{"a":1},"confidence":0.9}`,
			field: "choice",
		},
		{
			name:  "choice probabilities absent",
			read:  func(r *Result) error { _, err := Choice[dept]("q", "?", Opts[dept]{"a": nil}).From(r); return err },
			raw:   `{"type":"choice","choice":"a","confidence":0.9}`,
			field: "probabilities",
		},
		{
			name:  "choice probabilities empty",
			read:  func(r *Result) error { _, err := Choice[dept]("q", "?", Opts[dept]{"a": nil}).From(r); return err },
			raw:   `{"type":"choice","choice":"a","probabilities":{},"confidence":0.9}`,
			field: "probabilities",
		},
		{
			name:  "choice confidence absent",
			read:  func(r *Result) error { _, err := Choice[dept]("q", "?", Opts[dept]{"a": nil}).From(r); return err },
			raw:   `{"type":"choice","choice":"a","probabilities":{"a":1}}`,
			field: "confidence",
		},
		{
			name:  "score value absent",
			read:  func(r *Result) error { _, err := Score("q", "?", "low", "high").From(r); return err },
			raw:   `{"type":"score","legend":{"0":"low","1":"high"},"probabilities":{"0":1},"confidence":0.5}`,
			field: "score",
		},
		{
			name:  "score legend absent",
			read:  func(r *Result) error { _, err := Score("q", "?", "low", "high").From(r); return err },
			raw:   `{"type":"score","score":1,"probabilities":{"0":1},"confidence":0.5}`,
			field: "legend",
		},
		{
			name:  "score probabilities absent",
			read:  func(r *Result) error { _, err := Score("q", "?", "low", "high").From(r); return err },
			raw:   `{"type":"score","score":1,"legend":{"0":"low"},"confidence":0.5}`,
			field: "probabilities",
		},
		{
			name:  "score probabilities empty",
			read:  func(r *Result) error { _, err := Score("q", "?", "low", "high").From(r); return err },
			raw:   `{"type":"score","score":1,"legend":{"0":"low"},"probabilities":{},"confidence":0.5}`,
			field: "probabilities",
		},
		{
			name:  "score confidence absent",
			read:  func(r *Result) error { _, err := Score("q", "?", "low", "high").From(r); return err },
			raw:   `{"type":"score","score":1,"legend":{"0":"low"},"probabilities":{"0":1}}`,
			field: "confidence",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := resultWith(t, "q", tt.raw)
			err := tt.read(res)
			if !errors.Is(err, ErrIncompleteAnswer) {
				t.Fatalf("From = %v, want ErrIncompleteAnswer", err)
			}
			if !strings.Contains(err.Error(), tt.field) {
				t.Errorf("err = %q, want it to name the field %q", err, tt.field)
			}
		})
	}
}

// TestFromReadsGenuineZeroValues is the counterpart to the truncation test:
// the same fields present with zero values must decode cleanly. Tracking
// presence must not turn a real zero into an error.
func TestFromReadsGenuineZeroValues(t *testing.T) {
	type dept string

	noul, err := Noul("q", "?").From(resultWith(t, "q", `{"type":"noul","noul":0}`))
	if err != nil {
		t.Fatalf("Noul.From: %v", err)
	}
	if noul != 0 {
		t.Errorf("Noul.From = %v, want 0", noul)
	}

	choice, err := Choice[dept]("q", "?", Opts[dept]{"a": nil}).
		From(resultWith(t, "q", `{"type":"choice","choice":"a","probabilities":{"a":0},"confidence":0}`))
	if err != nil {
		t.Fatalf("Choice.From: %v", err)
	}
	if choice.Value != dept("a") || choice.Confidence != 0 {
		t.Errorf("Choice.From = %+v, want value a and confidence 0", choice)
	}
	if len(choice.Probabilities) != 1 || choice.Probabilities[dept("a")] != 0 {
		t.Errorf("Probabilities = %v, want {a: 0}", choice.Probabilities)
	}

	score, err := Score("q", "?", "low", "high").
		From(resultWith(t, "q", `{"type":"score","score":0,"legend":{"0":"low","1":"high"},"probabilities":{"0":0,"1":0},"confidence":0}`))
	if err != nil {
		t.Fatalf("Score.From: %v", err)
	}
	if score.Value != 0 || score.Confidence != 0 {
		t.Errorf("Score.From = %+v, want value 0 and confidence 0", score)
	}
	if len(score.Probabilities) != 2 {
		t.Errorf("Probabilities = %v, want two level entries", score.Probabilities)
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

func TestScoreFromRejectsNonNumericProbabilityKeys(t *testing.T) {
	// Legend keys are valid here so the loop reaches the Probabilities
	// conversion, the branch this test targets, rather than failing
	// earlier on the Legend conversion this same fixture would also fail
	// if its legend keys were non-numeric.
	q := Score("frustration", "How frustrated?", "Calm", "Angry")
	res := resultWith(t, "frustration", `{
	  "type": "score",
	  "score": 1.0,
	  "legend": {"0": "Calm", "1": "Angry"},
	  "probabilities": {"low": 1.0},
	  "confidence": 0.5
	}`)

	_, err := q.From(res)
	if err == nil {
		t.Fatal("From succeeded on a non-numeric probability key, want an error")
	}
	if !strings.Contains(err.Error(), "probability") {
		t.Errorf("err = %q, want it to mention the probability key", err)
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

func TestRawAnswerReturnsTheStoredBytes(t *testing.T) {
	res := resultWith(t, "is_urgent", `{"type":"noul","noul":0.92}`)

	raw, err := res.RawAnswer("is_urgent")
	if err != nil {
		t.Fatalf("RawAnswer: %v", err)
	}
	if string(raw) != `{"type":"noul","noul":0.92}` {
		t.Errorf("RawAnswer = %s, want the exact stored bytes", raw)
	}
}

func TestRawAnswerHandsBackACopy(t *testing.T) {
	// A caller must not be able to reach into the Result and corrupt it:
	// mutating the returned slice has to stay on the caller's side.
	res := resultWith(t, "is_urgent", `{"type":"noul","noul":0.92}`)

	raw, err := res.RawAnswer("is_urgent")
	if err != nil {
		t.Fatalf("RawAnswer: %v", err)
	}
	copy(raw, []byte(`{"type":"noul","noul":0.50}`))

	again, err := res.RawAnswer("is_urgent")
	if err != nil {
		t.Fatalf("RawAnswer again: %v", err)
	}
	if string(again) != `{"type":"noul","noul":0.92}` {
		t.Errorf("stored answer changed to %s; the caller mutated shared state", again)
	}

	// The typed reader must be unaffected too.
	q := Noul("is_urgent", "?")
	p, err := q.From(res)
	if err != nil || p != 0.92 {
		t.Errorf("From after caller mutation = (%v, %v), want 0.92 and no error", p, err)
	}
}

func TestRawAnswerRejectsUnknownAndNil(t *testing.T) {
	res := resultWith(t, "is_urgent", `{"type":"noul","noul":0.92}`)

	if _, err := res.RawAnswer("missing"); !errors.Is(err, ErrNoAnswer) {
		t.Errorf("RawAnswer(missing) = %v, want ErrNoAnswer", err)
	}
	var nilResult *Result
	if _, err := nilResult.RawAnswer("is_urgent"); !errors.Is(err, ErrNoAnswer) {
		t.Errorf("RawAnswer on nil = %v, want ErrNoAnswer", err)
	}
}

// resultDoc returns the doc comment attached to the Result type, with the
// comment markers stripped, so a test can assert what the comment claims.
func resultDoc(t *testing.T) string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "answer.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parsing answer.go: %v", err)
	}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name.Name != "Result" {
				continue
			}
			// The comment sits above the `type` keyword, so the parser
			// attaches it to the GenDecl, not the TypeSpec.
			doc := ts.Doc
			if doc == nil {
				doc = gen.Doc
			}
			if doc == nil {
				t.Fatal("Result has no doc comment to check")
			}
			return doc.Text()
		}
	}
	t.Fatal("answer.go has no Result type with a doc comment")
	return ""
}

func TestResultDocPromisesOnlyTheShapeGuarantee(t *testing.T) {
	doc := resultDoc(t)

	// The false claim: two handles can share an id and differ in type,
	// so a mismatched read compiles and fails at read time instead.
	if strings.Contains(doc, "will not compile") {
		t.Errorf("Result doc still claims a mismatched read will not compile:\n%s", doc)
	}
	// The real guarantee: the handle fixes the answer shape at compile time.
	if !strings.Contains(doc, "float64") {
		t.Errorf("Result doc does not state the compile-time shape guarantee:\n%s", doc)
	}
	// Where the runtime check lives, for the id-collision case.
	if !strings.Contains(doc, "ErrWrongType") {
		t.Errorf("Result doc does not point at ErrWrongType for the id-collision case:\n%s", doc)
	}
}

func TestMismatchedReadOfSharedIDFailsAtReadTime(t *testing.T) {
	// Two handles agree on the id "mood" and disagree on its type. Both
	// are legal Go, so the mismatched read compiles; ErrWrongType is what
	// catches it.
	type tone string
	asked := Choice[tone]("mood", "?", Opts[tone]{"terse": nil})
	misread := Noul("mood", "?")

	res := resultWith(t, "mood", `{
	  "type": "choice",
	  "choice": "terse",
	  "probabilities": {"terse": 1.0},
	  "confidence": 0.9
	}`)

	if _, err := misread.From(res); !errors.Is(err, ErrWrongType) {
		t.Fatalf("misread.From = %v, want ErrWrongType", err)
	}
	if _, err := asked.From(res); err != nil {
		t.Fatalf("asked.From through the right handle: %v", err)
	}
}

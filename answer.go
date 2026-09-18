// ABOUTME: Typed answers and the Result that holds them until a handle reads one.
// ABOUTME: Answers decode lazily, so callers pay only for what they actually read.

package typesafe

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// Usage reports token consumption for one request. TypeSafe bills input
// tokens only.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// Result holds the answers to one Ask.
//
// Read an answer through the question value that asked it — urgent.From(res)
// — rather than by key. Each handle's From fixes the answer's shape at
// compile time: NoulQuestion.From returns float64 and nothing else, and
// ChoiceQuestion[T].From returns ChoiceAnswer[T].
//
// The compiler cannot check the id, though. Two handles may share an id and
// disagree about its type, and that read compiles; it fails at read time
// with ErrWrongType rather than decoding a zero value.
type Result struct {
	// Model is the model that performed the evaluation. When you send an
	// alias like jev-latest, this is the concrete version it resolved to.
	Model string
	Usage Usage

	answers map[string]json.RawMessage
}

// rawAnswer finds one answer and checks its type tag before any decoding.
// The tag is the only presence check the whole answer gets: each From below
// is responsible for rejecting a payload field its answer body omits.
func (r *Result) rawAnswer(id, want string) (json.RawMessage, error) {
	if r == nil {
		return nil, fmt.Errorf("%w: %q (nil result)", ErrNoAnswer, id)
	}
	raw, ok := r.answers[id]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrNoAnswer, id)
	}

	var tag struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &tag); err != nil {
		return nil, fmt.Errorf("typesafe: decoding answer %q: %w", id, err)
	}
	if tag.Type != want {
		return nil, fmt.Errorf("%w: answer %q is %q, wanted %q", ErrWrongType, id, tag.Type, want)
	}
	return raw, nil
}

// missingField reports an answer body that omits a field the API documents
// as always present. Decoding such a body into the zero value would turn a
// truncated response into a confident judgment.
func missingField(id, field string) error {
	return fmt.Errorf("%w: answer %q has no %q", ErrIncompleteAnswer, id, field)
}

// RawAnswer returns the exact wire bytes the API sent for one answer,
// keyed by question id, with no decoding or type checking applied. The
// caller owns the returned copy: mutating it cannot affect the Result, so
// raw answer evidence can be retained and logged without sharing mutable
// state with the typed readers.
//
// This is the supported way to keep exact raw answer evidence — the bytes
// the API returned, not a re-encoding of them. A question id the response
// did not carry reports ErrNoAnswer.
func (r *Result) RawAnswer(id string) (json.RawMessage, error) {
	if r == nil {
		return nil, fmt.Errorf("%w: %q (nil result)", ErrNoAnswer, id)
	}
	raw, ok := r.answers[id]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrNoAnswer, id)
	}
	out := make(json.RawMessage, len(raw))
	copy(out, raw)
	return out, nil
}

// From reads this question's answer: the probability of yes, from 0 to 1.
// An answer body that omits or nulls the noul field reports
// ErrIncompleteAnswer instead of decoding to 0.
//
// A Noul carries no confidence field. The probability is the whole answer:
// 0.5 means the model is genuinely torn, not that it is unsure.
func (q NoulQuestion) From(r *Result) (float64, error) {
	raw, err := r.rawAnswer(q.id, "noul")
	if err != nil {
		return 0, err
	}
	var answer struct {
		Noul *float64 `json:"noul"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		return 0, fmt.Errorf("typesafe: decoding answer %q: %w", q.id, err)
	}
	if answer.Noul == nil {
		return 0, missingField(q.id, "noul")
	}
	return *answer.Noul, nil
}

// ChoiceAnswer is one Choice result, in the caller's own option type.
type ChoiceAnswer[T ~string] struct {
	// Value is the highest-probability option.
	Value T
	// Probabilities maps every option to its probability. They sum to 1.
	Probabilities map[T]float64
	// Confidence is how certain the model is, derived from Probabilities.
	Confidence float64
}

// From reads this question's answer, typed as the option type you declared.
// An answer body missing choice, probabilities, or confidence reports
// ErrIncompleteAnswer; an empty probabilities map counts as missing, since
// the API documents it as summing to 1.
func (q ChoiceQuestion[T]) From(r *Result) (ChoiceAnswer[T], error) {
	var out ChoiceAnswer[T]

	raw, err := r.rawAnswer(q.id, "choice")
	if err != nil {
		return out, err
	}
	var answer struct {
		Choice        *string            `json:"choice"`
		Probabilities map[string]float64 `json:"probabilities"`
		Confidence    *float64           `json:"confidence"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		return out, fmt.Errorf("typesafe: decoding answer %q: %w", q.id, err)
	}
	if answer.Choice == nil {
		return out, missingField(q.id, "choice")
	}
	if len(answer.Probabilities) == 0 {
		return out, missingField(q.id, "probabilities")
	}
	if answer.Confidence == nil {
		return out, missingField(q.id, "confidence")
	}

	out.Value = T(*answer.Choice)
	out.Confidence = *answer.Confidence
	out.Probabilities = make(map[T]float64, len(answer.Probabilities))
	for option, p := range answer.Probabilities {
		out.Probabilities[T(option)] = p
	}
	return out, nil
}

// ScoreAnswer is one Score result.
type ScoreAnswer struct {
	// Value is probability-weighted across the levels and can land between
	// two of them: 1.6 sits closer to level 2 than to level 1.
	Value float64
	// Legend maps each level index back to the description you supplied.
	Legend map[int]string
	// Probabilities maps each level index to its probability. They sum to 1.
	Probabilities map[int]float64
	// Confidence is how certain the model is, derived from Probabilities.
	Confidence float64
}

// From reads this question's answer, with the wire format's string level keys
// converted back to the integer indexes you passed levels in. An answer body
// missing score, legend, probabilities, or confidence reports
// ErrIncompleteAnswer; empty legend or probabilities maps count as missing.
func (q ScoreQuestion) From(r *Result) (ScoreAnswer, error) {
	var out ScoreAnswer

	raw, err := r.rawAnswer(q.id, "score")
	if err != nil {
		return out, err
	}
	var answer struct {
		Score         *float64           `json:"score"`
		Legend        map[string]string  `json:"legend"`
		Probabilities map[string]float64 `json:"probabilities"`
		Confidence    *float64           `json:"confidence"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		return out, fmt.Errorf("typesafe: decoding answer %q: %w", q.id, err)
	}
	if answer.Score == nil {
		return out, missingField(q.id, "score")
	}
	if len(answer.Legend) == 0 {
		return out, missingField(q.id, "legend")
	}
	if len(answer.Probabilities) == 0 {
		return out, missingField(q.id, "probabilities")
	}
	if answer.Confidence == nil {
		return out, missingField(q.id, "confidence")
	}

	out.Value = *answer.Score
	out.Confidence = *answer.Confidence

	out.Legend = make(map[int]string, len(answer.Legend))
	for key, description := range answer.Legend {
		level, err := strconv.Atoi(key)
		if err != nil {
			return ScoreAnswer{}, fmt.Errorf("typesafe: answer %q has non-numeric legend key %q", q.id, key)
		}
		out.Legend[level] = description
	}

	out.Probabilities = make(map[int]float64, len(answer.Probabilities))
	for key, p := range answer.Probabilities {
		level, err := strconv.Atoi(key)
		if err != nil {
			return ScoreAnswer{}, fmt.Errorf("typesafe: answer %q has non-numeric probability key %q", q.id, key)
		}
		out.Probabilities[level] = p
	}

	return out, nil
}

// ABOUTME: The three TypeSafe question primitives and their wire encoding.
// ABOUTME: Question values are immutable handles that later read their own answers.

package typesafe

import (
	"fmt"
	"maps"
	"slices"
)

// Entry is any JSON-encodable value the API accepts where structure is
// allowed: a string, an object, or an array.
//
// Instructions, Choice option descriptions, Score level descriptions, and
// Noul true/false criteria all take this shape. Structured entries let you
// hand the model a rubric instead of a sentence.
//
// Upstream documents null as valid only for a Choice option's description
// (criteria is map<string, string | null>: null means the option needs no
// extra detail). Nothing in the docs says null is valid for Instructions,
// Score levels, or Noul true/false criteria.
type Entry = any

// Question is one typed judgment in a request.
//
// The interface is sealed by its unexported methods: only Noul, Choice, and
// Score satisfy it, because the API accepts nothing else.
type Question interface {
	// ID is the key this question's answer comes back under.
	ID() string

	payload() questionPayload
	validate() error
}

// questionPayload is the wire form shared by all three question types.
type questionPayload struct {
	Type         string `json:"type"`
	Instructions Entry  `json:"instructions"`
	Criteria     any    `json:"criteria,omitempty"`
}

// NoulQuestion asks a yes/no question. Build one with Noul.
type NoulQuestion struct {
	id           string
	instructions Entry
	criteria     *noulCriteria
}

type noulCriteria struct {
	True  Entry `json:"true"`
	False Entry `json:"false"`
}

// Noul asks a yes/no question. The answer is the probability of yes, from
// 0 to 1. A Noul carries no confidence: the probability is the whole answer.
func Noul(id string, instructions Entry) NoulQuestion {
	return NoulQuestion{id: id, instructions: instructions}
}

// WithCriteria describes what a yes and a no mean, which sharpens the
// probability. It returns a copy; question values never change in place.
func (q NoulQuestion) WithCriteria(yes, no Entry) NoulQuestion {
	q.criteria = &noulCriteria{True: yes, False: no}
	return q
}

// ID is the key this question's answer comes back under.
func (q NoulQuestion) ID() string { return q.id }

func (q NoulQuestion) payload() questionPayload {
	p := questionPayload{Type: "noul", Instructions: q.instructions}
	if q.criteria != nil {
		p.Criteria = q.criteria
	}
	return p
}

func (q NoulQuestion) validate() error { return nil }

// Opts maps each Choice option to its description. A nil description means
// the option needs no extra detail.
type Opts[T ~string] map[T]Entry

// ChoiceQuestion picks one option from a set you define. Build one with Choice.
type ChoiceQuestion[T ~string] struct {
	id           string
	instructions Entry
	options      Opts[T]
}

// Choice picks one option from a set you define. Declare T as your own string
// type and the compiler will reject any option the API could not return.
func Choice[T ~string](id string, instructions Entry, options Opts[T]) ChoiceQuestion[T] {
	return ChoiceQuestion[T]{id: id, instructions: instructions, options: maps.Clone(options)}
}

// ID is the key this question's answer comes back under.
func (q ChoiceQuestion[T]) ID() string { return q.id }

func (q ChoiceQuestion[T]) payload() questionPayload {
	criteria := make(map[string]Entry, len(q.options))
	for option, description := range q.options {
		criteria[string(option)] = description
	}
	return questionPayload{Type: "choice", Instructions: q.instructions, Criteria: criteria}
}

func (q ChoiceQuestion[T]) validate() error {
	if len(q.options) == 0 {
		return fmt.Errorf("%w: question %q", ErrNoOptions, q.id)
	}
	return nil
}

// ScoreQuestion rates the state against ordered levels. Build one with Score.
type ScoreQuestion struct {
	id           string
	instructions Entry
	levels       []Entry
}

// Score rates the state against levels given lowest first. The answer is
// probability-weighted across them and can land between two levels.
// The API requires at least two.
func Score(id string, instructions Entry, levels ...Entry) ScoreQuestion {
	return ScoreQuestion{id: id, instructions: instructions, levels: slices.Clone(levels)}
}

// ID is the key this question's answer comes back under.
func (q ScoreQuestion) ID() string { return q.id }

func (q ScoreQuestion) payload() questionPayload {
	return questionPayload{Type: "score", Instructions: q.instructions, Criteria: q.levels}
}

func (q ScoreQuestion) validate() error {
	if len(q.levels) < 2 {
		return fmt.Errorf("%w: question %q has %d", ErrTooFewLevels, q.id, len(q.levels))
	}
	return nil
}

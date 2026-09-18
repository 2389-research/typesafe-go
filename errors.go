// ABOUTME: Typed errors for the TypeSafe API, with sentinels for errors.Is.
// ABOUTME: The error body schema is undocumented, so raw bodies are preserved.

package typesafe

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// statusOverloaded is TypeSafe's 529 response. net/http has no constant for
// it because it is not a registered status code.
const statusOverloaded = 529

// Sentinel errors. Match these with errors.Is rather than comparing status
// codes at the call site.
var (
	ErrUnauthorized  = errors.New("typesafe: unauthorized")
	ErrUnprocessable = errors.New("typesafe: unprocessable entity")
	ErrRateLimited   = errors.New("typesafe: rate limited")
	ErrOverloaded    = errors.New("typesafe: overloaded")
	ErrServer        = errors.New("typesafe: server error")

	ErrNoAnswer  = errors.New("typesafe: no answer for question id")
	ErrWrongType = errors.New("typesafe: answer type does not match question type")

	ErrNoAPIKey     = errors.New("typesafe: no API key")
	ErrNoQuestions  = errors.New("typesafe: no questions")
	ErrNilQuestion  = errors.New("typesafe: nil question")
	ErrEmptyID      = errors.New("typesafe: empty question id")
	ErrDuplicateID  = errors.New("typesafe: duplicate question id")
	ErrNoOptions    = errors.New("typesafe: choice has no options")
	ErrTooFewLevels = errors.New("typesafe: score needs at least two levels")
)

// Error is an HTTP-level failure from the TypeSafe API.
//
// TypeSafe documents its status codes but not the shape of its error bodies,
// so Body carries the response verbatim and Message is a best-effort guess.
// Read Body when you need certainty.
type Error struct {
	StatusCode int
	Body       []byte
	Message    string
}

func (e *Error) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("typesafe: %s (status %d)", e.Message, e.StatusCode)
	}
	return fmt.Sprintf("typesafe: request failed with status %d", e.StatusCode)
}

// Is maps status codes onto the package sentinels so callers can write
// errors.Is(err, typesafe.ErrRateLimited) instead of unpacking the status.
func (e *Error) Is(target error) bool {
	switch target {
	case ErrUnauthorized:
		return e.StatusCode == http.StatusUnauthorized
	case ErrUnprocessable:
		return e.StatusCode == http.StatusUnprocessableEntity
	case ErrRateLimited:
		return e.StatusCode == http.StatusTooManyRequests
	case ErrOverloaded:
		return e.StatusCode == statusOverloaded
	case ErrServer:
		return e.StatusCode >= 500 && e.StatusCode <= 599 && e.StatusCode != statusOverloaded
	}
	return false
}

// newError builds an Error from a response.
//
// The message probe checks the keys most APIs use. It is a convenience, not a
// contract: when it finds nothing, Body still holds the whole response.
// The live tests capture real error bodies from the API so this guess can
// be replaced with the actual schema.
func newError(status int, body []byte) *Error {
	e := &Error{StatusCode: status, Body: body}

	var probe map[string]any
	if json.Unmarshal(body, &probe) != nil {
		return e
	}
	for _, key := range []string{"message", "error", "detail"} {
		if s, ok := probe[key].(string); ok && s != "" {
			e.Message = s
			return e
		}
	}
	return e
}

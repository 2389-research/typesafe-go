package typesafe

import (
	"errors"
	"net/http"
	"testing"
)

func TestErrorMatchesSentinelsByStatus(t *testing.T) {
	cases := []struct {
		status int
		want   error
	}{
		{http.StatusUnauthorized, ErrUnauthorized},
		{http.StatusUnprocessableEntity, ErrUnprocessable},
		{http.StatusTooManyRequests, ErrRateLimited},
		{529, ErrOverloaded},
		{http.StatusInternalServerError, ErrServer},
		{http.StatusBadGateway, ErrServer},
	}

	for _, tc := range cases {
		err := newError(tc.status, []byte(`{}`))
		if !errors.Is(err, tc.want) {
			t.Errorf("status %d: errors.Is(err, %v) = false, want true", tc.status, tc.want)
		}
	}
}

func TestOverloadedIsNotAGenericServerError(t *testing.T) {
	// 529 has its own sentinel; folding it into ErrServer would lose the
	// distinction between "TypeSafe is busy" and "TypeSafe is broken".
	err := newError(529, []byte(`{}`))
	if errors.Is(err, ErrServer) {
		t.Error("529 matched ErrServer, want only ErrOverloaded")
	}
}

func TestErrorPreservesRawBody(t *testing.T) {
	// The API's error schema is undocumented, so the raw bytes are the only
	// thing guaranteed to carry the truth.
	body := []byte(`{"anything":"at all"}`)
	err := newError(422, body)

	if string(err.Body) != string(body) {
		t.Errorf("Body = %q, want %q", err.Body, body)
	}
}

func TestErrorExtractsMessageWhenPresent(t *testing.T) {
	err := newError(401, []byte(`{"message":"invalid api key"}`))
	if err.Message != "invalid api key" {
		t.Errorf("Message = %q, want %q", err.Message, "invalid api key")
	}
}

func TestErrorSurvivesUnparseableBody(t *testing.T) {
	err := newError(500, []byte("not json at all"))
	if err.Message != "" {
		t.Errorf("Message = %q, want empty", err.Message)
	}
	if err.Error() == "" {
		t.Error("Error() returned empty string")
	}
}

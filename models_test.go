// ABOUTME: Tests for the Models call and model listing.
// ABOUTME: Verify the GET request, authorization header, and response parsing.

package typesafe

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestModelsCallsTheModelsEndpoint(t *testing.T) {
	var gotPath, gotMethod, gotAuth string
	var gotBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		gotAuth = r.Header.Get("Authorization")
		gotBody, _ = io.ReadAll(r.Body)
		_, _ = io.WriteString(w, `{"models":[
		  {"name":"jev-latest","description":"Alias for the current stable release","release_date":"2026-08-01"},
		  {"name":"jev-1.13.0","description":"Jev 1.13","release_date":"2026-08-01"}
		]}`)
	}))
	defer srv.Close()

	models, err := testClient(t, srv.URL).Models(context.Background())
	if err != nil {
		t.Fatalf("Models: %v", err)
	}

	if gotPath != "/v1/models" {
		t.Errorf("path = %q, want /v1/models", gotPath)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %q, want GET", gotMethod)
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("Authorization = %q, want Bearer test-key", gotAuth)
	}
	if len(gotBody) != 0 {
		t.Errorf("GET carried a body: %q", gotBody)
	}

	if len(models) != 2 {
		t.Fatalf("got %d models, want 2", len(models))
	}
	if models[0].Name != "jev-latest" {
		t.Errorf("models[0].Name = %q, want jev-latest", models[0].Name)
	}
	if models[0].ReleaseDate != "2026-08-01" {
		t.Errorf("models[0].ReleaseDate = %q, want 2026-08-01", models[0].ReleaseDate)
	}
	if models[1].Description != "Jev 1.13" {
		t.Errorf("models[1].Description = %q, want %q", models[1].Description, "Jev 1.13")
	}
}

func TestModelsPropagatesAPIErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"message":"invalid api key"}`)
	}))
	defer srv.Close()

	if _, err := testClient(t, srv.URL).Models(context.Background()); err == nil {
		t.Fatal("Models succeeded on a 401, want an error")
	}
}

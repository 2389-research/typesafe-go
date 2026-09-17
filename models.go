// ABOUTME: Listing the models an account may name in the model field.
// ABOUTME: Release dates stay strings; the API does not document their format.

package typesafe

import (
	"context"
	"net/http"
)

// Model is one model or alias the account can use.
type Model struct {
	// Name is what you pass to WithModel, such as jev-latest.
	Name        string `json:"name"`
	Description string `json:"description"`
	// ReleaseDate is the API's value, unparsed. Its format is not
	// documented, so turning it into a time.Time would be a guess.
	ReleaseDate string `json:"release_date"`
}

// Models lists the models available to the account, newest aliases included.
func (c *Client) Models(ctx context.Context) ([]Model, error) {
	var response struct {
		Models []Model `json:"models"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/models", nil, &response); err != nil {
		return nil, err
	}
	return response.Models, nil
}

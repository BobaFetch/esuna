// Package jev scores review findings using TypeSafe's Noul probability.
package jev

import (
	"errors"
	"net/http"
	"os"
	"time"
)

// Client communicates with the TypeSafe System One API.
type Client struct {
	httpClient *http.Client
	apiKey     string
	baseURL    string
}

// NewClient reads TYPESAFE_API_KEY. Scoring is required, so an absent key is fatal
// to the caller's review run; help and flag parsing do not require credentials.
func NewClient() (*Client, error) {
	key := os.Getenv("TYPESAFE_API_KEY")
	if key == "" {
		return nil, errors.New("TYPESAFE_API_KEY is not set; esuna requires TypeSafe for confidence scoring and will not run without it")
	}
	return &Client{httpClient: &http.Client{Timeout: 60 * time.Second}, apiKey: key, baseURL: "https://api.typesafe.ai"}, nil
}

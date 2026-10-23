package jev

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewClient(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "")
	if _, err := NewClient(); err == nil || err.Error() != "TYPESAFE_API_KEY is not set; esuna requires TypeSafe for confidence scoring and will not run without it" {
		t.Fatalf("missing key error = %v", err)
	}
	t.Setenv("TYPESAFE_API_KEY", "test-key")
	c, err := NewClient()
	if err != nil {
		t.Fatal(err)
	}
	if c.apiKey != "test-key" || c.baseURL != "https://api.typesafe.ai" || c.httpClient.Timeout <= 0 {
		t.Fatalf("unexpected client configuration: baseURL=%q", c.baseURL)
	}
}

func TestScoreFindingInvalidResponse(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"unauthorized", 401, `{"detail":"invalid key"}`},
		{"rate limit", 429, `{"detail":"rate limited"}`},
		{"server error", 500, `upstream unavailable`},
		{"invalid JSON", 200, `<html>bad gateway</html>`},
		{"truncated", 200, `{"answers":`},
		{"empty", 200, ``},
		{"missing answer", 200, `{"answers":{}}`},
		{"wrong id", 200, `{"answers":{"other":{"type":"noul","noul":0.8}}}`},
		{"missing noul", 200, `{"answers":{"true_positive":{"type":"noul"}}}`},
		{"null noul", 200, `{"answers":{"true_positive":{"type":"noul","noul":null}}}`},
		{"wrong type", 200, `{"answers":{"true_positive":{"type":"score","noul":0.8}}}`},
		{"missing type", 200, `{"answers":{"true_positive":{"noul":0.8}}}`},
		{"string noul", 200, `{"answers":{"true_positive":{"type":"noul","noul":"0.8"}}}`},
		{"negative", 200, `{"answers":{"true_positive":{"type":"noul","noul":-0.1}}}`},
		{"too large", 200, `{"answers":{"true_positive":{"type":"noul","noul":1.1}}}`},
		{"overflow", 200, `{"answers":{"true_positive":{"type":"noul","noul":1e999}}}`},
		{"trailing JSON", 200, `{"answers":{"true_positive":{"type":"noul","noul":0.8}}} {}`},
		{"trailing garbage", 200, `{"answers":{"true_positive":{"type":"noul","noul":0.8}}} invalid`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			c := &Client{apiKey: "test-key", baseURL: server.URL, httpClient: server.Client()}
			if got, err := c.ScoreFinding(context.Background(), "finding", "diff", "content"); err == nil {
				t.Fatalf("ScoreFinding = %v, nil; want error", got)
			}
		})
	}
}

func TestScoreFindingBoundaries(t *testing.T) {
	for _, value := range []string{"0", "1"} {
		t.Run(value, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"answers":{"true_positive":{"type":"noul","noul":` + value + `}}}`))
			}))
			defer server.Close()
			c := &Client{apiKey: "test-key", baseURL: server.URL, httpClient: server.Client()}
			got, err := c.ScoreFinding(context.Background(), "finding", "diff", "content")
			if err != nil || (value == "0" && got != 0) || (value == "1" && got != 1) {
				t.Fatalf("ScoreFinding = %v, %v; want %s", got, err, value)
			}
		})
	}
}

func TestScoreFindingCanceled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("canceled request reached server") }))
	defer server.Close()
	c := &Client{apiKey: "test-key", baseURL: server.URL, httpClient: server.Client()}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.ScoreFinding(ctx, "finding", "diff", "content"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled error = %v", err)
	}
}

func TestScoreFinding(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/systemone" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" || r.Header.Get("Content-Type") != "application/json" {
			t.Error("missing auth or JSON header")
		}
		var payload struct {
			Model     string            `json:"model"`
			State     map[string]string `json:"state"`
			Questions map[string]struct {
				Type         string `json:"type"`
				Instructions string `json:"instructions"`
			} `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload.Model != "jev-latest" || len(payload.Questions) != 1 {
			t.Errorf("model/questions = %+v", payload)
		}
		q := payload.Questions["true_positive"]
		if q.Type != "noul" || !strings.Contains(q.Instructions, "true positive") {
			t.Errorf("question = %+v", q)
		}
		if payload.State["finding"] != "nil dereference\n\"quoted\"" || payload.State["diff"] != "+return p.Name" || payload.State["file_content"] != "func name() {}" {
			t.Errorf("state = %+v", payload.State)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"jev-1.13.0","answers":{"true_positive":{"type":"noul","noul":0.87}},"usage":{"input_tokens":120,"output_tokens":12}}`))
	}))
	defer server.Close()
	c := &Client{apiKey: "test-key", baseURL: server.URL, httpClient: server.Client()}
	got, err := c.ScoreFinding(context.Background(), "nil dereference\n\"quoted\"", "+return p.Name", "func name() {}")
	if err != nil || got != 0.87 {
		t.Fatalf("ScoreFinding = %v, %v; want 0.87, nil", got, err)
	}
}

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

func TestScoreFindingTokenLimitDetail(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"detail":{"error_type":"max_tokens_exceeded"}}`))
	}))
	defer server.Close()
	c := &Client{baseURL: server.URL, httpClient: server.Client()}
	_, err := c.ScoreFinding(context.Background(), "finding", "diff", "content")
	if err == nil || !strings.Contains(err.Error(), "max_tokens_exceeded") || !strings.Contains(err.Error(), "HTTP 400") || !strings.Contains(err.Error(), "exhausted context budgets") || calls != 3 {
		t.Fatalf("token limit error = %v", err)
	}
}

func TestScoreFindingBoundsErrorDetail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(strings.Repeat("x", 100000) + "SECRET_TAIL"))
	}))
	defer server.Close()
	c := &Client{baseURL: server.URL, httpClient: server.Client()}
	_, err := c.ScoreFinding(context.Background(), "finding", "diff", "content")
	if err == nil || len(err.Error()) > 4200 || strings.Contains(err.Error(), "SECRET_TAIL") || !strings.Contains(err.Error(), strings.Repeat("x", 4096)) {
		t.Fatalf("error detail was not bounded: %v", err)
	}
}

func TestScoreFindingBudgetsContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			State map[string]string `json:"state"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		for key, limit := range map[string]int{"diff": 24 * 1024, "file_content": 8 * 1024} {
			value := payload.State[key]
			if len(value) > limit || !strings.Contains(value, "[... context omitted ...]") || !strings.HasPrefix(value, "START") || !strings.HasSuffix(value, "END") || strings.Contains(value, "\ufffd") {
				t.Errorf("%s: length=%d, missing valid marked head/tail context", key, len(value))
			}
		}
		if payload.State["finding"] != "finding" {
			t.Error("finding changed")
		}
		_, _ = w.Write([]byte(`{"answers":{"true_positive":{"type":"noul","noul":0.8}}}`))
	}))
	defer server.Close()
	c := &Client{baseURL: server.URL, httpClient: server.Client()}
	large := "START" + strings.Repeat("界", 40000) + "END"
	if got, err := c.ScoreFinding(context.Background(), "finding", large, large); err != nil || got != 0.8 {
		t.Fatalf("ScoreFinding = %v, %v", got, err)
	}
}

func TestScoreFindingRetriesTokenLimit(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			previous := 1 << 30
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var payload struct {
					State map[string]string `json:"state"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				size := len(payload.State["diff"]) + len(payload.State["file_content"])
				if size >= previous {
					t.Errorf("context did not shrink: %d >= %d", size, previous)
				}
				previous = size
				if calls < 3 {
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"detail":{"error_type":"max_tokens_exceeded"}}`))
					return
				}
				_, _ = w.Write([]byte(`{"answers":{"true_positive":{"type":"noul","noul":0.7}}}`))
			}))
			defer server.Close()
			c := &Client{baseURL: server.URL, httpClient: server.Client()}
			large := strings.Repeat("x", 100000)
			got, err := c.ScoreFinding(context.Background(), "finding", large, large)
			if status == http.StatusBadRequest {
				if err != nil || got != 0.7 || calls != 3 {
					t.Fatalf("got %v, %v after %d calls", got, err, calls)
				}
			} else if err == nil || calls != 1 {
				t.Fatalf("got %v after %d calls; want no retry", err, calls)
			}
		})
	}
}

func TestScoreFindingRejectsOversizedFinding(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("oversized finding reached server")
	}))
	defer server.Close()
	c := &Client{baseURL: server.URL, httpClient: server.Client()}
	_, err := c.ScoreFinding(context.Background(), strings.Repeat("x", 8193), "diff", "content")
	if err == nil || !strings.Contains(err.Error(), "finding exceeds") {
		t.Fatalf("error = %v", err)
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

package pipeline

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"esuna/internal/contract"
	"esuna/internal/reviewer"
)

type fakeRunner struct {
	model reviewer.Model
	ask   func(context.Context, string) (string, error)
}

func (f fakeRunner) Model() reviewer.Model { return f.model }
func (f fakeRunner) Review(ctx context.Context, prompt string) (string, error) {
	return f.ask(ctx, prompt)
}

func TestChain(t *testing.T) {
	calls := 0
	ctx := context.WithValue(context.Background(), "test", "context")
	pool := []reviewer.Runner{
		fakeRunner{reviewer.Claude, func(got context.Context, prompt string) (string, error) {
			if got != ctx || !strings.Contains(prompt, "base context") || !strings.Contains(prompt, ReviewerOutputFormat) {
				t.Fatal("missing context or format")
			}
			calls++
			return "a.go|10|P2|nil pointer\nround-one prose", nil
		}},
		fakeRunner{reviewer.Codex, func(_ context.Context, prompt string) (string, error) {
			if calls != 1 || !strings.Contains(prompt, "prior findings so far:") || !strings.Contains(prompt, "nil pointer") || !strings.Contains(prompt, "round-one prose") {
				t.Fatal("missing prior round")
			}
			calls++
			return "a.go|11|P0|nil pointer in handler\nb.go|2|P1|leak", nil
		}},
	}
	got, err := Chain(ctx, pool, "base context")
	if err != nil || calls != 2 || len(got) != 2 || got[0].Severity != "P0" || got[0].Confidence != 0 {
		t.Fatalf("got %#v, %v (%d calls)", got, err, calls)
	}
	got, err = Chain(ctx, nil, "base")
	if err != nil || len(got) != 0 {
		t.Fatalf("empty: %#v %v", got, err)
	}
	boom := errors.New("CLI failed")
	pool[0] = fakeRunner{reviewer.Claude, func(context.Context, string) (string, error) { return "", boom }}
	if _, err = Chain(ctx, pool, "base"); !errors.Is(err, boom) {
		t.Fatalf("error lost: %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = Chain(canceled, pool, "base"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestVerify(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "a.go")
	if err := os.WriteFile(file, []byte("first\ncurrent line\n"), 0600); err != nil {
		t.Fatal(err)
	}
	findings := []contract.Finding{{File: "a.go", Line: 2, Severity: "P1", Message: "defect"}}
	for _, reply := range []string{"VALID: NO", "VALID: YES", "unclear", "not VALID: NO", "VALID: NO\nVALID: YES", ""} {
		t.Run(reply, func(t *testing.T) {
			runner := fakeRunner{reviewer.Claude, func(_ context.Context, prompt string) (string, error) {
				if !strings.Contains(prompt, "current line") || !strings.Contains(prompt, "defect") || !strings.Contains(prompt, "claimed line 2") {
					t.Fatalf("missing current evidence: %s", prompt)
				}
				return reply, nil
			}}
			got, err := Verify(context.Background(), runner, findings, root)
			want := 1
			if reply == "VALID: NO" {
				want = 0
			}
			if err != nil || len(got) != want {
				t.Fatalf("got %#v %v, want length %d", got, err, want)
			}
			if len(got) > 0 && got[0].Confidence != 0 {
				t.Fatal("confidence set before scoring")
			}
		})
	}
	// Each finding re-reads the file rather than sharing a stale snapshot.
	calls := 0
	runner := fakeRunner{reviewer.Codex, func(_ context.Context, prompt string) (string, error) {
		calls++
		if calls == 1 {
			if err := os.WriteFile(file, []byte("changed\n"), 0600); err != nil {
				t.Fatal(err)
			}
		} else if !strings.Contains(prompt, "changed") {
			t.Fatal("stale content")
		}
		return "VALID: YES", nil
	}}
	if _, err := Verify(context.Background(), runner, append(findings, findings...), root); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("CLI failed")
	runner.ask = func(context.Context, string) (string, error) { return "", boom }
	if _, err := Verify(context.Background(), runner, findings, root); !errors.Is(err, boom) {
		t.Fatalf("error lost: %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Verify(canceled, runner, findings, root); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	runner.ask = func(_ context.Context, prompt string) (string, error) {
		if !strings.Contains(prompt, "file is missing") {
			t.Fatal("missing-file context absent")
		}
		return "unclear", nil
	}
	missing := []contract.Finding{{File: "deleted.go", Line: 1, Severity: "P1", Message: "defect"}}
	if got, err := Verify(context.Background(), runner, missing, root); err != nil || len(got) != 1 {
		t.Fatalf("missing: %#v %v", got, err)
	}
	for _, path := range []string{"../outside", root} {
		bad := []contract.Finding{{File: path, Line: 1}}
		if _, err := Verify(context.Background(), runner, bad, root); err == nil {
			t.Fatalf("accepted unsafe path %q", path)
		}
	}
	if findings[0].Message != "defect" {
		t.Fatal("input mutated")
	}
}

func TestMerge(t *testing.T) {
	results := map[reviewer.Model]string{
		reviewer.Claude: "./a.go|10|P2|Nil pointer dereference\na.go|10|P1|resource leak\na.go|20|P2|Nil pointer dereference",
		reviewer.Codex:  "a.go|12|P0|nil   pointer dereference in handler\nb.go|10|P1|Nil pointer dereference",
	}
	want := []contract.Finding{
		{File: "a.go", Line: 10, Severity: "P0", Message: "Nil pointer dereference"},
		{File: "a.go", Line: 10, Severity: "P1", Message: "resource leak"},
		{File: "a.go", Line: 20, Severity: "P2", Message: "Nil pointer dereference"},
		{File: "b.go", Line: 10, Severity: "P1", Message: "Nil pointer dereference"},
	}
	for range 20 {
		got, err := Merge(results)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v, %v; want %#v", got, err, want)
		}
	}
	got, err := Merge(nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("empty: %#v, %v", got, err)
	}
}

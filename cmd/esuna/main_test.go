package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"esuna/internal/config"
	"esuna/internal/contract"
	"esuna/internal/repocontext"
	"esuna/internal/reviewer"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRunPipeline(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s %v", args, out, err)
		}
	}
	git("init", "-b", "feature")
	git("config", "user.name", "Test")
	git("config", "user.email", "test@example.invalid")
	git("config", "commit.gpgsign", "false")
	write := func(name, content string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), mode); err != nil {
			t.Fatal(err)
		}
	}
	write("code.go", "package fixture\n// old\n", 0600)
	git("add", "code.go")
	git("commit", "-m", "fixture")
	write("code.go", "package fixture\n// new\n", 0600)
	if err := os.Mkdir(filepath.Join(root, ".esuna"), 0700); err != nil {
		t.Fatal(err)
	}
	guidance := "UNIQUE GUIDANCE\nKeep this newline.\n"
	write(".esuna/guidelines.md", guidance, 0600)
	write(".esuna.conf", "reviewer_pool=claude\nauthor_model=claude\ngate_threshold=0.8\n", 0600)
	bin := t.TempDir()
	script := "#!/bin/sh\nprompt=$(cat)\nprintf '%s\\n' \"$prompt\" >> \"$ESUNA_TEST_PROMPTS\"\ncase \"$prompt\" in\n *'Reply only VALID:'*) printf 'VALID: YES\\n' ;;\n *) printf 'code.go|2|P0|Known test defect\\n' ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("TYPESAFE_API_KEY", "test-only")
	log := filepath.Join(root, "prompts.log")
	t.Setenv("ESUNA_TEST_PROMPTS", log)
	old := http.DefaultTransport
	defer func() { http.DefaultTransport = old }()
	calls := 0
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.URL.String() != "https://api.typesafe.ai/v1/systemone" {
			t.Errorf("URL: %s", req.URL)
		}
		var payload struct {
			State map[string]string `json:"state"`
		}
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(payload.State["diff"], "+// new") || payload.State["file_content"] != "package fixture\n// new\n" || !strings.Contains(payload.State["finding"], "Known test defect") {
			t.Errorf("scoring evidence: %+v", payload.State)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"answers":{"true_positive":{"type":"noul","noul":0.8}}}`)), Header: make(http.Header)}, nil
	})
	sub := filepath.Join(root, "subdir")
	if err := os.Mkdir(sub, 0700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)
	for _, tc := range []struct {
		name string
		args []string
		code int
	}{
		{"default merge advisory", []string{"--json", "--author-model=codex"}, 0},
		{"merge gate at threshold", []string{"--json", "--merge", "--gate", "--author-model=codex"}, 1},
		{"chain verify gate", []string{"--json", "--chain", "--verify", "--gate", "--author-model=muse"}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(tc.args, &stdout, &stderr)
			if code != tc.code || stderr.String() != "reviewing 1 files, +1/-1, base HEAD..HEAD\n" {
				t.Fatalf("exit %d, stderr %s, stdout %s", code, &stderr, &stdout)
			}
			var result contract.Result
			if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if len(result.Findings) != 1 || result.Findings[0].Confidence != 0.8 || result.OverallConfidence != 0.8 {
				t.Fatalf("result: %+v", result)
			}
		})
	}
	if calls != 3 {
		t.Fatalf("scoring calls: %d", calls)
	}
	prompts, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(prompts), guidance) != 4 || !strings.Contains(string(prompts), "FILE|LINE|SEVERITY|MESSAGE") || !strings.Contains(string(prompts), "prior findings so far") {
		t.Fatalf("missing prompt guidance/history: %s", prompts)
	}
	var out, errs bytes.Buffer
	if code := run([]string{"--json"}, &out, &errs); code != 1 || !strings.Contains(errs.String(), "empty") || out.Len() != 0 {
		t.Fatalf("config author exclusion: %d %s %s", code, &out, &errs)
	}

	// Exercise the real run with synthetic reviewer findings and HTTP fixtures.
	for _, tc := range []struct {
		name         string
		findings     []contract.Finding
		status       int
		transportErr error
		wantFiles    string
		wantCalls    int
		wantCode     int
		warning      string
		reason       string
	}{
		{name: "missing file keeps other findings", findings: []contract.Finding{{File: "code.go", Line: 1}, {File: "missing.go", Line: 7}, {File: "code.go", Line: 2}}, wantFiles: "code.go,code.go", wantCalls: 2, warning: "missing.go:7"},
		{name: "single score failure keeps others", findings: []contract.Finding{{File: "code.go", Line: 1}, {File: "code.go", Line: 2}}, status: 422, wantFiles: "code.go", wantCalls: 2, warning: "code.go:1"},
		{name: "bad input keeps others", findings: []contract.Finding{{File: "code.go", Line: 1}, {File: "code.go", Line: 2}}, status: 400, wantFiles: "code.go", wantCalls: 2, warning: "code.go:1"},
		{name: "oversized input keeps others", findings: []contract.Finding{{File: "code.go", Line: 1}, {File: "code.go", Line: 2}}, status: 413, wantFiles: "code.go", wantCalls: 2, warning: "code.go:1"},
		{name: "transport error fails fast", findings: []contract.Finding{{File: "code.go", Line: 1}, {File: "code.go", Line: 2}}, transportErr: errors.New("dial tcp: connect: connection refused"), wantCalls: 1, wantCode: 1},
		{name: "file read error keeps others", findings: []contract.Finding{{File: ".esuna", Line: 7}, {File: "code.go", Line: 2}}, wantFiles: "code.go", wantCalls: 1, warning: ".esuna:7", reason: "is a directory"},
		{name: "quota fails fast", findings: []contract.Finding{{File: "code.go", Line: 1}, {File: "code.go", Line: 2}}, status: 429, wantCalls: 1, wantCode: 1},
		{name: "all file reads skipped", findings: []contract.Finding{{File: "missing.go", Line: 7}}, warning: "missing.go:7"},
		{name: "all scores skipped", findings: []contract.Finding{{File: "code.go", Line: 1}}, status: 422, wantCalls: 1, warning: "code.go:1"},
		{name: "auth fails fast", findings: []contract.Finding{{File: "code.go", Line: 1}, {File: "code.go", Line: 2}}, status: 401, wantCalls: 1, wantCode: 1},
		{name: "forbidden fails fast", findings: []contract.Finding{{File: "code.go", Line: 1}, {File: "code.go", Line: 2}}, status: 403, wantCalls: 1, wantCode: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var raw strings.Builder
			for _, finding := range tc.findings {
				fmt.Fprintf(&raw, "%s|%d|P1|Synthetic defect at line %d\n", finding.File, finding.Line, finding.Line)
			}
			t.Setenv("ESUNA_TEST_FINDINGS", raw.String())
			if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\nprintf '%s' \"$ESUNA_TEST_FINDINGS\"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0700); err != nil {
					t.Error(err)
				}
			}()
			transport := http.DefaultTransport
			defer func() { http.DefaultTransport = transport }()
			calls := 0
			http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				if tc.transportErr != nil {
					return nil, tc.transportErr
				}
				status := http.StatusOK
				body := `{"answers":{"true_positive":{"type":"noul","noul":0.8}}}`
				if calls == 1 && tc.status != 0 {
					status, body = tc.status, "synthetic scoring failure"
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})
			var stdout, stderr bytes.Buffer
			code := run([]string{"--json", "--author-model="}, &stdout, &stderr)
			if code != tc.wantCode || calls != tc.wantCalls {
				t.Fatalf("exit %d, calls %d; stdout %s; stderr %s", code, calls, &stdout, &stderr)
			}
			if tc.wantCode != 0 {
				reason := fmt.Sprintf("HTTP %d", tc.status)
				if tc.transportErr != nil {
					reason = "TypeSafe scoring request:"
					if !strings.Contains(stderr.String(), tc.transportErr.Error()) {
						t.Fatalf("missing transport error: %s", &stderr)
					}
				}
				if stdout.Len() != 0 || !strings.Contains(stderr.String(), reason) || strings.Contains(stderr.String(), "skipping") {
					t.Fatalf("fatal error: stdout %s, stderr %s", &stdout, &stderr)
				}
				t.Logf("synthetic run exit=%d, scoring calls=%d\nstdout: %sstderr: %s", code, calls, &stdout, &stderr)
				return
			}
			var result contract.Result
			if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			var files []string
			for _, f := range result.Findings {
				files = append(files, f.File)
				if f.Confidence != 0.8 {
					t.Fatalf("unscored finding: %+v", f)
				}
			}
			wantConfidence := 0.8
			if tc.wantFiles == "" {
				wantConfidence = 1
			}
			if strings.Join(files, ",") != tc.wantFiles || result.OverallConfidence != wantConfidence || result.Findings == nil {
				t.Fatalf("result: %+v", result)
			}
			if !strings.Contains(stderr.String(), "esuna: warning:") || !strings.Contains(stderr.String(), tc.warning) || !strings.Contains(stderr.String(), "skipping") {
				t.Fatalf("missing warning: %s", &stderr)
			}
			reason := tc.reason
			if reason == "" {
				reason = "file does not exist"
			}
			if tc.status != 0 {
				reason = fmt.Sprintf("HTTP %d", tc.status)
			}
			if !strings.Contains(stderr.String(), reason) {
				t.Fatalf("missing warning reason: %s", &stderr)
			}
			t.Logf("synthetic run exit=%d\nstdout: %sstderr: %s", code, &stdout, &stderr)
		})
	}

	// A service-wide scoring failure must still abort, not produce a successful report.
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})
	out.Reset()
	errs.Reset()
	if code := run([]string{"--json", "--author-model="}, &out, &errs); code != 1 || out.Len() != 0 || !strings.Contains(errs.String(), "HTTP 503") {
		t.Fatalf("scoring failure: %d %s %s", code, &out, &errs)
	}
	// An empty review must avoid scoring and serialize [] rather than null.
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errs.Reset()
	if code := run([]string{"--json", "--gate", "--author-model="}, &out, &errs); code != 0 || errs.String() != "reviewing 1 files, +1/-1, base HEAD..HEAD\n" {
		t.Fatalf("empty review: %d %s %s", code, &out, &errs)
	}
	if !strings.Contains(out.String(), `"overall_confidence":1`) || !strings.Contains(out.String(), `"findings":[]`) {
		t.Fatalf("empty JSON: %s", &out)
	}
	// CLI failure must also abort with no partial JSON.
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\nexit 9\n"), 0700); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errs.Reset()
	if code := run([]string{"--json", "--author-model="}, &out, &errs); code != 1 || out.Len() != 0 || !strings.Contains(errs.String(), "claude reviewer") {
		t.Fatalf("reviewer failure: %d %s %s", code, &out, &errs)
	}

	// A missing configured CLI must not prevent the remaining reviewer running.
	write(".esuna.conf", "reviewer_pool=grok,claude\n", 0600)
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\nprintf 'ran\\n' >> \"$ESUNA_TEST_PROMPTS\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	oldLookup := lookPath
	lookPath = func(name string) (string, error) {
		if name == "grok" {
			return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
		}
		return exec.LookPath(name)
	}
	t.Cleanup(func() { lookPath = oldLookup })
	for _, mode := range []string{"--merge", "--chain"} {
		out.Reset()
		errs.Reset()
		if code := run([]string{"--json", mode}, &out, &errs); code != 0 {
			t.Fatalf("partial pool %s: exit %d, stdout %s, stderr %s", mode, code, &out, &errs)
		}
		if errs.String() != "esuna: warning: \"grok\" not found on PATH, skipping\nreviewing 1 files, +1/-1, base HEAD..HEAD\n" || !strings.Contains(out.String(), `"findings":[]`) {
			t.Fatalf("partial pool %s: stdout %s, stderr %s", mode, &out, &errs)
		}
	}
	prompts, err = os.ReadFile(log)
	if err != nil || strings.Count(string(prompts), "ran\n") != 2 {
		t.Fatalf("remaining reviewer did not run twice: %s, %v", prompts, err)
	}
}

func TestRunReviewerOverride(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s %v", args, out, err)
		}
	}
	git("init", "-b", "feature")
	git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "fixture")
	if err := os.WriteFile(filepath.Join(root, "change.go"), []byte("package fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "change.go")
	configText := "reviewer_pool=claude\n"
	if err := os.WriteFile(filepath.Join(root, ".esuna.conf"), []byte(configText), 0600); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	log := filepath.Join(root, "invocations.log")
	for _, name := range []string{"claude", "codex", "muse", "grok"} {
		script := "#!/bin/sh\nwhile IFS= read -r line; do :; done\nprintf '%s\\n' '" + name + "' >> \"$ESUNA_TEST_INVOCATIONS\"\n"
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("TYPESAFE_API_KEY", "test-only")
	t.Setenv("ESUNA_TEST_INVOCATIONS", log)
	t.Chdir(root)
	oldLookup := lookPath
	t.Cleanup(func() { lookPath = oldLookup })
	for _, tc := range []struct {
		name, invoked, lookedUp, missing, errorText string
		args                                        []string
		code                                        int
	}{
		{name: "override", args: []string{"--reviewer=codex"}, invoked: "codex\n", lookedUp: "codex"},
		{name: "unset uses config", invoked: "claude\n", lookedUp: "claude"},
		{name: "author excluded", args: []string{"--reviewer=claude,codex", "--author-model=claude"}, invoked: "codex\n", lookedUp: "codex"},
		{name: "author empties pool", args: []string{"--reviewer=codex", "--author-model=codex"}, code: 1, errorText: "Reviewer pool is empty"},
		{name: "invalid", args: []string{"--reviewer=invalid"}, code: 2, errorText: "--reviewer must contain distinct models: claude, codex, muse, grok"},
		{name: "duplicate", args: []string{"--reviewer=codex,codex"}, code: 2, errorText: "--reviewer must contain distinct models: claude, codex, muse, grok"},
		{name: "empty", args: []string{"--reviewer="}, code: 2, errorText: "--reviewer must contain distinct models"},
		{name: "empty entry", args: []string{"--reviewer=codex,"}, code: 2, errorText: "--reviewer must contain distinct models"},
		{name: "case sensitive", args: []string{"--reviewer=Codex"}, code: 2, errorText: "--reviewer must contain distinct models"},
		{name: "trim and preserve chain order", args: []string{"--reviewer= grok , muse , codex , claude ", "--chain"}, invoked: "grok\nmuse\ncodex\nclaude\n", lookedUp: "grok,muse,codex,claude"},
		{name: "skip missing", args: []string{"--reviewer=grok,codex"}, missing: "grok", invoked: "codex\n", lookedUp: "grok,codex"},
		{name: "missing empties pool", args: []string{"--reviewer=codex"}, missing: "codex", lookedUp: "codex", code: 1, errorText: "Reviewer pool is empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(log, nil, 0600); err != nil {
				t.Fatal(err)
			}
			var lookedUp []string
			lookPath = func(name string) (string, error) {
				lookedUp = append(lookedUp, name)
				if name == tc.missing {
					return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
				}
				return exec.LookPath(name)
			}
			var stdout, stderr bytes.Buffer
			if code := run(append([]string{"--json"}, tc.args...), &stdout, &stderr); code != tc.code {
				t.Fatalf("exit %d, want %d, stdout %s, stderr %s", code, tc.code, &stdout, &stderr)
			}
			if got := strings.Join(lookedUp, ","); got != tc.lookedUp {
				t.Fatalf("lookups: %q, want %q", got, tc.lookedUp)
			}
			invocations, err := os.ReadFile(log)
			if err != nil || string(invocations) != tc.invoked {
				t.Fatalf("invocations: %q, want %q, %v", invocations, tc.invoked, err)
			}
			wantStderr := ""
			if tc.missing != "" {
				wantStderr = "esuna: warning: \"" + tc.missing + "\" not found on PATH, skipping\n"
			}
			if tc.code == 0 {
				if !strings.Contains(stdout.String(), `"findings":[]`) || stderr.String() != wantStderr+"reviewing 1 files, +1/-0, base HEAD..HEAD\n" {
					t.Fatalf("stdout %s, stderr %s", &stdout, &stderr)
				}
			} else if stdout.Len() != 0 || !strings.Contains(stderr.String(), tc.errorText) || !strings.HasPrefix(stderr.String(), wantStderr) {
				t.Fatalf("stdout %s, stderr %s, want error %q", &stdout, &stderr, tc.errorText)
			}
			contents, err := os.ReadFile(filepath.Join(root, ".esuna.conf"))
			if err != nil || string(contents) != configText {
				t.Fatalf("config changed: %q, %v", contents, err)
			}
		})
	}
}

func TestConfiguredPoolOrder(t *testing.T) {
	old := lookPath
	lookPath = func(name string) (string, error) { return "/fixture/" + name, nil }
	t.Cleanup(func() { lookPath = old })
	cfg := &config.Config{ReviewerPool: []reviewer.Model{reviewer.Grok, reviewer.Claude, reviewer.Codex}, AuthorModel: "claude"}
	pool, err := configuredPool(cfg, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(pool) != 2 || pool[0].Model() != reviewer.Grok || pool[1].Model() != reviewer.Codex {
		t.Fatalf("pool: %v", pool)
	}
}

func TestConfiguredPoolSkipsMissingCLI(t *testing.T) {
	old := lookPath
	lookPath = func(name string) (string, error) {
		if name == "grok" {
			return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
		}
		return "/fixture/" + name, nil
	}
	t.Cleanup(func() { lookPath = old })
	cfg := &config.Config{ReviewerPool: []reviewer.Model{reviewer.Grok, reviewer.Muse, reviewer.Claude}}
	var stderr bytes.Buffer
	pool, err := configuredPool(cfg, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if len(pool) != 2 || pool[0].Model() != reviewer.Muse || pool[1].Model() != reviewer.Claude {
		t.Fatalf("pool: %v", pool)
	}
	if got, want := stderr.String(), "esuna: warning: \"grok\" not found on PATH, skipping\n"; got != want {
		t.Fatalf("warning: got %q, want %q", got, want)
	}
}

func TestRunMissingReviewersFailsLoudly(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "init", root).CombinedOutput(); err != nil {
		t.Fatalf("init: %s %v", out, err)
	}
	if err := os.WriteFile(filepath.Join(root, ".esuna.conf"), []byte("reviewer_pool=claude,grok\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	t.Setenv("TYPESAFE_API_KEY", "test-only")
	old := lookPath
	t.Cleanup(func() { lookPath = old })
	for _, author := range []string{"", "claude"} {
		t.Run("author="+author, func(t *testing.T) {
			var lookedUp []string
			lookPath = func(name string) (string, error) {
				lookedUp = append(lookedUp, name)
				return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
			}
			var stdout, stderr bytes.Buffer
			code := run([]string{"--json", "--author-model=" + author}, &stdout, &stderr)
			if code != 1 || stdout.Len() != 0 {
				t.Fatalf("exit %d, stdout %s, stderr %s", code, &stdout, &stderr)
			}
			if !strings.Contains(stderr.String(), "Reviewer pool is empty") || !strings.Contains(stderr.String(), "skipping missing CLIs") {
				t.Fatalf("missing clear empty-pool error: %s", &stderr)
			}
			wantLookups := "claude,grok"
			if author == "claude" {
				wantLookups = "grok"
			}
			if strings.Join(lookedUp, ",") != wantLookups {
				t.Fatalf("lookups %v, want %s", lookedUp, wantLookups)
			}
			for _, name := range lookedUp {
				if !strings.Contains(stderr.String(), "esuna: warning: \""+name+"\" not found on PATH, skipping\n") {
					t.Fatalf("missing warning for %s: %s", name, &stderr)
				}
			}
		})
	}
}

func TestPromptDeepAndTests(t *testing.T) {
	bundle := &repocontext.Bundle{Diff: "DIFF EVIDENCE", Files: map[string]string{"b.go": "B CONTENT", "a.go": "A CONTENT"}, Symbols: map[string][]repocontext.Symbol{"a.go": {{Name: "Function", Kind: "func", Line: 1}}}}
	prompt := buildPrompt(bundle, "GUIDANCE\n", true)
	for _, text := range []string{"DIFF EVIDENCE", "A CONTENT", "B CONTENT", "Repository symbol map:", "Function", "GUIDANCE\n", "Include test code", "FILE|LINE|SEVERITY|MESSAGE"} {
		if !strings.Contains(prompt, text) {
			t.Fatalf("missing %q in %s", text, prompt)
		}
	}
	if strings.Index(prompt, "File a.go:") > strings.Index(prompt, "File b.go:") {
		t.Fatal("unstable file order")
	}
}

func TestRunInstallHook(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "init", root).CombinedOutput(); err != nil {
		t.Fatalf("init: %s %v", out, err)
	}
	t.Setenv("TYPESAFE_API_KEY", "")
	var out, errs bytes.Buffer
	if code := run([]string{"install-hook", "--target", root, "--gate"}, &out, &errs); code != 0 || errs.Len() != 0 {
		t.Fatalf("install: %d %s", code, &errs)
	}
	data, err := os.ReadFile(filepath.Join(root, ".git/hooks/pre-push"))
	if err != nil || !strings.Contains(string(data), "--gate") {
		t.Fatalf("hook: %s %v", data, err)
	}
}

func TestReviewBaseIncludesCommittedChanges(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s %v", args, out, err)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-b", "feature")
	git("config", "user.name", "Test")
	git("config", "user.email", "test@example.invalid")
	git("config", "commit.gpgsign", "false")
	git("commit", "--allow-empty", "-m", "initial")
	if got := reviewBase(root); got != "HEAD" {
		t.Fatalf("initial base: %s", got)
	}
	initial := git("rev-parse", "HEAD")
	git("branch", "upstream-fixture")
	git("commit", "--allow-empty", "-m", "second")
	if got := reviewBase(root); got != initial {
		t.Fatalf("new branch base: %s want %s", got, initial)
	}
	git("commit", "--allow-empty", "-m", "third")
	git("branch", "--set-upstream-to=upstream-fixture")
	if got := reviewBase(root); got != initial {
		t.Fatalf("upstream base: %s want %s", got, initial)
	}
}

func TestReviewBaseCandidateOrder(t *testing.T) {
	for _, refs := range [][]string{
		{"origin/main", "origin/master", "main", "master"},
		{"origin/master", "main", "master"}, {"main", "master"}, {"master"}, {},
	} {
		t.Run(strings.Join(refs, ","), func(t *testing.T) {
			root := t.TempDir()
			git := func(args ...string) string {
				t.Helper()
				out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
				if err != nil {
					t.Fatalf("git %v: %s %v", args, out, err)
				}
				return strings.TrimSpace(string(out))
			}
			git("init", "-b", "feature")
			git("config", "user.name", "Test")
			git("config", "user.email", "test@example.invalid")
			git("config", "commit.gpgsign", "false")
			git("commit", "--allow-empty", "-m", "initial")
			if got := reviewBase(root); got != "HEAD" {
				t.Fatalf("initial: %s", got)
			}
			want := git("rev-parse", "HEAD")
			for i, ref := range refs {
				name := "refs/heads/" + ref
				if strings.HasPrefix(ref, "origin/") {
					name = "refs/remotes/" + ref
				}
				diverged := git("commit-tree", "HEAD^{tree}", "-p", "HEAD", "-m", "diverged mainline")
				git("update-ref", name, diverged)
				git("commit", "--allow-empty", "-m", "candidate")
				if i == len(refs)-1 {
					git("branch", "tracking")
				}
			}
			git("commit", "--allow-empty", "-m", "second")
			previous := git("rev-parse", "HEAD")
			git("commit", "--allow-empty", "-m", "third")
			if len(refs) == 0 {
				want = previous
			}
			if got := reviewBase(root); got != want {
				t.Fatalf("base %s, want %s", got, want)
			}
			if len(refs) > 0 {
				git("branch", "--set-upstream-to=tracking")
				want = git("rev-parse", "tracking")
				if got := reviewBase(root); got != want {
					t.Fatalf("upstream %s, want %s", got, want)
				}
			}
		})
	}
}

func TestRunBaseAndReviewHeader(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s %v", args, out, err)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-b", "main")
	git("config", "user.name", "Test")
	git("config", "user.email", "test@example.invalid")
	git("config", "commit.gpgsign", "false")
	git("commit", "--allow-empty", "-m", "initial")
	initial := git("rev-parse", "HEAD")
	git("switch", "-c", "feature")
	for _, name := range []string{"one.go", "two.go", "three.go"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("package fixture\n"), 0600); err != nil {
			t.Fatal(err)
		}
		git("add", name)
		git("commit", "-m", name)
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\ncat > \"$ESUNA_TEST_PROMPTS\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(bin, "prompt")
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("TYPESAFE_API_KEY", "test-only")
	t.Setenv("ESUNA_TEST_PROMPTS", log)
	t.Chdir(root)
	for _, tc := range []struct {
		base, wantBase string
		count          int
	}{
		{"", initial, 3}, {"main", "main", 3}, {"HEAD^", "HEAD^", 1}, {"HEAD~2", "HEAD~2", 2},
	} {
		for _, jsonOutput := range []bool{true, false} {
			args := []string{"--reviewer=claude"}
			if tc.base != "" {
				args = append(args, "--base", tc.base)
			}
			if jsonOutput {
				args = append(args, "--json")
			}
			var out, errs bytes.Buffer
			if code := run(args, &out, &errs); code != 0 {
				t.Fatalf("exit %d, stdout %s, stderr %s", code, &out, &errs)
			}
			wantHeader := fmt.Sprintf("reviewing %d files, +%d/-0, base %s..HEAD\n", tc.count, tc.count, tc.wantBase)
			if errs.String() != wantHeader {
				t.Fatalf("header %q, want %q", errs.String(), wantHeader)
			}
			if jsonOutput {
				var result map[string]any
				if err := json.Unmarshal(out.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if result["base"] != tc.wantBase || result["files_reviewed"] != float64(tc.count) {
					t.Fatalf("metadata: %s", &out)
				}
			} else if !strings.Contains(out.String(), "0 findings across 0 files (overall confidence 1.00)") {
				t.Fatalf("human output: %s", &out)
			}
			prompt, err := os.ReadFile(log)
			if err != nil || strings.Count(string(prompt), "diff --git") != tc.count {
				t.Fatalf("prompt %s, %v", prompt, err)
			}
		}
	}
	// A resolving but unrelated mainline must never trigger HEAD^ fallback.
	unrelated := git("commit-tree", "HEAD^{tree}", "-m", "unrelated history")
	git("update-ref", "refs/remotes/origin/main", unrelated)
	if got := reviewBase(root); got != "" {
		t.Fatalf("unrelated mainline silently fell back to %s", got)
	}
	var out, errs bytes.Buffer
	if code := run([]string{"--json", "--reviewer=claude"}, &out, &errs); code != 1 || out.Len() != 0 || !strings.Contains(errs.String(), "could not find a merge-base") {
		t.Fatalf("unrelated mainline: exit %d, stdout %s, stderr %s", code, &out, &errs)
	}
	out.Reset()
	errs.Reset()
	if code := run([]string{"--json", "--reviewer=claude", "--base=HEAD^"}, &out, &errs); code != 0 || !strings.Contains(out.String(), `"base":"HEAD^"`) {
		t.Fatalf("explicit base did not bypass resolution: exit %d, stdout %s, stderr %s", code, &out, &errs)
	}
}

func TestRunDiffPreflight(t *testing.T) {
	for _, name := range []string{"", "README.md", "nested/ReAdMe", "LICENSE", "notes.RST", "notes.TXT", "Makefile", "Dockerfile", "docs/setup.go", "config.yaml"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			git := func(args ...string) {
				t.Helper()
				if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
					t.Fatalf("git: %s %v", out, err)
				}
			}
			git("init", "-b", "feature")
			git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "initial")
			if name != "" {
				path := filepath.Join(root, name)
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("fixture\n"), 0600); err != nil {
					t.Fatal(err)
				}
				git("add", name)
			}
			bin := t.TempDir()
			log := filepath.Join(bin, "calls")
			if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\nprintf called >> \"$ESUNA_TEST_INVOCATIONS\"\nexit 9\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
			t.Setenv("ESUNA_TEST_INVOCATIONS", log)
			t.Setenv("TYPESAFE_API_KEY", "test-only")
			t.Chdir(root)
			old := http.DefaultTransport
			http.DefaultTransport = roundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("unexpected Jev call"); return nil, nil })
			t.Cleanup(func() { http.DefaultTransport = old })
			for _, gate := range []bool{false, true} {
				args := []string{"--json", "--reviewer=claude"}
				if gate {
					args = append(args, "--gate")
				}
				var out, errs bytes.Buffer
				code := run(args, &out, &errs)
				if code != 1 || out.Len() != 0 {
					t.Fatalf("exit %d, stdout %s, stderr %s", code, &out, &errs)
				}
				codeFile := name == "Makefile" || name == "Dockerfile" || name == "docs/setup.go" || name == "config.yaml"
				want := "only documentation files changed between HEAD and the working tree (" + name + "); nothing to review"
				if name == "" {
					want = "no changes found between HEAD and the working tree; nothing to review"
				}
				if codeFile {
					want = "claude reviewer"
				}
				if !strings.Contains(errs.String(), want) {
					t.Fatalf("stderr %s, want %s", &errs, want)
				}
				_, err := os.Stat(log)
				if !codeFile && !os.IsNotExist(err) {
					t.Fatalf("reviewer invoked: %v", err)
				}
			}
		})
	}
}

func TestDiffStat(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git: %s %v", out, err)
		}
	}
	git("init", "-b", "feature")
	path := filepath.Join(root, "code.go")
	if err := os.WriteFile(path, []byte("one\ntwo\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "code.go")
	git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "commit", "-m", "initial")
	for _, tc := range []struct {
		content  string
		ins, del int
	}{
		{"one\ntwo\n", 0, 0}, {"one\n", 0, 1}, {"", 0, 2}, {"one\ntwo\nthree\n", 1, 0}, {"one\nthree\nfour\n", 2, 1},
	} {
		if err := os.WriteFile(path, []byte(tc.content), 0600); err != nil {
			t.Fatal(err)
		}
		ins, del, err := diffStat(root, "HEAD")
		if err != nil || ins != tc.ins || del != tc.del {
			t.Fatalf("%q: +%d/-%d, %v; want +%d/-%d", tc.content, ins, del, err, tc.ins, tc.del)
		}
	}
}

func TestDocumentationAllowlist(t *testing.T) {
	docs := []string{"README", "README.md", "README.txt", "LICENSE", "LICENSE.md", "CHANGELOG", "CHANGELOG.md", "CONTRIBUTING", "CONTRIBUTING.md", "NOTICE", "NOTICE.md", "guide.rst", "guide.txt", "guide.md"}
	for _, name := range docs {
		if err := checkReviewableDiff("main", []string{filepath.Join("nested", strings.ToUpper(name))}); err == nil {
			t.Fatalf("not classified as doc: %s", name)
		}
	}
	for _, name := range []string{"Makefile", "Dockerfile", "docs/setup.go", "README.go", "LICENSE.txt.go", "config.yaml", "docs/settings.json", "docs/manual.pdf"} {
		if err := checkReviewableDiff("main", append(append([]string{}, docs...), name)); err != nil {
			t.Fatalf("code classified as doc: %s: %v", name, err)
		}
	}
}

func TestRunGate(t *testing.T) {
	for _, tc := range []struct {
		severity              string
		confidence, threshold float64
		gate                  bool
		want                  int
	}{
		{"P0", 0.7, 0.7, true, 1}, {"P0", 0.7, 0.8, true, 0}, {"P0", 0.8, 0.7, false, 0}, {"P1", 1, 0.7, true, 0}, {"P2", 1, 0.7, true, 0},
	} {
		result := contract.Result{Findings: []contract.Finding{{Severity: tc.severity, Confidence: tc.confidence}}}
		if got := runGate(result, tc.threshold, tc.gate); got != tc.want {
			t.Fatalf("%+v: got %d", tc, got)
		}
	}
}

func TestRunHelpAndMissingKey(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "")
	var out, errs bytes.Buffer
	if code := run([]string{"--help"}, &out, &errs); code != 0 {
		t.Fatalf("help: %d %s", code, &errs)
	}
	if !strings.Contains(errs.String(), "-reviewer=<cli[,cli,...]>") || !strings.Contains(errs.String(), "override reviewer_pool for this run") {
		t.Fatalf("help missing reviewer flag: %s", &errs)
	}
	for _, text := range []string{"full Git branch range", "-base <ref>", "esuna --base <ref>"} {
		if !strings.Contains(errs.String(), text) {
			t.Fatalf("help missing %q: %s", text, &errs)
		}
	}
	errs.Reset()
	if code := run([]string{"--json"}, &out, &errs); code != 1 || !strings.Contains(errs.String(), "TYPESAFE_API_KEY") || out.Len() != 0 {
		t.Fatalf("missing key: %d %s", code, &errs)
	}
}

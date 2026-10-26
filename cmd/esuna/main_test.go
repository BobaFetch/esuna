package main

import (
	"bytes"
	"encoding/json"
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
	git("init")
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
			if code != tc.code || stderr.Len() != 0 {
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

	// Scoring failure must not become a zero-confidence successful report.
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
	if code := run([]string{"--json", "--gate", "--author-model="}, &out, &errs); code != 0 || errs.Len() != 0 {
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
		if errs.String() != "esuna: warning: \"grok\" not found on PATH, skipping\n" || !strings.Contains(out.String(), `"findings":[]`) {
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
	git("init")
	git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "fixture")
	if err := os.WriteFile(filepath.Join(root, "change.txt"), []byte("real test change\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "change.txt")
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
				if !strings.Contains(stdout.String(), `"findings":[]`) || stderr.String() != wantStderr {
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
	git("init")
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
	errs.Reset()
	if code := run([]string{"--json"}, &out, &errs); code != 1 || !strings.Contains(errs.String(), "TYPESAFE_API_KEY") || out.Len() != 0 {
		t.Fatalf("missing key: %d %s", code, &errs)
	}
}

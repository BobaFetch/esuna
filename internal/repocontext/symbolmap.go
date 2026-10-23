package repocontext

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
)

// Symbol describes a lexical declaration reported by Universal Ctags.
type Symbol struct {
	Name string
	Kind string
	Line int
}

// SymbolMap returns deduplicated symbols keyed by repo-relative file path.
// Universal Ctags with JSON support is required; failures are not suppressed.
func SymbolMap(repoRoot string) (map[string][]Symbol, error) {
	binary, err := exec.LookPath("ctags")
	if err != nil {
		return nil, fmt.Errorf("universal-ctags not found on PATH: %w", err)
	}
	version, err := exec.Command(binary, "--version").CombinedOutput()
	if err != nil || !bytes.Contains(version, []byte("Universal Ctags")) {
		return nil, fmt.Errorf("ctags must be Universal Ctags (not legacy ctags); install universal-ctags: %s", strings.TrimSpace(string(version)))
	}
	cmd := exec.Command(binary, "--options=NONE", "-R", "--output-format=json", "--fields=+nK", "--sort=yes", "-f", "-", ".")
	cmd.Dir = repoRoot
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("universal-ctags: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	symbols := make(map[string][]Symbol)
	seen := make(map[string]map[Symbol]bool)
	decoder := json.NewDecoder(bytes.NewReader(out))
	for {
		var tag struct {
			Type string `json:"_type"`
			Path string `json:"path"`
			Name string `json:"name"`
			Kind string `json:"kind"`
			Line int    `json:"line"`
		}
		err := decoder.Decode(&tag)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse universal-ctags JSON: %w", err)
		}
		if tag.Type != "tag" {
			continue
		}
		path := filepath.Clean(tag.Path)
		if !filepath.IsLocal(path) {
			return nil, fmt.Errorf("universal-ctags returned non-repo-relative path %q", tag.Path)
		}
		symbol := Symbol{Name: tag.Name, Kind: tag.Kind, Line: tag.Line}
		if seen[path] == nil {
			seen[path] = make(map[Symbol]bool)
		}
		if !seen[path][symbol] {
			symbols[path] = append(symbols[path], symbol)
			seen[path][symbol] = true
		}
	}
	return symbols, nil
}

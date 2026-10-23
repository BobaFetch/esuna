// Package config loads optional per-repository review settings.
package config

import (
	"bufio"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"esuna/internal/contract"
	"esuna/internal/reviewer"
)

type Config struct {
	ReviewerPool  []reviewer.Model
	GateThreshold float64
	AuthorModel   string
}

// Load reads .esuna.conf. Blank lines and full-line # comments are ignored.
// Unknown/duplicate keys and invalid values are errors; missing files use defaults.
func Load(repoRoot string) (*Config, error) {
	cfg := &Config{ReviewerPool: append([]reviewer.Model(nil), reviewer.AllModels...), GateThreshold: contract.DefaultGateThreshold}
	file, err := os.Open(filepath.Join(repoRoot, ".esuna.conf"))
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read .esuna.conf: %w", err)
	}
	defer file.Close()
	seen := make(map[string]bool)
	scanner := bufio.NewScanner(file)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		key, value, ok := strings.Cut(text, "=")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		invalid := func(reason string) (*Config, error) { return nil, fmt.Errorf(".esuna.conf line %d: %s", line, reason) }
		if !ok {
			return invalid("expected key=value")
		}
		if seen[key] {
			return invalid("duplicate key " + key)
		}
		seen[key] = true
		switch key {
		case "reviewer_pool":
			cfg.ReviewerPool = nil
			models := make(map[reviewer.Model]bool)
			for _, name := range strings.Split(value, ",") {
				model := reviewer.Model(strings.TrimSpace(name))
				if !validModel(model) || models[model] {
					return invalid("reviewer_pool must contain distinct models: claude, codex, muse, grok")
				}
				models[model] = true
				cfg.ReviewerPool = append(cfg.ReviewerPool, model)
			}
		case "gate_threshold":
			threshold, err := strconv.ParseFloat(value, 64)
			if err != nil || math.IsNaN(threshold) || math.IsInf(threshold, 0) || threshold < 0 || threshold > 1 {
				return invalid("gate_threshold must be a finite number in [0,1]")
			}
			cfg.GateThreshold = threshold
		case "author_model":
			if value != "" && !validModel(reviewer.Model(value)) {
				return invalid("author_model must be claude, codex, muse, or grok")
			}
			cfg.AuthorModel = value
		default:
			return invalid("unknown key " + key)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read .esuna.conf: %w", err)
	}
	return cfg, nil
}

func validModel(model reviewer.Model) bool {
	switch model {
	case reviewer.Claude, reviewer.Codex, reviewer.Muse, reviewer.Grok:
		return true
	}
	return false
}

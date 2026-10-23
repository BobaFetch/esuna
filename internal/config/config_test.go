package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"esuna/internal/contract"
	"esuna/internal/reviewer"
)

func TestLoad(t *testing.T) {
	root := t.TempDir()
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.ReviewerPool, reviewer.AllModels) || cfg.GateThreshold != contract.DefaultGateThreshold || cfg.AuthorModel != "" {
		t.Fatalf("defaults: %+v", cfg)
	}
	data := "# local settings\n reviewer_pool = codex,claude\ngate_threshold=0.8\nauthor_model=claude\n"
	if err := os.WriteFile(filepath.Join(root, ".esuna.conf"), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.ReviewerPool, []reviewer.Model{reviewer.Codex, reviewer.Claude}) || cfg.GateThreshold != 0.8 || cfg.AuthorModel != "claude" {
		t.Fatalf("loaded: %+v", cfg)
	}
}

func TestLoadInvalid(t *testing.T) {
	for _, data := range []string{"bad", "unknown=value", "reviewer_pool=", "reviewer_pool=unknown", "reviewer_pool=claude,claude", "author_model=unknown", "gate_threshold=NaN", "gate_threshold=Inf", "gate_threshold=-0.1", "gate_threshold=1.1", "gate_threshold=bad", "gate_threshold=0.7\ngate_threshold=0.8"} {
		t.Run(data, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, ".esuna.conf"), []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(root); err == nil {
				t.Fatalf("accepted %q", data)
			}
		})
	}
}

func TestLoadGuidelines(t *testing.T) {
	root := t.TempDir()
	got, err := LoadGuidelines(root)
	if err != nil || got != "" {
		t.Fatalf("missing: %q %v", got, err)
	}
	if err := os.Mkdir(filepath.Join(root, ".esuna"), 0700); err != nil {
		t.Fatal(err)
	}
	want := "# Review guidance\n\nPreserve this text verbatim.\n"
	if err := os.WriteFile(filepath.Join(root, ".esuna/guidelines.md"), []byte(want), 0600); err != nil {
		t.Fatal(err)
	}
	got, err = LoadGuidelines(root)
	if err != nil || got != want {
		t.Fatalf("guidelines: %q %v", got, err)
	}
}

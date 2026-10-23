package hook

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstall(t *testing.T) {
	for _, gate := range []bool{false, true} {
		t.Run(map[bool]string{false: "advisory", true: "gate"}[gate], func(t *testing.T) {
			root := t.TempDir()
			if out, err := exec.Command("git", "init", root).CombinedOutput(); err != nil {
				t.Fatalf("git init: %s %v", out, err)
			}
			if err := Install(root, gate); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, ".git/hooks/pre-push")
			info, err := os.Stat(path)
			if err != nil || info.Mode()&0111 == 0 {
				t.Fatalf("not executable: %v %v", info, err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), "esuna") || strings.Contains(string(data), "--gate") != gate {
				t.Fatalf("script: %s", data)
			}
			if err := Install(root, gate); err == nil || !strings.Contains(err.Error(), "merge manually") {
				t.Fatalf("collision: %v", err)
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != string(data) {
				t.Fatal("existing hook changed")
			}
			// A failing esuna must block only a gating hook; no real reviewers run here.
			bin := t.TempDir()
			if err := os.WriteFile(filepath.Join(bin, "esuna"), []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(path)
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
			err = cmd.Run()
			if (err != nil) != gate {
				t.Fatalf("gate=%v execution: %v", gate, err)
			}
		})
	}
}

func TestInstallCustomHooksPath(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "init", root).CombinedOutput(); err != nil {
		t.Fatalf("init: %s %v", out, err)
	}
	if err := exec.Command("git", "-C", root, "config", "core.hooksPath", "custom-hooks").Run(); err != nil {
		t.Fatal(err)
	}
	if err := Install(root, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "custom-hooks/pre-push")); err != nil {
		t.Fatal(err)
	}
}

func TestInstallOutsideRepo(t *testing.T) {
	if err := Install(t.TempDir(), false); err == nil {
		t.Fatal("accepted non-repository")
	}
}

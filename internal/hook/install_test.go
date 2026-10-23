package hook

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
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

// The subprocess's file-size limit forces a real write failure without changing
// process-global limits in the test runner or requiring a full filesystem.
func TestInstallWriteFailureLeavesNoHook(t *testing.T) {
	if root := os.Getenv("ESUNA_HOOK_WRITE_FAILURE_ROOT"); root != "" {
		var limit syscall.Rlimit
		if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &limit); err != nil {
			t.Fatal(err)
		}
		installErr := Install(root, true)
		// Restore the soft limit so Go can write coverage data on process exit.
		limit.Cur = limit.Max
		if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &limit); err != nil {
			t.Fatal(err)
		}
		if installErr == nil || !strings.Contains(installErr.Error(), "write pre-push hook") {
			t.Fatalf("expected write failure, got %v", installErr)
		}
		if _, err := os.Lstat(filepath.Join(root, ".git/hooks/pre-push")); !os.IsNotExist(err) {
			t.Fatalf("failed installation left a hook behind: %v", err)
		}
		return
	}
	root := t.TempDir()
	if out, err := exec.Command("git", "init", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %s %v", out, err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", `ulimit -S -f 0; exec "$@"`, "sh", binary, "-test.run=^TestInstallWriteFailureLeavesNoHook$")
	cmd.Env = append(os.Environ(), "ESUNA_HOOK_WRITE_FAILURE_ROOT="+root)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("write-failure subprocess: %v\n%s", err, out)
	}
	entries, err := os.ReadDir(filepath.Join(root, ".git/hooks"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".pre-push-") {
			t.Fatalf("temporary hook left behind: %s", entry.Name())
		}
	}
	if err := Install(root, true); err != nil {
		t.Fatalf("retry after write failure: %v", err)
	}
}

func TestInstallOutsideRepo(t *testing.T) {
	if err := Install(t.TempDir(), false); err == nil {
		t.Fatal("accepted non-repository")
	}
}

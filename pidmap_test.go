package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPidMapDisambiguatesSharedHostByWorkspace(t *testing.T) {
	home := t.TempDir()
	pid, start := 303, "3682"
	workspaceA := filepath.Join(home, "repo-a")
	workspaceB := filepath.Join(home, "repo-b")
	recordPidMapFor(home, "sid:session-a", pid, start, workspaceA)
	recordPidMapFor(home, "sid:session-b", pid, start, workspaceB)

	if got := lookupPidMapFor(home, pid, start, workspaceA); got != "sid:session-a" {
		t.Fatalf("workspace A resolved %q", got)
	}
	if got := lookupPidMapFor(home, pid, start, workspaceB); got != "sid:session-b" {
		t.Fatalf("workspace B resolved %q", got)
	}
	if got := lookupPidMapFor(home, pid, start, filepath.Join(workspaceA, "internal", "bus")); got != "sid:session-a" {
		t.Fatalf("nested workspace resolved %q", got)
	}
	if got := lookupPidMapFor(home, pid, start, filepath.Join(home, "other")); got != "sid:session-b" {
		t.Fatalf("legacy fallback resolved %q, want the last legacy mapping", got)
	}
}

func TestPidMapLegacyCompatibility(t *testing.T) {
	home := t.TempDir()
	pid, start := 100, "10"
	if err := writeTestPidMap(pidMapPath(home, pid, start), "sid:legacy"); err != nil {
		t.Fatal(err)
	}
	if got := lookupPidMapFor(home, pid, start, t.TempDir()); got != "sid:legacy" {
		t.Fatalf("legacy mapping resolved %q", got)
	}
}

func writeTestPidMap(path, key string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(key+"\n"), 0o600)
}

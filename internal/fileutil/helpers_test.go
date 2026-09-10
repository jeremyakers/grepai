package fileutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReplaceFileAtomicallyReplacesExistingTarget(t *testing.T) {
	dir := t.TempDir()
	tempPath := filepath.Join(dir, "index.tmp")
	targetPath := filepath.Join(dir, "index.gob")
	if err := os.WriteFile(tempPath, []byte("new"), 0o600); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	if err := os.WriteFile(targetPath, []byte("old"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}

	if err := ReplaceFileAtomically(tempPath, targetPath); err != nil {
		t.Fatalf("ReplaceFileAtomically: %v", err)
	}
	got, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(got) != "new" {
		t.Fatalf("target content = %q, want new", got)
	}
}

func TestReplaceFileAtomicallyFailurePreservesExistingTarget(t *testing.T) {
	dir := t.TempDir()
	tempPath := filepath.Join(dir, "missing.tmp")
	targetPath := filepath.Join(dir, "index.gob")
	if err := os.WriteFile(targetPath, []byte("old"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}

	if err := ReplaceFileAtomically(tempPath, targetPath); err == nil {
		t.Fatal("ReplaceFileAtomically succeeded with missing temp")
	}
	got, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("target was lost after failed replacement: %v", err)
	}
	if string(got) != "old" {
		t.Fatalf("target content = %q, want old", got)
	}
}

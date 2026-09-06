package fileutil

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestProjectWriterLockContentionIsImmediateAndActionable(t *testing.T) {
	projectRoot := t.TempDir()
	first, err := AcquireProjectWriterLock(projectRoot)
	if err != nil {
		t.Fatalf("AcquireProjectWriterLock(first) error = %v", err)
	}
	defer first.Close()

	started := time.Now()
	second, err := AcquireProjectWriterLock(projectRoot)
	if second != nil {
		second.Close()
		t.Fatal("AcquireProjectWriterLock(second) unexpectedly acquired the lock")
	}
	if time.Since(started) > time.Second {
		t.Fatalf("contended acquisition took %s; want nonblocking failure", time.Since(started))
	}
	var activeErr *ProjectWriterActiveError
	if !errors.As(err, &activeErr) {
		t.Fatalf("error = %T %v, want *ProjectWriterActiveError", err, err)
	}
	if activeErr.ProjectRoot != first.ProjectRoot() {
		t.Fatalf("error project root = %q, want %q", activeErr.ProjectRoot, first.ProjectRoot())
	}
	if !strings.Contains(err.Error(), first.ProjectRoot()) || !strings.Contains(err.Error(), "another writer is active") {
		t.Fatalf("error = %q, want project path and actionable contention message", err)
	}
	if _, statErr := os.Stat(filepath.Join(first.ProjectRoot(), ".grepai", "writer.lock")); statErr != nil {
		t.Fatalf("writer lock file was not created: %v", statErr)
	}
}

func TestProjectWriterLocksAreIndependentAcrossProjects(t *testing.T) {
	first, err := AcquireProjectWriterLock(t.TempDir())
	if err != nil {
		t.Fatalf("AcquireProjectWriterLock(first project) error = %v", err)
	}
	defer first.Close()

	second, err := AcquireProjectWriterLock(t.TempDir())
	if err != nil {
		t.Fatalf("AcquireProjectWriterLock(second project) error = %v", err)
	}
	defer second.Close()
}

func TestProjectWriterLockReleasePermitsReacquisition(t *testing.T) {
	projectRoot := t.TempDir()
	first, err := AcquireProjectWriterLock(projectRoot)
	if err != nil {
		t.Fatalf("AcquireProjectWriterLock(first) error = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}

	second, err := AcquireProjectWriterLock(projectRoot)
	if err != nil {
		t.Fatalf("AcquireProjectWriterLock(after release) error = %v", err)
	}
	defer second.Close()
}

func TestProjectWriterLockCanonicalizesRelativeAndSymlinkPaths(t *testing.T) {
	projectRoot := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relativeRoot, err := filepath.Rel(cwd, projectRoot)
	if err != nil {
		t.Fatal(err)
	}

	first, err := AcquireProjectWriterLock(relativeRoot)
	if err != nil {
		t.Fatalf("AcquireProjectWriterLock(relative) error = %v", err)
	}
	defer first.Close()
	assertWriterContention(t, projectRoot)

	if runtime.GOOS != "windows" {
		symlinkRoot := filepath.Join(t.TempDir(), "project-link")
		if err := os.Symlink(projectRoot, symlinkRoot); err != nil {
			t.Logf("skipping symlink assertion: %v", err)
			return
		}
		assertWriterContention(t, symlinkRoot)
	}
}

func assertWriterContention(t *testing.T, projectRoot string) {
	t.Helper()
	lock, err := AcquireProjectWriterLock(projectRoot)
	if lock != nil {
		lock.Close()
		t.Fatalf("AcquireProjectWriterLock(%q) unexpectedly succeeded", projectRoot)
	}
	var activeErr *ProjectWriterActiveError
	if !errors.As(err, &activeErr) {
		t.Fatalf("AcquireProjectWriterLock(%q) error = %T %v, want *ProjectWriterActiveError", projectRoot, err, err)
	}
}

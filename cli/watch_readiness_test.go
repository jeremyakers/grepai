package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yoanbernabeu/grepai/config"
	"github.com/yoanbernabeu/grepai/daemon"
	"github.com/yoanbernabeu/grepai/watcher"
)

func TestWaitForBackgroundReadyChecksChildExitFirst(t *testing.T) {
	logDir := t.TempDir()
	const childPID = 4242
	readyPath := daemon.GetWorkspaceReadyFile(logDir, "ws")
	if err := os.WriteFile(readyPath, []byte("ready\n4242\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	exitCh := make(chan struct{})
	close(exitCh)

	err := waitForBackgroundReady(exitCh, childPID, func(pid int) bool {
		return daemon.IsWorkspaceReadyForPID(logDir, "ws", pid)
	}, time.Minute, time.Minute)
	if err == nil {
		t.Fatal("stale ready marker won over exited child")
	}
}

func TestWaitForBackgroundReadyAcceptsMatchingLiveChildMarker(t *testing.T) {
	exitCh := make(chan struct{})
	if err := waitForBackgroundReady(exitCh, 42, func(pid int) bool { return pid == 42 }, time.Minute, time.Minute); err != nil {
		t.Fatalf("waitForBackgroundReady() error = %v", err)
	}
}

func TestRegistrationStartupClearsStaleReadyMarkers(t *testing.T) {
	logDir := t.TempDir()
	if err := daemon.WriteReadyFile(logDir); err != nil {
		t.Fatal(err)
	}
	if err := removeProjectReadyMarker(logDir, ""); err != nil {
		t.Fatal(err)
	}
	if daemon.IsReady(logDir) {
		t.Fatal("project ready marker survived startup preparation")
	}

	if err := daemon.WriteWorktreeReadyFile(logDir, "wt"); err != nil {
		t.Fatal(err)
	}
	if err := removeProjectReadyMarker(logDir, "wt"); err != nil {
		t.Fatal(err)
	}
	if daemon.IsWorktreeReady(logDir, "wt") {
		t.Fatal("worktree ready marker survived startup preparation")
	}
}

func TestStartBackgroundWatchRejectsStaleReadyWhenChildFails(t *testing.T) {
	logDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(logDir, "grepai-watch.pid"), []byte("-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logDir, "grepai-watch.ready"), []byte("ready\n4242\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	original := watchSpawnBackground
	t.Cleanup(func() { watchSpawnBackground = original })
	watchSpawnBackground = func(dir string, _ []string) (int, <-chan struct{}, error) {
		if daemon.IsReady(dir) {
			t.Fatal("stale ready marker was not removed before spawn")
		}
		exited := make(chan struct{})
		close(exited) // Simulates registration ENOSPC terminating the child.
		return 4242, exited, nil
	}

	if err := startBackgroundWatch(logDir, ""); err == nil {
		t.Fatal("startBackgroundWatch() accepted stale readiness after child failure")
	}
}

func TestStartBackgroundWorkspaceRejectsStaleReadyWhenChildFails(t *testing.T) {
	logDir := t.TempDir()
	ws := &config.Workspace{Name: "ws"}
	if err := os.WriteFile(daemon.GetWorkspacePIDFile(logDir, ws.Name), []byte("-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(daemon.GetWorkspaceReadyFile(logDir, ws.Name), []byte("ready\n4242\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	original := watchSpawnWorkspace
	t.Cleanup(func() { watchSpawnWorkspace = original })
	watchSpawnWorkspace = func(dir, name string, _ []string) (int, <-chan struct{}, error) {
		if daemon.IsWorkspaceReady(dir, name) {
			t.Fatal("stale workspace ready marker was not removed before spawn")
		}
		exited := make(chan struct{})
		close(exited)
		return 4242, exited, nil
	}

	if err := startBackgroundWorkspaceWatch(logDir, ws); err == nil {
		t.Fatal("startBackgroundWorkspaceWatch() accepted stale readiness after child failure")
	}
}

func TestStartBackgroundWatchMatchingReadyPathUnaffected(t *testing.T) {
	logDir := t.TempDir()
	original := watchSpawnBackground
	t.Cleanup(func() { watchSpawnBackground = original })
	watchSpawnBackground = func(dir string, _ []string) (int, <-chan struct{}, error) {
		if err := daemon.WriteReadyFile(dir); err != nil {
			t.Fatal(err)
		}
		return os.Getpid(), make(chan struct{}), nil
	}
	if err := startBackgroundWatch(logDir, ""); err != nil {
		t.Fatalf("startBackgroundWatch() error = %v", err)
	}
}

func TestCleanupAndPersistFatalCleansBeforeBoundedPersistence(t *testing.T) {
	primary := &watcher.FatalError{Operation: "watch", Cause: errors.New("fatal")}
	cleaned := false
	persistStarted := false
	err := cleanupAndPersistFatal(primary, func() {
		cleaned = true
	}, func(ctx context.Context) error {
		persistStarted = true
		if !cleaned {
			t.Fatal("persistence started before watcher/ready cleanup")
		}
		<-ctx.Done()
		return ctx.Err()
	}, 0)
	if !persistStarted {
		t.Fatal("persistence was not attempted")
	}
	if !errors.Is(err, primary) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cleanupAndPersistFatal() error = %v, want primary and deadline errors", err)
	}
}

func TestCleanupAndPersistFatalJoinsPersistenceFailure(t *testing.T) {
	primary := errors.New("watch failed")
	persistErr := errors.New("persist failed")
	err := cleanupAndPersistFatal(primary, func() {}, func(context.Context) error { return persistErr }, time.Second)
	if !errors.Is(err, primary) || !errors.Is(err, persistErr) {
		t.Fatalf("cleanupAndPersistFatal() error = %v, want both causes", err)
	}
}

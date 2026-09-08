package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/yoanbernabeu/grepai/config"
	"github.com/yoanbernabeu/grepai/daemon"
)

func TestProjectChildFailureBeforeWatcherSetupRemovesReadyMarker(t *testing.T) {
	if os.Getenv("GREPAI_TEST_PROJECT_CHILD_FAILURE") == "1" {
		watchLogDir = os.Getenv("GREPAI_TEST_LOG_DIR")
		if err := os.Chdir(os.Getenv("GREPAI_TEST_WORKING_DIR")); err != nil {
			t.Fatal(err)
		}
		if err := runWatchForeground(); err == nil {
			t.Fatal("runWatchForeground() succeeded without project configuration")
		}
		if daemon.IsReady(watchLogDir) {
			t.Fatal("project ready marker remains after pre-registration failure")
		}
		return
	}

	logDir := t.TempDir()
	workingDir := t.TempDir()
	if err := daemon.WriteReadyFile(logDir); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestProjectChildFailureBeforeWatcherSetupRemovesReadyMarker$")
	cmd.Env = append(os.Environ(),
		"GREPAI_BACKGROUND=1",
		"GREPAI_TEST_PROJECT_CHILD_FAILURE=1",
		"GREPAI_TEST_LOG_DIR="+logDir,
		"GREPAI_TEST_WORKING_DIR="+workingDir,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("project child helper failed: %v\n%s", err, output)
	}
	if daemon.IsReady(logDir) {
		t.Fatal("project ready marker remains after pre-registration failure")
	}
}

func TestWorkspaceChildFailureBeforeRuntimeSetupRemovesReadyMarker(t *testing.T) {
	if os.Getenv("GREPAI_TEST_WORKSPACE_CHILD_FAILURE") == "1" {
		logDir := os.Getenv("GREPAI_TEST_LOG_DIR")
		ws := &config.Workspace{
			Name: "ws",
			Projects: []config.ProjectEntry{{
				Name: "missing",
				Path: os.Getenv("GREPAI_TEST_MISSING_PROJECT"),
			}},
		}
		if err := runWorkspaceWatchForeground(logDir, ws); err == nil {
			t.Fatal("runWorkspaceWatchForeground() succeeded with missing project")
		}
		if daemon.IsWorkspaceReady(logDir, ws.Name) {
			t.Fatal("workspace ready marker remains after pre-runtime failure")
		}
		return
	}

	logDir := t.TempDir()
	missingProject := filepath.Join(t.TempDir(), "missing")
	if err := daemon.WriteWorkspaceReadyFile(logDir, "ws"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWorkspaceChildFailureBeforeRuntimeSetupRemovesReadyMarker$")
	cmd.Env = append(os.Environ(),
		"GREPAI_BACKGROUND=1",
		"GREPAI_TEST_WORKSPACE_CHILD_FAILURE=1",
		"GREPAI_TEST_LOG_DIR="+logDir,
		"GREPAI_TEST_MISSING_PROJECT="+missingProject,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("workspace child helper failed: %v\n%s", err, output)
	}
	if daemon.IsWorkspaceReady(logDir, "ws") {
		t.Fatal("workspace ready marker remains after pre-runtime failure")
	}
}

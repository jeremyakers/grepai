package cli

import (
	"log"
	"os"
	"path/filepath"
	"testing"

	"github.com/yoanbernabeu/grepai/config"
	"github.com/yoanbernabeu/grepai/daemon"
)

func TestProjectChildFailureBeforeWatcherSetupRemovesReadyMarker(t *testing.T) {
	logDir := t.TempDir()
	workingDir := t.TempDir()
	if err := daemon.WriteReadyFile(logDir); err != nil {
		t.Fatal(err)
	}
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(workingDir); err != nil {
		t.Fatal(err)
	}
	originalLogDir := watchLogDir
	originalLogFlags, originalLogPrefix := log.Flags(), log.Prefix()
	watchLogDir = logDir
	t.Setenv("GREPAI_BACKGROUND", "1")
	t.Cleanup(func() {
		watchLogDir = originalLogDir
		log.SetFlags(originalLogFlags)
		log.SetPrefix(originalLogPrefix)
		_ = os.Chdir(originalDir)
	})

	if err := runWatchForeground(); err == nil {
		t.Fatal("runWatchForeground() succeeded without project configuration")
	}
	if daemon.IsReady(logDir) {
		t.Fatal("project ready marker remains after pre-registration failure")
	}
}

func TestWorkspaceChildFailureBeforeRuntimeSetupRemovesReadyMarker(t *testing.T) {
	logDir := t.TempDir()
	ws := &config.Workspace{
		Name: "ws",
		Projects: []config.ProjectEntry{{
			Name: "missing",
			Path: filepath.Join(t.TempDir(), "missing"),
		}},
	}
	if err := daemon.WriteWorkspaceReadyFile(logDir, ws.Name); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GREPAI_BACKGROUND", "1")
	originalLogFlags, originalLogPrefix := log.Flags(), log.Prefix()
	t.Cleanup(func() {
		log.SetFlags(originalLogFlags)
		log.SetPrefix(originalLogPrefix)
	})

	if err := runWorkspaceWatchForeground(logDir, ws); err == nil {
		t.Fatal("runWorkspaceWatchForeground() succeeded with missing project")
	}
	if daemon.IsWorkspaceReady(logDir, ws.Name) {
		t.Fatal("workspace ready marker remains after pre-runtime failure")
	}
}

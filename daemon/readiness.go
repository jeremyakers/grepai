package daemon

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func readyFileMatchesPID(path string, expectedPID int) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	fields := strings.Fields(string(data))
	if len(fields) != 2 || fields[0] != "ready" {
		return false
	}
	pid, err := strconv.Atoi(fields[1])
	return err == nil && pid == expectedPID
}

// IsReadyForPID reports whether the project marker belongs to expectedPID.
func IsReadyForPID(logDir string, expectedPID int) bool {
	return readyFileMatchesPID(filepath.Join(logDir, readyFileName), expectedPID)
}

// IsWorktreeReadyForPID reports whether the worktree marker belongs to expectedPID.
func IsWorktreeReadyForPID(logDir, worktreeID string, expectedPID int) bool {
	return readyFileMatchesPID(GetWorktreeReadyFile(logDir, worktreeID), expectedPID)
}

// IsWorkspaceReadyForPID reports whether the workspace marker belongs to expectedPID.
func IsWorkspaceReadyForPID(logDir, workspaceName string, expectedPID int) bool {
	return readyFileMatchesPID(GetWorkspaceReadyFile(logDir, workspaceName), expectedPID)
}

package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/yoanbernabeu/grepai/daemon"
)

func removeProjectReadyMarker(logDir, worktreeID string) error {
	if worktreeID != "" {
		return daemon.RemoveWorktreeReadyFile(logDir, worktreeID)
	}
	return daemon.RemoveReadyFile(logDir)
}

const fatalPersistTimeout = 30 * time.Second

var errNotReady = errors.New("not ready")

func waitForBackgroundReady(exitCh <-chan struct{}, expectedPID int, isReady func(int) bool, timeout, pollInterval time.Duration) error {
	check := func() error {
		select {
		case <-exitCh:
			return errors.New("background process exited before becoming ready")
		default:
		}
		if isReady(expectedPID) {
			return nil
		}
		return errNotReady
	}
	if err := check(); !errors.Is(err, errNotReady) {
		return err
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-exitCh:
			return errors.New("background process exited before becoming ready")
		case <-timer.C:
			return fmt.Errorf("timeout waiting for background process readiness after %v", timeout)
		case <-ticker.C:
			if err := check(); !errors.Is(err, errNotReady) {
				return err
			}
		}
	}
}

func cleanupAndPersistFatal(primary error, cleanup func(), persist func(context.Context) error, timeout time.Duration) error {
	cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := persist(ctx); err != nil {
		return errors.Join(primary, fmt.Errorf("persist after watcher failure: %w", err))
	}
	return primary
}

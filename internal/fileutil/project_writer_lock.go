package fileutil

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const projectWriterLockName = "writer.lock"

// ProjectWriterActiveError reports that a project already has a live writer.
type ProjectWriterActiveError struct {
	ProjectRoot string
	LockPath    string
	Err         error
}

func (e *ProjectWriterActiveError) Error() string {
	return fmt.Sprintf("cannot watch project %s: another writer is active (lock: %s)", e.ProjectRoot, e.LockPath)
}

func (e *ProjectWriterActiveError) Unwrap() error { return e.Err }

// ProjectWriterLock is an exclusive, process-lifetime lock for one canonical
// project root. Close releases it; the operating system also releases it after
// a crash. The lock file's contents are never used to infer process liveness.
type ProjectWriterLock struct {
	projectRoot string
	lockPath    string
	file        *os.File
	mu          sync.Mutex
}

// AcquireProjectWriterLock acquires the project lock without blocking.
//
// Lock ordering: callers must acquire this lifetime lock before loading or
// mutating stores. Store Load/Persist locks may then be nested transiently.
func AcquireProjectWriterLock(projectRoot string) (*ProjectWriterLock, error) {
	canonicalRoot, err := canonicalProjectRoot(projectRoot)
	if err != nil {
		return nil, err
	}
	lockPath := filepath.Join(canonicalRoot, ".grepai", projectWriterLockName)
	if err := EnsureParentDir(lockPath); err != nil {
		return nil, fmt.Errorf("failed to prepare writer lock for project %s: %w", canonicalRoot, err)
	}

	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open writer lock for project %s: %w", canonicalRoot, err)
	}
	if err := FlockExclusive(file, true); err != nil {
		_ = file.Close()
		if isFlockContention(err) {
			return nil, &ProjectWriterActiveError{
				ProjectRoot: canonicalRoot,
				LockPath:    lockPath,
				Err:         err,
			}
		}
		return nil, fmt.Errorf("failed to acquire writer lock for project %s: %w", canonicalRoot, err)
	}

	return &ProjectWriterLock{
		projectRoot: canonicalRoot,
		lockPath:    lockPath,
		file:        file,
	}, nil
}

func canonicalProjectRoot(projectRoot string) (string, error) {
	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return "", fmt.Errorf("failed to make project path absolute: %w", err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", fmt.Errorf("failed to canonicalize project path %s: %w", absRoot, err)
	}
	return filepath.Clean(resolvedRoot), nil
}

// ProjectRoot returns the canonical root represented by the lock.
func (l *ProjectWriterLock) ProjectRoot() string { return l.projectRoot }

// Close releases and closes the lock. It is safe to call more than once.
func (l *ProjectWriterLock) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	file := l.file
	l.file = nil
	return errors.Join(Funlock(file), file.Close())
}

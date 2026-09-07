package watcher

import (
	"errors"
	"fmt"
)

var (
	errBackendClosed  = errors.New("fsnotify channel closed unexpectedly")
	errEventQueueFull = errors.New("file event queue is full")
)

// RegistrationError reports a failure to register filesystem coverage.
type RegistrationError struct {
	Operation string
	Path      string
	Cause     error
}

func (e *RegistrationError) Error() string {
	if hint := inotifyLimitHint(e.Cause); hint != "" {
		return fmt.Sprintf("%s %q: %s: %v", e.Operation, e.Path, hint, e.Cause)
	}
	return fmt.Sprintf("%s %q: %v", e.Operation, e.Path, e.Cause)
}

func (e *RegistrationError) Unwrap() error { return e.Cause }

// FatalError reports a watcher failure after registration completed.
type FatalError struct {
	Operation string
	Path      string
	Cause     error
}

func (e *FatalError) Error() string {
	if hint := inotifyLimitHint(e.Cause); hint != "" {
		return fmt.Sprintf("%s %q: %s: %v", e.Operation, e.Path, hint, e.Cause)
	}
	if e.Path == "" {
		return fmt.Sprintf("%s: %v", e.Operation, e.Cause)
	}
	return fmt.Sprintf("%s %q: %v", e.Operation, e.Path, e.Cause)
}

func (e *FatalError) Unwrap() error { return e.Cause }

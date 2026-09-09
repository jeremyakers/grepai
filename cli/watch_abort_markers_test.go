package cli

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestAbortBackgroundStartStopFailurePreservesMarkers(t *testing.T) {
	stopFailure := errors.New("stop denied")
	primary := errors.New("startup failed")
	exited := make(chan struct{})
	close(exited)
	cleanupCalled := false

	originalStop := watchStopProcess
	t.Cleanup(func() { watchStopProcess = originalStop })
	watchStopProcess = func(int) error { return stopFailure }

	err := abortBackgroundStart(primary, 42, exited, func() error {
		cleanupCalled = true
		return nil
	})
	if cleanupCalled {
		t.Fatal("marker cleanup ran after stop failure")
	}
	if !errors.Is(err, primary) || !errors.Is(err, stopFailure) {
		t.Fatalf("abortBackgroundStart() error = %v, want joined primary and stop errors", err)
	}
}

func TestAbortBackgroundStartExitTimeoutPreservesMarkers(t *testing.T) {
	primary := errors.New("startup failed")
	cleanupCalled := false
	originalStop := watchStopProcess
	originalTimeout := backgroundExitTimeout
	t.Cleanup(func() {
		watchStopProcess = originalStop
		backgroundExitTimeout = originalTimeout
	})
	watchStopProcess = func(int) error { return nil }
	backgroundExitTimeout = 10 * time.Millisecond

	err := abortBackgroundStart(primary, 42, make(chan struct{}), func() error {
		cleanupCalled = true
		return nil
	})
	if cleanupCalled {
		t.Fatal("marker cleanup ran before child exit")
	}
	if !errors.Is(err, primary) || !strings.Contains(err.Error(), "did not exit") {
		t.Fatalf("abortBackgroundStart() error = %v, want joined primary and timeout errors", err)
	}
}

func TestAbortBackgroundStartConfirmedExitRemovesMarkers(t *testing.T) {
	primary := errors.New("startup failed")
	exited := make(chan struct{})
	cleanupFailure := errors.New("cleanup failed")
	cleanupCalled := false
	originalStop := watchStopProcess
	t.Cleanup(func() { watchStopProcess = originalStop })
	watchStopProcess = func(int) error {
		close(exited)
		return nil
	}

	err := abortBackgroundStart(primary, 42, exited, func() error {
		cleanupCalled = true
		return cleanupFailure
	})
	if !cleanupCalled {
		t.Fatal("marker cleanup did not run after confirmed child exit")
	}
	if !errors.Is(err, primary) || !errors.Is(err, cleanupFailure) {
		t.Fatalf("abortBackgroundStart() error = %v, want joined primary and cleanup errors", err)
	}
}

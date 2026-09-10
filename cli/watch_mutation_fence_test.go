package cli

import (
	"context"
	"errors"
	"syscall"
	"testing"
	"time"

	"github.com/yoanbernabeu/grepai/watcher"
)

func awaitWatchTestSignal(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-signal:
	case <-timer.C:
		t.Fatalf("timed out waiting for %s", name)
	}
}

func awaitWatchTestValue[T any](t *testing.T, values <-chan T, name string) T {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case value := <-values:
		return value
	case <-timer.C:
		t.Fatalf("timed out waiting for %s", name)
		var zero T
		return zero
	}
}

func TestProjectMutationFenceRejectsQueuedEventAfterFatal(t *testing.T) {
	source := newFakeWatchSource()
	fence := newWatchMutationFence()
	if !fence.addWatcher(source) {
		t.Fatal("watcher was not admitted into a new mutation fence")
	}

	fatal := &watcher.FatalError{Operation: "enqueue file event", Cause: syscall.ENOSPC}
	source.readyErr = fatal
	mutated := false
	err := fence.handle(context.Background(), func(context.Context) {
		mutated = true
	})
	if !errors.Is(err, fatal) {
		t.Fatalf("handle() error = %v, want watcher fatal", err)
	}
	if mutated {
		t.Fatal("queued project event mutated state after watcher fatal")
	}
}

func TestFatalWithdrawalWaitsForCanceledMutation(t *testing.T) {
	fence := newWatchMutationFence()
	source := newFakeWatchSource()
	fence.addWatcher(source)

	started := make(chan struct{})
	canceled := make(chan struct{})
	release := make(chan struct{})
	handled := make(chan error, 1)
	go func() {
		handled <- fence.handle(context.Background(), func(ctx context.Context) {
			close(started)
			<-ctx.Done()
			close(canceled)
			<-release
		})
	}()
	<-started

	withdrawn := make(chan struct{})
	fatalDone := make(chan struct{})
	go func() {
		fence.fail(func() { close(withdrawn) })
		close(fatalDone)
	}()
	awaitWatchTestSignal(t, canceled, "mutation cancellation")
	duplicateDone := make(chan struct{})
	go func() {
		fence.fail(nil)
		close(duplicateDone)
	}()
	select {
	case <-withdrawn:
		t.Fatal("readiness was withdrawn before the active mutation quiesced")
	default:
	}
	select {
	case <-duplicateDone:
		t.Fatal("duplicate fatal call returned before first fatal completed")
	default:
	}
	close(release)
	if err := awaitWatchTestValue(t, handled, "mutation return"); err != nil {
		t.Fatalf("handle() error = %v", err)
	}
	awaitWatchTestSignal(t, fatalDone, "fatal completion")
	awaitWatchTestSignal(t, duplicateDone, "duplicate fatal completion")
	select {
	case <-withdrawn:
	default:
		t.Fatal("readiness was not withdrawn after the mutation quiesced")
	}
}

func TestZeroValueMutationFenceHasSafeFailureChannels(t *testing.T) {
	var fence watchMutationFence
	firstDone := make(chan struct{})
	go func() {
		fence.fail(nil)
		close(firstDone)
	}()
	awaitWatchTestSignal(t, firstDone, "zero-value fence failure")
	duplicateDone := make(chan struct{})
	go func() {
		fence.fail(nil)
		close(duplicateDone)
	}()
	awaitWatchTestSignal(t, duplicateDone, "zero-value duplicate failure")
	if err := fence.handle(context.Background(), func(context.Context) {
		t.Fatal("zero-value failed fence admitted a mutation")
	}); !errors.Is(err, errWatchMutationAdmissionClosed) {
		t.Fatalf("zero-value failed fence handle() error = %v, want admission closed", err)
	}
}

func TestWorkspaceMutationAdmissionChecksEveryWatcher(t *testing.T) {
	first := newFakeWatchSource()
	second := newFakeWatchSource()
	fatal := &watcher.FatalError{Operation: "watch linked project", Cause: syscall.ENOSPC}
	second.readyErr = fatal
	fence := newWatchMutationFence()
	fence.addWatcher(first)
	fence.addWatcher(second)

	mutated := false
	err := fence.handle(context.Background(), func(context.Context) { mutated = true })
	if !errors.Is(err, fatal) {
		t.Fatalf("handle() error = %v, want fatal from peer watcher", err)
	}
	if mutated {
		t.Fatal("workspace event was admitted while a peer watcher was fatal")
	}
}

func TestMutationFenceClosedReadinessIsStale(t *testing.T) {
	fence := newWatchMutationFence()
	fence.fail(nil)
	published := false
	if err := fence.publishReadiness(func() error {
		published = true
		return nil
	}); err != nil {
		t.Fatalf("publishReadiness() error = %v", err)
	}
	if published {
		t.Fatal("closed mutation fence published stale readiness")
	}
}

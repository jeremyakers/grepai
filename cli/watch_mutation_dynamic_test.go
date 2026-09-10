package cli

import (
	"context"
	"errors"
	"syscall"
	"testing"

	"github.com/yoanbernabeu/grepai/embedder"
	"github.com/yoanbernabeu/grepai/indexer"
	"github.com/yoanbernabeu/grepai/watcher"
)

func TestDynamicWatchSupervisorWaitsForEverySessionMutation(t *testing.T) {
	mainRoot := canonicalPath(t.TempDir())
	linkedRoot := canonicalPath(t.TempDir())
	fatal := &watcher.FatalError{Operation: "watch", Path: linkedRoot, Cause: syscall.ENOSPC}
	started := make(chan struct{}, 2)
	canceled := make(chan struct{}, 2)
	mainRelease := make(chan struct{})
	linkedRelease := make(chan struct{})
	triggerFatal := make(chan struct{})
	linkedMutationDone := make(chan error, 1)
	fence := newWatchMutationFence()
	runner := func(ctx context.Context, projectRoot string, _ embedder.Embedder, _ bool, onReady func(), _ watchSessionEventObserver, _ func(int, int, string), _ func(indexer.BatchProgressInfo), _ func(string, int, int), _ watchActivityObserver, _ watchStatsObserver) error {
		onReady()
		release := mainRelease
		if projectRoot == linkedRoot {
			release = linkedRelease
			go func() {
				linkedMutationDone <- fence.handle(ctx, func(eventCtx context.Context) {
					started <- struct{}{}
					<-eventCtx.Done()
					canceled <- struct{}{}
					<-release
				})
			}()
			<-triggerFatal
			return fatal
		}
		if err := fence.handle(ctx, func(eventCtx context.Context) {
			started <- struct{}{}
			<-eventCtx.Done()
			canceled <- struct{}{}
			<-release
		}); err != nil {
			return err
		}
		<-ctx.Done()
		return ctx.Err()
	}

	withdrawn := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		result <- runDynamicWatchSupervisor(
			context.Background(), mainRoot, nil,
			withWatchSupervisorInitialLinkedWorktrees([]string{linkedRoot}),
			withWatchSupervisorSessionRunner(runner),
			withWatchSupervisorMutationFence(fence),
			withWatchSupervisorFatalObserver(func() { close(withdrawn) }),
		)
	}()
	awaitWatchTestValue(t, started, "first dynamic mutation admission")
	awaitWatchTestValue(t, started, "second dynamic mutation admission")
	close(triggerFatal)
	awaitWatchTestValue(t, canceled, "first dynamic mutation cancellation")
	awaitWatchTestValue(t, canceled, "second dynamic mutation cancellation")
	close(linkedRelease)
	if err := awaitWatchTestValue(t, linkedMutationDone, "linked mutation return"); err != nil {
		t.Fatalf("linked handle() error = %v", err)
	}
	select {
	case <-withdrawn:
		t.Fatal("dynamic readiness was withdrawn with another session mutating")
	default:
	}
	close(mainRelease)
	awaitWatchTestSignal(t, withdrawn, "dynamic readiness withdrawal")
	if err := awaitWatchTestValue(t, result, "dynamic supervisor return"); !errors.Is(err, fatal) {
		t.Fatalf("runDynamicWatchSupervisor() error = %v, want fatal", err)
	}
}

func TestDynamicReadyQueuedDuringFatalDrainIsNotPublished(t *testing.T) {
	mainRoot := canonicalPath(t.TempDir())
	linkedRoot := canonicalPath(t.TempDir())
	fence := newWatchMutationFence()
	fatal := &watcher.FatalError{Operation: "watch", Path: linkedRoot, Cause: syscall.ENOSPC}
	mainMutationStarted := make(chan struct{})
	mainMutationCanceled := make(chan struct{})
	mainRelease := make(chan struct{})
	linkedMayFail := make(chan struct{})
	linkedRunning := make(chan struct{})
	mainRunning := make(chan struct{})
	withdrawn := make(chan struct{})
	published := make(chan struct{}, 1)

	runner := func(ctx context.Context, projectRoot string, _ embedder.Embedder, _ bool, onReady func(), _ watchSessionEventObserver, _ func(int, int, string), _ func(indexer.BatchProgressInfo), _ func(string, int, int), _ watchActivityObserver, _ watchStatsObserver) error {
		if projectRoot == linkedRoot {
			onReady()
			<-linkedMayFail
			return fatal
		}
		if err := fence.handle(ctx, func(eventCtx context.Context) {
			close(mainMutationStarted)
			<-eventCtx.Done()
			close(mainMutationCanceled)
			onReady()
			<-mainRelease
		}); err != nil {
			return err
		}
		<-ctx.Done()
		return ctx.Err()
	}

	result := make(chan error, 1)
	go func() {
		result <- runDynamicWatchSupervisor(
			context.Background(), mainRoot, nil,
			withWatchSupervisorInitialLinkedWorktrees([]string{linkedRoot}),
			withWatchSupervisorSessionRunner(runner),
			withWatchSupervisorMutationFence(fence),
			withWatchSupervisorInitialReadyObserver(func(int) error {
				published <- struct{}{}
				return nil
			}),
			withWatchSupervisorFatalObserver(func() { close(withdrawn) }),
			withWatchSupervisorLifecycleObserver(func(projectRoot, state, _ string) {
				if projectRoot == linkedRoot && state == "running" {
					close(linkedRunning)
				}
				if projectRoot == mainRoot && state == "running" {
					close(mainRunning)
				}
			}),
		)
	}()
	awaitWatchTestSignal(t, mainMutationStarted, "main mutation admission")
	awaitWatchTestSignal(t, linkedRunning, "linked session readiness")
	close(linkedMayFail)
	awaitWatchTestSignal(t, mainMutationCanceled, "main mutation cancellation")
	awaitWatchTestSignal(t, mainRunning, "final initial readiness processing")
	select {
	case <-published:
		t.Fatal("initial readiness published while fatal mutation was draining")
	default:
	}
	select {
	case <-withdrawn:
		t.Fatal("readiness withdrawn before fatal mutation drained")
	default:
	}
	close(mainRelease)
	awaitWatchTestSignal(t, withdrawn, "fatal readiness withdrawal")
	if err := awaitWatchTestValue(t, result, "dynamic fatal return"); !errors.Is(err, fatal) {
		t.Fatalf("runDynamicWatchSupervisor() error = %v, want fatal", err)
	}
	select {
	case <-published:
		t.Fatal("stale initial readiness was eventually published")
	default:
	}
}

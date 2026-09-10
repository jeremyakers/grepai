package cli

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/yoanbernabeu/grepai/config"
	"github.com/yoanbernabeu/grepai/trace"
	"github.com/yoanbernabeu/grepai/watcher"
)

type abortSignalingWatchSource struct {
	*fakeWatchSource
	abortStarted chan struct{}
	abortOnce    sync.Once
}

func (w *abortSignalingWatchSource) Abort() {
	w.abortOnce.Do(func() { close(w.abortStarted) })
	w.fakeWatchSource.Abort()
}

func runPersistenceFenceTestLoop(ctx context.Context, root string, st *nonCooperativePersistStore, source watchSource, fence *watchMutationFence, onFatal func()) error {
	return runProjectWatchLoopWithFence(
		ctx, st, trace.NewGOBSymbolStore(filepath.Join(root, "symbols.gob")), source,
		nil, nil, nil, nil, nil, nil, root, config.DefaultConfig(), nil, nil, nil, onFatal, fence,
	)
}

func TestGracefulPeerPersistenceIsFencedFromFatalWithdrawal(t *testing.T) {
	root := t.TempDir()
	fence := newWatchMutationFence()
	gracefulSource := newFakeWatchSource()
	fatalSource := &abortSignalingWatchSource{fakeWatchSource: newFakeWatchSource(), abortStarted: make(chan struct{})}
	if !fence.addWatcher(gracefulSource) || !fence.addWatcher(fatalSource) {
		t.Fatal("failed to register test watchers")
	}
	st := &nonCooperativePersistStore{called: make(chan struct{}), unblock: make(chan struct{})}
	gracefulCtx, cancelGraceful := context.WithCancel(context.Background())
	gracefulResult := make(chan error, 1)
	go func() {
		gracefulResult <- runPersistenceFenceTestLoop(gracefulCtx, root, st, gracefulSource, fence, nil)
	}()
	cancelGraceful()
	awaitWatchTestSignal(t, st.called, "graceful persistence start")

	fatal := &watcher.FatalError{Operation: "peer watch", Cause: syscall.ENOSPC}
	withdrawn := make(chan struct{})
	fatalResult := make(chan error, 1)
	go func() {
		fatalResult <- runPersistenceFenceTestLoop(context.Background(), root, &nonCooperativePersistStore{called: make(chan struct{}), unblock: make(chan struct{})}, fatalSource, fence, func() { close(withdrawn) })
	}()
	fatalSource.errors <- fatal
	awaitWatchTestSignal(t, fatalSource.abortStarted, "fatal fence closure")

	withdrewEarly := false
	select {
	case <-withdrawn:
		withdrewEarly = true
	case <-time.After(25 * time.Millisecond):
	}
	close(st.unblock)
	if err := awaitWatchTestValue(t, gracefulResult, "graceful peer return"); err != nil {
		t.Fatalf("graceful peer error = %v", err)
	}
	awaitWatchTestSignal(t, withdrawn, "fatal withdrawal after persistence")
	if err := awaitWatchTestValue(t, fatalResult, "fatal peer return"); !errors.Is(err, fatal) {
		t.Fatalf("fatal peer error = %v, want fatal", err)
	}
	if withdrewEarly {
		t.Fatal("fatal readiness withdrew while graceful peer persistence was still in flight")
	}
}

func TestFatalFenceWinsBeforeGracefulPeerPersistence(t *testing.T) {
	root := t.TempDir()
	fence := newWatchMutationFence()
	gracefulSource := newFakeWatchSource()
	fatalSource := &abortSignalingWatchSource{fakeWatchSource: newFakeWatchSource(), abortStarted: make(chan struct{})}
	if !fence.addWatcher(gracefulSource) || !fence.addWatcher(fatalSource) {
		t.Fatal("failed to register test watchers")
	}
	st := &persistCountingStore{}
	gracefulCtx, cancelGraceful := context.WithCancel(context.Background())
	gracefulResult := make(chan error, 1)
	go func() {
		gracefulResult <- runProjectWatchLoopWithFence(
			gracefulCtx, st, trace.NewGOBSymbolStore(filepath.Join(root, "symbols.gob")), gracefulSource,
			nil, nil, nil, nil, nil, nil, root, config.DefaultConfig(), nil, nil, nil, nil, fence,
		)
	}()

	fatal := &watcher.FatalError{Operation: "peer watch", Cause: syscall.ENOSPC}
	withdrawn := make(chan struct{})
	fatalSource.errors <- fatal
	fatalResult := make(chan error, 1)
	go func() {
		fatalResult <- runPersistenceFenceTestLoop(context.Background(), root, &nonCooperativePersistStore{called: make(chan struct{}), unblock: make(chan struct{})}, fatalSource, fence, func() { close(withdrawn) })
	}()
	awaitWatchTestSignal(t, withdrawn, "fatal withdrawal")
	cancelGraceful()
	if err := awaitWatchTestValue(t, gracefulResult, "graceful peer return"); err != nil {
		t.Fatalf("graceful peer error = %v", err)
	}
	if err := awaitWatchTestValue(t, fatalResult, "fatal peer return"); !errors.Is(err, fatal) {
		t.Fatalf("fatal peer error = %v, want fatal", err)
	}
	if st.persists != 0 {
		t.Fatalf("graceful peer persisted %d times after fatal won, want 0", st.persists)
	}
}

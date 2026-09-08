package cli

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/yoanbernabeu/grepai/config"
	"github.com/yoanbernabeu/grepai/indexer"
	"github.com/yoanbernabeu/grepai/trace"
	"github.com/yoanbernabeu/grepai/watcher"
)

type persistCountingStore struct {
	mockVectorStore
	persists  int
	onPersist func()
}

type cancellationBlockingMutationStore struct {
	mockVectorStore
	started  chan struct{}
	canceled chan struct{}
	release  chan struct{}
	persists atomic.Int32
}

func (s *cancellationBlockingMutationStore) DeleteByFile(ctx context.Context, _ string) error {
	close(s.started)
	<-ctx.Done()
	close(s.canceled)
	<-s.release
	return ctx.Err()
}

func (s *cancellationBlockingMutationStore) Persist(context.Context) error {
	s.persists.Add(1)
	return nil
}

func TestRunProjectWatchLoopFatalWaitsForInFlightMutationBeforeWithdrawal(t *testing.T) {
	root := t.TempDir()
	source := newFakeWatchSource()
	fence := newWatchMutationFence()
	if !fence.addWatcher(source) {
		t.Fatal("failed to register project watcher with mutation fence")
	}
	st := &cancellationBlockingMutationStore{
		started:  make(chan struct{}),
		canceled: make(chan struct{}),
		release:  make(chan struct{}),
	}
	idx := indexer.NewIndexer(root, st, nil, nil, nil, time.Time{})
	symbolStore := trace.NewGOBSymbolStore(filepath.Join(root, "symbols.gob"))
	withdrawn := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		result <- runProjectWatchLoopWithFence(
			context.Background(), st, symbolStore, source, idx, nil, nil, nil, nil, nil,
			root, config.DefaultConfig(), nil, nil, nil, func() { close(withdrawn) }, fence,
		)
	}()

	sendTimer := time.NewTimer(5 * time.Second)
	select {
	case source.events <- watcher.FileEvent{Type: watcher.EventDelete, Path: "obsolete.go"}:
	case <-sendTimer.C:
		t.Fatal("timed out delivering project event")
	}
	if !sendTimer.Stop() {
		select {
		case <-sendTimer.C:
		default:
		}
	}
	awaitWatchTestSignal(t, st.started, "project vector mutation")
	fatal := &watcher.FatalError{Operation: "process filesystem events", Path: root, Cause: syscall.ENOSPC}
	source.errors <- fatal
	awaitWatchTestSignal(t, st.canceled, "project mutation cancellation")
	select {
	case <-withdrawn:
		t.Fatal("project readiness withdrawn before mutation quiesced")
	default:
	}
	select {
	case err := <-result:
		t.Fatalf("project loop returned before mutation quiesced: %v", err)
	default:
	}
	if got := st.persists.Load(); got != 0 {
		t.Fatalf("fatal project path persisted %d times before mutation release, want 0", got)
	}

	close(st.release)
	err := awaitWatchTestValue(t, result, "project fatal return")
	if !errors.Is(err, fatal) {
		t.Fatalf("runProjectWatchLoopWithFence() error = %v, want fatal", err)
	}
	awaitWatchTestSignal(t, withdrawn, "project readiness withdrawal")
	if got := st.persists.Load(); got != 0 {
		t.Fatalf("fatal project path persisted %d times, want 0", got)
	}
}

func (s *persistCountingStore) Persist(context.Context) error {
	s.persists++
	if s.onPersist != nil {
		s.onPersist()
	}
	return nil
}

func TestRunProjectWatchLoopFatalWatcherErrorSkipsPersistenceAndAborts(t *testing.T) {
	root := t.TempDir()
	source := newFakeWatchSource()
	fatal := &watcher.FatalError{Operation: "process filesystem events", Path: root, Cause: syscall.ENOSPC}
	source.errors <- fatal
	readyWithdrawn := false
	vectorStore := &persistCountingStore{}
	symbolStore := trace.NewGOBSymbolStore(filepath.Join(root, "symbols.gob"))
	cfg := config.DefaultConfig()

	err := runProjectWatchLoop(context.Background(), vectorStore, symbolStore, source, nil, nil, nil, nil, nil, nil, root, cfg, nil, nil, nil, func() { readyWithdrawn = true })
	if !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("runProjectWatchLoop() error = %v, want ENOSPC", err)
	}
	if vectorStore.persists != 0 {
		t.Fatalf("vector store persisted %d times, want 0", vectorStore.persists)
	}
	if source.aborted != 1 || source.closed != 0 {
		t.Fatalf("watcher aborts/closes = %d/%d, want 1/0", source.aborted, source.closed)
	}
	if !readyWithdrawn {
		t.Fatal("ready marker was not withdrawn before fatal return")
	}
}

type nonCooperativePersistStore struct {
	mockVectorStore
	called  chan struct{}
	unblock chan struct{}
}

func (s *nonCooperativePersistStore) Persist(context.Context) error {
	close(s.called)
	<-s.unblock
	return nil
}

func TestRunProjectWatchLoopFatalDoesNotCallBlockingPersistence(t *testing.T) {
	root := t.TempDir()
	source := newFakeWatchSource()
	source.errors <- &watcher.FatalError{Operation: "watch", Cause: syscall.ENOSPC}
	st := &nonCooperativePersistStore{called: make(chan struct{}), unblock: make(chan struct{})}
	symbolStore := trace.NewGOBSymbolStore(filepath.Join(root, "symbols.gob"))
	result := make(chan error, 1)
	go func() {
		result <- runProjectWatchLoop(context.Background(), st, symbolStore, source, nil, nil, nil, nil, nil, nil, root, config.DefaultConfig(), nil, nil, nil, func() {})
	}()
	select {
	case err := <-result:
		if !errors.Is(err, syscall.ENOSPC) {
			t.Fatalf("runProjectWatchLoop() error = %v, want ENOSPC", err)
		}
	case <-st.called:
		close(st.unblock)
		t.Fatal("fatal watcher path invoked non-cooperative persistence")
	}
}

func TestRunProjectWatchLoopGracefulShutdownStillPersists(t *testing.T) {
	root := t.TempDir()
	source := newFakeWatchSource()
	st := &persistCountingStore{}
	symbolStore := trace.NewGOBSymbolStore(filepath.Join(root, "symbols.gob"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runProjectWatchLoop(ctx, st, symbolStore, source, nil, nil, nil, nil, nil, nil, root, config.DefaultConfig(), nil, nil, nil, nil); err != nil {
		t.Fatalf("runProjectWatchLoop() error = %v", err)
	}
	if st.persists != 1 {
		t.Fatalf("graceful shutdown persisted %d times, want 1", st.persists)
	}
}

func TestRunProjectWatchLoopQueuedEventCancellationUsesGracefulShutdown(t *testing.T) {
	root := t.TempDir()
	source := newFakeWatchSource()
	ctx, cancel := context.WithCancel(context.Background())
	source.readyFn = func(func() error) error {
		cancel()
		return context.Canceled
	}
	st := &persistCountingStore{}
	symbolStore := trace.NewGOBSymbolStore(filepath.Join(root, "symbols.gob"))
	fence := newWatchMutationFence()
	fence.addWatcher(source)
	withdrawn := make(chan struct{}, 1)
	result := make(chan error, 1)
	go func() {
		result <- runProjectWatchLoopWithFence(ctx, st, symbolStore, source, nil, nil, nil, nil, nil, nil, root, config.DefaultConfig(), nil, nil, nil, func() { withdrawn <- struct{}{} }, fence)
	}()

	sendTimer := time.NewTimer(5 * time.Second)
	defer sendTimer.Stop()
	select {
	case source.events <- watcher.FileEvent{Type: watcher.EventDelete, Path: "queued.go"}:
	case <-sendTimer.C:
		t.Fatal("timed out delivering queued project event")
	}
	if err := awaitWatchTestValue(t, result, "graceful project loop return"); err != nil {
		t.Fatalf("runProjectWatchLoopWithFence() error = %v, want nil", err)
	}
	if st.persists != 1 {
		t.Fatalf("graceful queued-event cancellation persisted %d times, want 1", st.persists)
	}
	select {
	case <-withdrawn:
		t.Fatal("graceful queued-event cancellation withdrew readiness")
	default:
	}
	if source.aborted != 0 {
		t.Fatalf("graceful queued-event cancellation aborted watcher %d times, want 0", source.aborted)
	}
}

func TestRunProjectWatchLoopFatalCancellationSkipsPersistence(t *testing.T) {
	root := t.TempDir()
	source := newFakeWatchSource()
	st := &persistCountingStore{}
	symbolStore := trace.NewGOBSymbolStore(filepath.Join(root, "symbols.gob"))
	ctx, cancel := context.WithCancelCause(context.Background())
	fatal := &watcher.FatalError{Operation: "another session failed", Cause: syscall.ENOSPC}
	cancel(fatal)
	err := runProjectWatchLoop(ctx, st, symbolStore, source, nil, nil, nil, nil, nil, nil, root, config.DefaultConfig(), nil, nil, nil, nil)
	if !errors.Is(err, fatal) {
		t.Fatalf("runProjectWatchLoop() error = %v, want fatal cancellation cause", err)
	}
	if st.persists != 0 {
		t.Fatalf("fatal cancellation persisted %d times, want 0", st.persists)
	}
	if source.aborted != 1 || source.closed != 0 {
		t.Fatalf("watcher aborts/closes = %d/%d, want 1/0", source.aborted, source.closed)
	}
}

func TestFatalAbortSuppressesBlockingStoreClose(t *testing.T) {
	aborted := true
	called := make(chan struct{})
	unblock := make(chan struct{})
	done := make(chan struct{})
	go func() {
		closeUnlessAborted(context.Background(), &aborted, func() error {
			close(called)
			<-unblock
			return nil
		})
		close(done)
	}()
	select {
	case <-done:
	case <-called:
		close(unblock)
		t.Fatal("fatal abort invoked blocking store Close")
	}

	aborted = false
	normalCalled := false
	closeUnlessAborted(context.Background(), &aborted, func() error {
		normalCalled = true
		return nil
	})
	if !normalCalled {
		t.Fatal("normal lifecycle skipped store Close")
	}

	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(&watcher.FatalError{Operation: "peer watcher failed", Cause: syscall.ENOSPC})
	fatalContextCalled := false
	closeUnlessAborted(ctx, &aborted, func() error {
		fatalContextCalled = true
		return nil
	})
	if fatalContextCalled {
		t.Fatal("fatal supervisor cancellation invoked store Close")
	}
}

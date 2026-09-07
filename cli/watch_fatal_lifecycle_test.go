package cli

import (
	"context"
	"errors"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/yoanbernabeu/grepai/config"
	"github.com/yoanbernabeu/grepai/trace"
	"github.com/yoanbernabeu/grepai/watcher"
)

type persistCountingStore struct {
	mockVectorStore
	persists  int
	onPersist func()
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

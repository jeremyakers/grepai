package cli

import (
	"context"
	"errors"
	"syscall"
	"testing"
	"time"

	"github.com/yoanbernabeu/grepai/embedder"
	"github.com/yoanbernabeu/grepai/indexer"
	"github.com/yoanbernabeu/grepai/watcher"
)

func TestDynamicWatchInitialLinkedFatalPreventsReadyPublication(t *testing.T) {
	mainRoot := canonicalPath("/tmp/main-barrier")
	linkedRoot := canonicalPath("/tmp/linked-barrier")
	mainReady := make(chan struct{})
	releaseLinked := make(chan struct{})
	published := make(chan struct{}, 1)

	runner := func(
		ctx context.Context,
		projectRoot string,
		_ embedder.Embedder,
		_ bool,
		onReady func(),
		_ watchSessionEventObserver,
		_ func(current, total int, file string),
		_ func(info indexer.BatchProgressInfo),
		_ func(step string, current, total int),
		_ watchActivityObserver,
		_ watchStatsObserver,
	) error {
		if projectRoot == mainRoot {
			onReady()
			close(mainReady)
			<-ctx.Done()
			return ctx.Err()
		}
		<-releaseLinked
		return &watcher.RegistrationError{Operation: "add watch", Path: linkedRoot, Cause: syscall.ENOSPC}
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- runDynamicWatchSupervisor(
			context.Background(), mainRoot, nil,
			withWatchSupervisorSessionRunner(runner),
			withWatchSupervisorInitialLinkedWorktrees([]string{linkedRoot}),
			withWatchSupervisorInitialReadyObserver(func(int) error {
				published <- struct{}{}
				return nil
			}),
		)
	}()

	select {
	case <-mainReady:
	case <-time.After(time.Second):
		t.Fatal("main session did not become ready")
	}
	select {
	case <-published:
		t.Fatal("daemon readiness published before initial linked session was ready")
	default:
	}
	close(releaseLinked)

	select {
	case err := <-errCh:
		if !errors.Is(err, syscall.ENOSPC) {
			t.Fatalf("runDynamicWatchSupervisor() error = %v, want ENOSPC", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("supervisor did not exit after initial linked registration failure")
	}
	select {
	case <-published:
		t.Fatal("daemon readiness published after initial linked session failed")
	default:
	}
}

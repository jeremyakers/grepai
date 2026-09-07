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

func TestDynamicWatchSupervisorLinkedWatcherFatalStopsAllSessions(t *testing.T) {
	mainRoot := t.TempDir()
	linkedRoot := t.TempDir()
	fatal := &watcher.FatalError{Operation: "process filesystem events", Path: linkedRoot, Cause: syscall.ENOSPC}
	runner := func(ctx context.Context, projectRoot string, _ embedder.Embedder, _ bool, onReady func(), _ watchSessionEventObserver, _ func(int, int, string), _ func(indexer.BatchProgressInfo), _ func(string, int, int), _ watchActivityObserver, _ watchStatsObserver) error {
		if projectRoot == linkedRoot {
			return fatal
		}
		onReady()
		<-ctx.Done()
		return ctx.Err()
	}

	err := runDynamicWatchSupervisor(
		context.Background(),
		mainRoot,
		nil,
		withWatchSupervisorInitialLinkedWorktrees([]string{linkedRoot}),
		withWatchSupervisorSessionRunner(runner),
	)
	if !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("runDynamicWatchSupervisor() error = %v, want ENOSPC", err)
	}
}

func TestDynamicWatchSupervisorReadyPublicationFailureReturns(t *testing.T) {
	root := t.TempDir()
	readyErr := errors.New("write ready marker: permission denied")
	runner := func(ctx context.Context, _ string, _ embedder.Embedder, _ bool, onReady func(), _ watchSessionEventObserver, _ func(int, int, string), _ func(indexer.BatchProgressInfo), _ func(string, int, int), _ watchActivityObserver, _ watchStatsObserver) error {
		onReady()
		<-ctx.Done()
		return ctx.Err()
	}
	err := runDynamicWatchSupervisor(
		context.Background(), root, nil,
		withWatchSupervisorInitialLinkedWorktrees([]string{}),
		withWatchSupervisorSessionRunner(runner),
		withWatchSupervisorInitialReadyObserver(func(int) error { return readyErr }),
	)
	if !errors.Is(err, readyErr) {
		t.Fatalf("runDynamicWatchSupervisor() error = %v, want ready write error", err)
	}
}

func TestDynamicWatchSupervisorDiscardsReadyQueuedAfterFatal(t *testing.T) {
	root := t.TempDir()
	releaseReady := make(chan struct{})
	readyQueued := make(chan struct{})
	readyPublished := false
	fatal := &watcher.FatalError{Operation: "watch", Cause: syscall.ENOSPC}
	runner := func(_ context.Context, _ string, _ embedder.Embedder, _ bool, onReady func(), _ watchSessionEventObserver, _ func(int, int, string), _ func(indexer.BatchProgressInfo), _ func(string, int, int), _ watchActivityObserver, _ watchStatsObserver) error {
		go func() {
			<-releaseReady
			onReady()
			close(readyQueued)
		}()
		return fatal
	}
	err := runDynamicWatchSupervisor(
		context.Background(), root, nil,
		withWatchSupervisorInitialLinkedWorktrees([]string{}),
		withWatchSupervisorSessionRunner(runner),
		withWatchSupervisorInitialReadyObserver(func(int) error { readyPublished = true; return nil }),
		withWatchSupervisorLifecycleObserver(func(_ string, state, _ string) {
			if state == "error" {
				close(releaseReady)
			}
		}),
	)
	if !errors.Is(err, fatal) {
		t.Fatalf("runDynamicWatchSupervisor() error = %v, want fatal", err)
	}
	<-readyQueued
	if readyPublished {
		t.Fatal("stale ready queued after fatal was published")
	}
}

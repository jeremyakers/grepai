package cli

import (
	"context"
	"errors"
	"syscall"
	"testing"

	"github.com/yoanbernabeu/grepai/config"
	"github.com/yoanbernabeu/grepai/embedder"
	"github.com/yoanbernabeu/grepai/indexer"
	"github.com/yoanbernabeu/grepai/store"
	"github.com/yoanbernabeu/grepai/watcher"
)

func TestDynamicSupervisorPreservesPeerFatalDuringMainStoreLoad(t *testing.T) {
	mainRoot := canonicalPath(t.TempDir())
	linkedRoot := canonicalPath(t.TempDir())
	cfg := config.DefaultConfig()
	cfg.Store.Backend = "gob"
	if err := cfg.Save(mainRoot); err != nil {
		t.Fatalf("save main config: %v", err)
	}

	fence := newWatchMutationFence()
	loadStarted := make(chan struct{})
	loadCanceled := make(chan struct{})
	releaseLoad := make(chan struct{})
	fatal := &watcher.FatalError{Operation: "linked watch", Path: linkedRoot, Cause: syscall.ENOSPC}
	wiring := watchProjectStartupWiring{
		initializeStore: func(ctx context.Context, _ *config.Config, _ string) (store.VectorStore, error) {
			close(loadStarted)
			<-ctx.Done()
			close(loadCanceled)
			<-releaseLoad
			return nil, ctx.Err()
		},
	}
	runner := func(ctx context.Context, projectRoot string, emb embedder.Embedder, background bool, onReady func(), onEvent watchSessionEventObserver, onScan func(int, int, string), onEmbed func(indexer.BatchProgressInfo), onRPG func(string, int, int), onActivity watchActivityObserver, onStats watchStatsObserver) error {
		if projectRoot == linkedRoot {
			<-loadStarted
			return fatal
		}
		return watchProjectWithEventObserverAndFence(
			ctx, projectRoot, emb, background, onReady, watchEventObserver(onEvent),
			onScan, onEmbed, onRPG, onActivity, onStats, nil, fence, wiring,
		)
	}

	result := make(chan error, 1)
	go func() {
		result <- runDynamicWatchSupervisor(
			context.Background(), mainRoot, &noOpEmbedder{},
			withWatchSupervisorInitialLinkedWorktrees([]string{linkedRoot}),
			withWatchSupervisorSessionRunner(runner),
			withWatchSupervisorMutationFence(fence),
		)
	}()
	awaitWatchTestSignal(t, loadCanceled, "main store load fatal cancellation")
	close(releaseLoad)
	err := awaitWatchTestValue(t, result, "dynamic supervisor fatal return")
	if !errors.Is(err, fatal) {
		t.Fatalf("runDynamicWatchSupervisor() error = %v, want original linked fatal", err)
	}
	if errors.Is(err, context.Canceled) {
		t.Fatalf("runDynamicWatchSupervisor() masked peer fatal as context cancellation: %v", err)
	}
}

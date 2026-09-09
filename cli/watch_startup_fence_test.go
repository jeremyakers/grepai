package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/yoanbernabeu/grepai/config"
	"github.com/yoanbernabeu/grepai/embedder"
	"github.com/yoanbernabeu/grepai/indexer"
	"github.com/yoanbernabeu/grepai/watcher"
)

type startupBlockingEmbedder struct {
	started  chan struct{}
	canceled chan struct{}
	cause    chan error
	release  chan struct{}
	once     sync.Once
}

func (e *startupBlockingEmbedder) block(ctx context.Context) error {
	e.once.Do(func() { close(e.started) })
	<-ctx.Done()
	e.cause <- context.Cause(ctx)
	close(e.canceled)
	<-e.release
	return ctx.Err()
}

func (e *startupBlockingEmbedder) Embed(ctx context.Context, _ string) ([]float32, error) {
	return nil, e.block(ctx)
}

func (e *startupBlockingEmbedder) EmbedBatch(ctx context.Context, _ []string) ([][]float32, error) {
	return nil, e.block(ctx)
}

func (e *startupBlockingEmbedder) Dimensions() int { return 3 }
func (e *startupBlockingEmbedder) Close() error    { return nil }

func TestDynamicLinkedStartupQuiescesBeforeFatalWithdrawal(t *testing.T) {
	mainRoot := canonicalPath(t.TempDir())
	linkedRoot := canonicalPath(t.TempDir())
	cfg := config.DefaultConfig()
	cfg.Store.Backend = "gob"
	cfg.RPG.Enabled = false
	if err := cfg.Save(linkedRoot); err != nil {
		t.Fatalf("save linked config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(linkedRoot, "main.go"), []byte("package linked\n\nfunc startupMutation() {}\n"), 0644); err != nil {
		t.Fatalf("write linked source: %v", err)
	}

	emb := &startupBlockingEmbedder{
		started:  make(chan struct{}),
		canceled: make(chan struct{}),
		cause:    make(chan error, 1),
		release:  make(chan struct{}),
	}
	fence := newWatchMutationFence()
	fatal := &watcher.FatalError{Operation: "watch", Path: mainRoot, Cause: syscall.ENOSPC}
	mainMayFail := make(chan struct{})
	mainReady := make(chan struct{})
	withdrawn := make(chan struct{})
	var linkedDesired atomic.Bool

	runner := func(ctx context.Context, projectRoot string, emb embedder.Embedder, background bool, onReady func(), onEvent watchSessionEventObserver, onScan func(int, int, string), onEmbed func(indexer.BatchProgressInfo), onRPG func(string, int, int), onActivity watchActivityObserver, onStats watchStatsObserver) error {
		if projectRoot == mainRoot {
			onReady()
			close(mainReady)
			<-mainMayFail
			return fatal
		}
		return watchProjectWithEventObserverAndFence(ctx, projectRoot, emb, background, onReady, watchEventObserver(onEvent), onScan, onEmbed, onRPG, onActivity, onStats, nil, fence)
	}

	result := make(chan error, 1)
	go func() {
		result <- runDynamicWatchSupervisor(
			context.Background(), mainRoot, emb,
			withWatchSupervisorInitialLinkedWorktrees([]string{}),
			withWatchSupervisorDiscoverWorktrees(func(string) []string {
				if linkedDesired.Load() {
					return []string{linkedRoot}
				}
				return nil
			}),
			withWatchSupervisorReconcileInterval(time.Millisecond),
			withWatchSupervisorSessionRunner(runner),
			withWatchSupervisorMutationFence(fence),
			withWatchSupervisorFatalObserver(func() { close(withdrawn) }),
		)
	}()
	awaitWatchTestSignal(t, mainReady, "main watcher readiness")
	linkedDesired.Store(true)
	awaitWatchTestSignal(t, emb.started, "real linked initial scan")

	close(mainMayFail)
	awaitWatchTestSignal(t, emb.canceled, "linked startup cancellation")
	if cause := awaitWatchTestValue(t, emb.cause, "linked startup cancellation cause"); !errors.Is(cause, fatal) {
		t.Fatalf("linked startup cancellation cause = %v, want fatal", cause)
	}
	select {
	case <-withdrawn:
		t.Fatal("readiness withdrawn before linked startup quiesced")
	default:
	}
	select {
	case err := <-result:
		t.Fatalf("supervisor returned before linked startup quiesced: %v", err)
	default:
	}

	close(emb.release)
	awaitWatchTestSignal(t, withdrawn, "fatal readiness withdrawal")
	if err := awaitWatchTestValue(t, result, "supervisor return"); !errors.Is(err, fatal) {
		t.Fatalf("runDynamicWatchSupervisor() error = %v, want fatal", err)
	}
	for _, path := range []string{config.GetIndexPath(linkedRoot), config.GetSymbolIndexPath(linkedRoot), config.GetRPGIndexPath(linkedRoot)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("fatal startup created persistence-bearing artifact %s: %v", path, err)
		}
	}
}

func TestDynamicLinkedWatcherRegistrationHandoffFencesFatalCleanup(t *testing.T) {
	mainRoot := canonicalPath(t.TempDir())
	linkedRoot := canonicalPath(t.TempDir())
	cfg := config.DefaultConfig()
	cfg.Store.Backend = "gob"
	cfg.RPG.Enabled = false
	if err := cfg.Save(linkedRoot); err != nil {
		t.Fatalf("save linked config: %v", err)
	}

	fence := newWatchMutationFence()
	fatal := &watcher.FatalError{Operation: "watch", Path: mainRoot, Cause: syscall.ENOSPC}
	mainMayFail := make(chan struct{})
	mainReady := make(chan struct{})
	registrationBlocked := make(chan struct{})
	registrationCanceled := make(chan struct{})
	releaseRegistration := make(chan struct{})
	withdrawn := make(chan struct{})
	hookErr := make(chan error, 1)
	var linkedDesired atomic.Bool
	artifacts := []string{config.GetIndexPath(linkedRoot), config.GetSymbolIndexPath(linkedRoot)}

	wiring := watchProjectStartupWiring{beforeWatcherRegistration: func(ctx context.Context) {
		for _, path := range artifacts {
			if _, err := os.Stat(path); err != nil {
				hookErr <- errors.New("startup persistence did not create " + path)
				return
			}
			if err := os.Remove(path); err != nil {
				hookErr <- err
				return
			}
		}
		close(registrationBlocked)
		<-ctx.Done()
		close(registrationCanceled)
		<-releaseRegistration
		hookErr <- nil
	}}
	runner := func(ctx context.Context, projectRoot string, emb embedder.Embedder, background bool, onReady func(), onEvent watchSessionEventObserver, onScan func(int, int, string), onEmbed func(indexer.BatchProgressInfo), onRPG func(string, int, int), onActivity watchActivityObserver, onStats watchStatsObserver) error {
		if projectRoot == mainRoot {
			onReady()
			close(mainReady)
			<-mainMayFail
			return fatal
		}
		return watchProjectWithEventObserverAndFence(ctx, projectRoot, emb, background, onReady, watchEventObserver(onEvent), onScan, onEmbed, onRPG, onActivity, onStats, nil, fence, wiring)
	}

	result := make(chan error, 1)
	go func() {
		result <- runDynamicWatchSupervisor(
			context.Background(), mainRoot, &noOpEmbedder{},
			withWatchSupervisorInitialLinkedWorktrees([]string{}),
			withWatchSupervisorDiscoverWorktrees(func(string) []string {
				if linkedDesired.Load() {
					return []string{linkedRoot}
				}
				return nil
			}),
			withWatchSupervisorReconcileInterval(time.Millisecond),
			withWatchSupervisorSessionRunner(runner),
			withWatchSupervisorMutationFence(fence),
			withWatchSupervisorFatalObserver(func() { close(withdrawn) }),
		)
	}()
	awaitWatchTestSignal(t, mainReady, "main watcher readiness")
	linkedDesired.Store(true)
	awaitWatchTestSignal(t, registrationBlocked, "linked watcher registration handoff")
	close(mainMayFail)
	awaitWatchTestSignal(t, registrationCanceled, "registration cancellation")
	select {
	case <-withdrawn:
		t.Fatal("readiness withdrawn before registration handoff quiesced")
	default:
	}

	close(releaseRegistration)
	if err := awaitWatchTestValue(t, hookErr, "registration hook return"); err != nil {
		t.Fatal(err)
	}
	awaitWatchTestSignal(t, withdrawn, "fatal readiness withdrawal")
	if err := awaitWatchTestValue(t, result, "supervisor return"); !errors.Is(err, fatal) {
		t.Fatalf("runDynamicWatchSupervisor() error = %v, want fatal", err)
	}
	for _, path := range artifacts {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("store Close ran after fatal handoff for %s: %v", path, err)
		}
	}
}

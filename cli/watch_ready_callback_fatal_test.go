package cli

import (
	"context"
	"errors"
	"testing"

	"github.com/yoanbernabeu/grepai/watcher"
)

type nonCooperativeCloseWatchSource struct {
	*fakeWatchSource
	closeStarted chan struct{}
	blockClose   chan struct{}
}

type abortNotifyingCloseWatchSource struct {
	*nonCooperativeCloseWatchSource
	abortStarted chan struct{}
}

func (w *abortNotifyingCloseWatchSource) Abort() {
	w.nonCooperativeCloseWatchSource.Abort()
	close(w.abortStarted)
}

func newNonCooperativeCloseWatchSource() *nonCooperativeCloseWatchSource {
	return &nonCooperativeCloseWatchSource{
		fakeWatchSource: newFakeWatchSource(),
		closeStarted:    make(chan struct{}),
		blockClose:      make(chan struct{}),
	}
}

func (w *nonCooperativeCloseWatchSource) Close() error {
	close(w.closeStarted)
	<-w.blockClose
	return nil
}

func (w *nonCooperativeCloseWatchSource) Abort() { w.aborted++ }

func testReadyCallbackFatalReturnsPromptly(t *testing.T, scope string, sources []watchSource) {
	t.Helper()
	markerErr := errors.New("ready marker write failed")
	abortStores := false
	storeCloseCalled := make(chan struct{}, 1)
	result := make(chan error, 1)
	for _, source := range sources {
		if blocking, ok := source.(*nonCooperativeCloseWatchSource); ok {
			defer close(blocking.blockClose)
		}
	}
	go func() {
		err := abortWatcherReadiness(&abortStores, sources, scope, markerErr, nil)
		closeUnlessAborted(context.Background(), &abortStores, func() error {
			storeCloseCalled <- struct{}{}
			return nil
		})
		result <- err
	}()

	err := <-result
	var fatalErr *watcher.FatalError
	if !errors.As(err, &fatalErr) || !errors.Is(err, markerErr) {
		t.Fatalf("abortWatcherReadiness() error = %T %v, want fatal marker error", err, err)
	}
	for _, source := range sources {
		blocking := source.(*nonCooperativeCloseWatchSource)
		select {
		case <-blocking.closeStarted:
			t.Fatal("fatal helper invoked non-cooperative Close")
		default:
		}
	}
	select {
	case <-storeCloseCalled:
		t.Fatal("ready callback fatal path invoked persistence-bearing store Close")
	default:
	}
}

func TestProjectReadyCallbackErrorIsFatalWithoutBlockingClose(t *testing.T) {
	source := newNonCooperativeCloseWatchSource()
	testReadyCallbackFatalReturnsPromptly(t, "project /repo", []watchSource{source})
	if source.aborted != 1 {
		t.Fatalf("project watcher aborted %d times, want 1", source.aborted)
	}
}

func TestWorkspaceReadyCallbackErrorIsFatalWithoutBlockingCloses(t *testing.T) {
	first := newNonCooperativeCloseWatchSource()
	second := newNonCooperativeCloseWatchSource()
	testReadyCallbackFatalReturnsPromptly(t, "workspace ws", []watchSource{first, second})
	for i, source := range []*nonCooperativeCloseWatchSource{first, second} {
		if source.aborted != 1 {
			t.Fatalf("workspace watcher %d aborted %d times, want 1", i, source.aborted)
		}
	}
}

func TestPublishWorkspaceReadinessFailureWaitsForWorkerQuiescence(t *testing.T) {
	source := &abortNotifyingCloseWatchSource{
		nonCooperativeCloseWatchSource: newNonCooperativeCloseWatchSource(),
		abortStarted:                   make(chan struct{}),
	}
	defer close(source.blockClose)
	fence := newWatchMutationFence()
	if !fence.addWatcher(source) {
		t.Fatal("failed to register workspace watcher")
	}
	workerCanceled := make(chan struct{})
	releaseWorker := make(chan struct{})
	worker := startWatchMutationWorker(context.Background(), fence, func(ctx context.Context) {
		<-ctx.Done()
		close(workerCanceled)
		<-releaseWorker
	})
	awaitWatchTestSignal(t, worker.started, "workspace worker admission")

	publishErr := errors.New("write workspace ready marker: permission denied")
	withdrawn := make(chan struct{})
	abortStores := false
	abortWatcherClose := false
	result := make(chan error, 1)
	go func() {
		result <- publishWorkspaceReadiness(
			fence, []watchSource{source}, "workspace ws",
			func() error { return publishErr }, func() { close(withdrawn) },
			&abortStores, &abortWatcherClose,
		)
	}()
	awaitWatchTestSignal(t, workerCanceled, "workspace worker cancellation")
	awaitWatchTestSignal(t, source.abortStarted, "workspace watcher abort")
	select {
	case <-withdrawn:
		t.Fatal("workspace readiness withdrawn before worker quiesced")
	default:
	}
	select {
	case err := <-result:
		t.Fatalf("workspace readiness failure returned before worker quiesced: %v", err)
	default:
	}
	close(releaseWorker)
	err := awaitWatchTestValue(t, result, "workspace readiness failure return")
	if !errors.Is(err, publishErr) {
		t.Fatalf("publishWorkspaceReadiness() error = %v, want original publication error", err)
	}
	awaitWatchTestSignal(t, withdrawn, "workspace readiness withdrawal")
	awaitWatchTestSignal(t, worker.done, "workspace worker return")
	if !abortStores || !abortWatcherClose {
		t.Fatalf("abort flags = stores:%t watcher-close:%t, want both true", abortStores, abortWatcherClose)
	}
	if source.aborted != 1 || source.closed != 0 {
		t.Fatalf("watcher aborts/closes = %d/%d, want 1/0", source.aborted, source.closed)
	}
	persisted := false
	closeUnlessAborted(context.Background(), &abortStores, func() error {
		persisted = true
		return nil
	})
	if persisted {
		t.Fatal("workspace readiness fatal path invoked persistence-bearing cleanup")
	}
	select {
	case <-source.closeStarted:
		t.Fatal("workspace readiness fatal path invoked watcher Close")
	default:
	}
}

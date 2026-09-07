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

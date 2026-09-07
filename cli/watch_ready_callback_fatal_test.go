package cli

import (
	"context"
	"errors"
	"testing"
	"time"

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

func testReadyCallbackFatalReturnsPromptly(t *testing.T, scope string, sources []watchSource) {
	t.Helper()
	markerErr := errors.New("ready marker write failed")
	abortStores := false
	storeCloseCalled := make(chan struct{}, 1)
	result := make(chan error, 1)
	go func() {
		err := abortWatcherReadiness(&abortStores, sources, scope, markerErr, nil)
		closeUnlessAborted(context.Background(), &abortStores, func() error {
			storeCloseCalled <- struct{}{}
			select {}
		})
		result <- err
	}()

	select {
	case err := <-result:
		var fatalErr *watcher.FatalError
		if !errors.As(err, &fatalErr) || !errors.Is(err, markerErr) {
			t.Fatalf("abortWatcherReadiness() error = %T %v, want fatal marker error", err, err)
		}
	case <-time.After(time.Second):
		t.Fatal("ready callback fatal path blocked on watcher/store close")
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
	select {
	case <-source.closeStarted:
	case <-time.After(time.Second):
		t.Fatal("project watcher close was not started")
	}
}

func TestWorkspaceReadyCallbackErrorIsFatalWithoutBlockingCloses(t *testing.T) {
	first := newNonCooperativeCloseWatchSource()
	second := newNonCooperativeCloseWatchSource()
	testReadyCallbackFatalReturnsPromptly(t, "workspace ws", []watchSource{first, second})
	for i, source := range []*nonCooperativeCloseWatchSource{first, second} {
		select {
		case <-source.closeStarted:
		case <-time.After(time.Second):
			t.Fatalf("workspace watcher %d close was not started", i)
		}
	}
}

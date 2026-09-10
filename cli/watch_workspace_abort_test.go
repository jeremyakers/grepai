package cli

import "testing"

func TestWorkspaceRuntimeFatalAbortsEveryWatcherWithoutClose(t *testing.T) {
	first := newNonCooperativeCloseWatchSource()
	second := newNonCooperativeCloseWatchSource()
	defer close(first.blockClose)
	defer close(second.blockClose)

	abortWatchSources([]watchSource{first, second})

	for i, source := range []*nonCooperativeCloseWatchSource{first, second} {
		if source.aborted != 1 || source.closed != 0 {
			t.Fatalf("watcher %d aborts/closes = %d/%d, want 1/0", i, source.aborted, source.closed)
		}
		select {
		case <-source.closeStarted:
			t.Fatalf("watcher %d non-cooperative Close was invoked", i)
		default:
		}
	}
}

func TestFatalAbortHelperCreatesNoCloseGoroutines(t *testing.T) {
	source := newNonCooperativeCloseWatchSource()
	defer close(source.blockClose)
	returned := make(chan struct{})
	go func() {
		abortWatchSources([]watchSource{source})
		close(returned)
	}()

	select {
	case <-source.closeStarted:
		t.Fatal("fatal abort helper spawned a goroutine calling Close")
	case <-returned:
	}
}

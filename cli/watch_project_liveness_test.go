package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yoanbernabeu/grepai/config"
	"github.com/yoanbernabeu/grepai/watcher"
)

func TestHealthyProjectWatcherRemainsLiveAfterReadiness(t *testing.T) {
	root := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Store.Backend = "gob"
	cfg.RPG.Enabled = false
	cfg.Watch.DebounceMs = 10
	if err := cfg.Save(root); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{})
	events := make(chan watcher.FileEvent, 1)
	done := make(chan error, 1)
	go func() {
		done <- watchProjectWithEventObserver(
			ctx, root, &noOpEmbedder{}, true, func() { close(ready) },
			func(_ string, event watcher.FileEvent) { events <- event },
			nil, nil, nil, nil, nil, nil,
		)
	}()

	awaitProjectWatchSignal(t, ready, "watcher readiness")
	path := filepath.Join(root, "live.go")
	if err := os.WriteFile(path, []byte("package live\nfunc Changed() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	select {
	case event := <-events:
		if event.Path != "live.go" {
			t.Fatalf("event path = %q, want live.go", event.Path)
		}
	case err := <-done:
		t.Fatalf("watcher exited after readiness: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("healthy watcher did not observe a post-readiness file event")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("graceful watcher shutdown returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("healthy watcher did not join after cancellation")
	}
}

func awaitProjectWatchSignal(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}

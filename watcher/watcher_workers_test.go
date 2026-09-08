package watcher

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

func TestBackendDrainsWhileOutputDeliveryIsStalled(t *testing.T) {
	root := t.TempDir()
	w := newDirectoryTestWatcher(t, root)
	w.debounceMs = int(time.Hour / time.Millisecond)
	backendEvents := make(chan fsnotify.Event)
	backendErrors := make(chan error)
	w.backendEvents = backendEvents
	w.backendErrors = backendErrors
	for i := 0; i < cap(w.events); i++ {
		w.events <- FileEvent{Path: "barrier"}
	}
	w.pending["initial.go"] = FileEvent{Type: EventCreate, Path: "initial.go"}
	if err := w.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	w.flushReady <- struct{}{}

	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	w.awaitPendingDrained(t, deadline.C)
	changed := filepath.Join(root, "changed.go")
	if err := os.WriteFile(changed, []byte("package changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	errorDrained := make(chan struct{})
	eventDrained := make(chan struct{})
	go func() {
		backendErrors <- errors.New("backend overflow")
		close(errorDrained)
	}()
	go func() {
		backendEvents <- fsnotify.Event{Name: changed, Op: fsnotify.Write}
		close(eventDrained)
	}()
	for errorDrained != nil || eventDrained != nil {
		select {
		case <-errorDrained:
			errorDrained = nil
		case <-eventDrained:
			eventDrained = nil
		case <-deadline.C:
			t.Fatal("backend was not drained while output delivery was stalled")
		}
	}

	// Resume the consumer and prove the stalled batch can complete normally.
	for {
		select {
		case event := <-w.Events():
			if event.Path == "initial.go" {
				return
			}
		case <-deadline.C:
			t.Fatal("stalled delivery did not resume")
		}
	}
}

func TestWatcherShutdownJoinsBackendAndDeliveryWorkers(t *testing.T) {
	for _, mode := range []string{"close", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			w := newDirectoryTestWatcher(t, root)
			backendEvents := make(chan fsnotify.Event)
			backendErrors := make(chan error)
			w.backendEvents = backendEvents
			w.backendErrors = backendErrors
			for i := 0; i < cap(w.events); i++ {
				w.events <- FileEvent{Path: "barrier"}
			}
			w.pending["blocked.go"] = FileEvent{Type: EventCreate, Path: "blocked.go"}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if err := w.Start(ctx); err != nil {
				t.Fatal(err)
			}
			w.flushReady <- struct{}{}
			deadline := time.NewTimer(5 * time.Second)
			defer deadline.Stop()
			w.awaitPendingDrained(t, deadline.C)

			joined := make(chan struct{})
			if mode == "close" {
				go func() {
					_ = w.Close()
					close(joined)
				}()
			} else {
				cancel()
				go func() {
					w.workers.Wait()
					close(joined)
				}()
			}
			select {
			case <-joined:
			case <-deadline.C:
				t.Fatalf("%s did not stop both watcher workers", mode)
			}
			if mode == "cancel" {
				if err := w.Close(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

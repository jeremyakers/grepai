package watcher

import (
	"errors"
	"testing"

	"github.com/fsnotify/fsnotify"
)

func TestHandleEventReturnsFatalWhenWatchedRootIsLost(t *testing.T) {
	for _, tc := range []struct {
		name string
		op   fsnotify.Op
	}{
		{name: "removed", op: fsnotify.Remove},
		{name: "renamed", op: fsnotify.Rename},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Given
			root := t.TempDir()
			w := newTestWatcher(t, root)
			defer w.Close()

			// When
			err := w.handleEvent(fsnotify.Event{Name: root, Op: tc.op})

			// Then
			var fatalErr *FatalError
			if !errors.As(err, &fatalErr) {
				t.Fatalf("handleEvent() error = %T %v, want FatalError for watched root loss", err, err)
			}
			if !errors.Is(err, errWatchRootLost) {
				t.Fatalf("handleEvent() error = %v, want watched-root-loss cause", err)
			}
			if fatalErr.Path != root {
				t.Fatalf("FatalError.Path = %q, want %q", fatalErr.Path, root)
			}
		})
	}
}

//go:build windows

package watcher

import (
	"os"
	"path/filepath"
	"testing"
)

func renameWatchedTreeOut(t *testing.T, inside, _ string) string {
	t.Helper()
	// Windows can reject cross-parent directory moves while fsnotify holds
	// descendant handles. A same-parent rename exercises the rename event and
	// old-subtree watch release without relying on that unavailable operation.
	renamed := filepath.Join(filepath.Dir(inside), "renamed")
	if err := os.Rename(inside, renamed); err != nil {
		t.Fatal(err)
	}
	return renamed
}

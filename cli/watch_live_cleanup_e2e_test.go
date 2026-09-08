//go:build e2e

package cli_test

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWatchLiveCleanupPersistsWhileProcessRemainsRunning(t *testing.T) {
	bin := buildLiveCleanupCandidate(t)
	for _, scenario := range []struct {
		name            string
		deleteDirectory bool
	}{
		{name: "plain-file"},
		{name: "nested-directory", deleteDirectory: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			h := newLiveCleanupHarness(t, bin)
			for _, dir := range []string{filepath.Join(h.root, "src", "nested"), filepath.Join(h.root, "src-old")} {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			h.init()
			watch := h.startWatch()
			watch.waitFor("Watching for changes")
			pid := watch.cmd.Process.Pid
			watch.assertRunning(pid)

			h.write("src/nested/delete.go", "package nested\nfunc LiveDeleteTarget() {}\nfunc LiveDeleteCaller() { LiveDeleteTarget() }\n")
			h.write("src-old/keep.go", "package srcold\nfunc LiveKeepTarget() {}\nfunc LiveKeepCaller() { LiveKeepTarget() }\n")
			h.awaitState(watch, pid, liveCleanupExpectation{
				presentFiles:  []string{"src/nested/delete.go", "src-old/keep.go"},
				presentTraces: map[string]string{"LiveDeleteTarget": "src/nested/delete.go", "LiveKeepTarget": "src-old/keep.go"},
			})

			if scenario.deleteDirectory {
				if err := os.RemoveAll(filepath.Join(h.root, "src")); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Remove(filepath.Join(h.root, "src", "nested", "delete.go")); err != nil {
				t.Fatal(err)
			}
			h.awaitState(watch, pid, liveCleanupExpectation{
				presentFiles: []string{"src-old/keep.go"}, absentFiles: []string{"src/nested/delete.go"},
				presentTraces: map[string]string{"LiveKeepTarget": "src-old/keep.go"}, absentTraces: []string{"LiveDeleteTarget"},
			})
			watch.assertRunning(pid)
			if _, err := os.Stat(filepath.Join(h.root, "src-old", "keep.go")); err != nil {
				t.Fatalf("boundary sibling removed: %v", err)
			}
			// Persistent document, chunk, symbol, reference, and real trace CLI
			// assertions deliberately complete before the watcher is stopped.
			watch.stop()
		})
	}
}

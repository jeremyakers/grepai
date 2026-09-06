package cli

import (
	"context"
	"errors"
	"testing"

	"github.com/yoanbernabeu/grepai/trace"
)

type failingWatchSymbolStore struct {
	trace.SymbolStore
	loadErr error
	closes  int
}

func (s *failingWatchSymbolStore) Load(context.Context) error { return s.loadErr }
func (s *failingWatchSymbolStore) Close() error               { s.closes++; return nil }

func TestPostgresLoadPolicyStopsCallbackBeforeScan(t *testing.T) {
	store := &failingWatchSymbolStore{loadErr: errors.New("migration failed")}
	scans := 0
	err := runAfterWatcherSymbolLoad(context.Background(), "postgres", "project", store, func() error { scans++; return nil })
	if err == nil || scans != 0 || store.closes != 0 {
		t.Fatalf("err=%v scans=%d closes=%d", err, scans, store.closes)
	}
}

func TestWorkspacePostgresSymbolLoadStopsAndClosesBeforeScan(t *testing.T) {
	store := &failingWatchSymbolStore{loadErr: errors.New("migration failed")}
	scans := 0
	err := initializeWorkspaceSymbolStore(context.Background(), "postgres", "project", store, func() error { scans++; return nil })
	if err == nil || scans != 0 || store.closes != 1 {
		t.Fatalf("err=%v scans=%d closes=%d", err, scans, store.closes)
	}
}

func TestGOBSymbolLoadWarningContinues(t *testing.T) {
	store := &failingWatchSymbolStore{loadErr: errors.New("legacy snapshot damaged")}
	scans := 0
	if err := runAfterWatcherSymbolLoad(context.Background(), "gob", "project", store, func() error { scans++; return nil }); err != nil || scans != 1 {
		t.Fatalf("err=%v scans=%d", err, scans)
	}
}

func TestWorkspaceSymbolStoreSuccessRemainsOpen(t *testing.T) {
	// Given a store that loads successfully.
	store := &failingWatchSymbolStore{}
	scans := 0

	// When workspace initialization completes.
	err := initializeWorkspaceSymbolStore(context.Background(), "postgres", "project", store, func() error {
		scans++
		return nil
	})

	// Then the scan ran and ownership of the open store is returned.
	if err != nil || scans != 1 || store.closes != 0 {
		t.Fatalf("err=%v scans=%d closes=%d", err, scans, store.closes)
	}
}

func TestWorkspaceSymbolStoreScanFailureCloses(t *testing.T) {
	// Given a successfully loaded store whose scan fails.
	store := &failingWatchSymbolStore{}
	want := errors.New("scan failed")

	// When workspace initialization invokes the scan seam.
	err := initializeWorkspaceSymbolStore(context.Background(), "postgres", "project", store, func() error { return want })

	// Then the error propagates and the unreturned store is closed.
	if !errors.Is(err, want) || store.closes != 1 {
		t.Fatalf("err=%v closes=%d", err, store.closes)
	}
}

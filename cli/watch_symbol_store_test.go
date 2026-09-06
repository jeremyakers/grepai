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

func TestSingleProjectPostgresSymbolLoadStopsBeforeScan(t *testing.T) {
	store := &failingWatchSymbolStore{loadErr: errors.New("migration failed")}
	scans := 0
	err := runAfterWatcherSymbolLoad(context.Background(), "postgres", "project", store, func() error { scans++; return nil })
	_ = store.Close() // models the immediately-installed single-project defer
	if err == nil || scans != 0 || store.closes != 1 {
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

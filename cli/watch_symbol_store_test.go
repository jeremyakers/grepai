package cli

import (
	"context"
	"errors"
	"testing"

	"github.com/yoanbernabeu/grepai/trace"
	"github.com/yoanbernabeu/grepai/watcher"
)

type failingWatchSymbolStore struct {
	trace.SymbolStore
	loadErr error
	closes  int
}

func (s *failingWatchSymbolStore) Load(context.Context) error { return s.loadErr }
func (s *failingWatchSymbolStore) Close() error               { s.closes++; return nil }

type lockHeldWatchSymbolStore struct {
	failingWatchSymbolStore
	ordinaryLoads int
	heldLoads     int
}

func (s *lockHeldWatchSymbolStore) Load(context.Context) error {
	s.ordinaryLoads++
	return nil
}

func (s *lockHeldWatchSymbolStore) LoadWithProjectWriterLockHeld(context.Context) error {
	s.heldLoads++
	return nil
}

func TestPostgresWatcherLoadUsesLockHeldPath(t *testing.T) {
	// Given a Postgres watcher store with distinct ordinary and lock-held loaders.
	store := &lockHeldWatchSymbolStore{}

	// When the watcher helper loads the symbol store.
	err := runAfterWatcherSymbolLoad(context.Background(), "postgres", "project", store, nil)

	// Then it uses only the path that assumes the watcher lock is already held.
	if err != nil || store.heldLoads != 1 || store.ordinaryLoads != 0 {
		t.Fatalf("err=%v heldLoads=%d ordinaryLoads=%d", err, store.heldLoads, store.ordinaryLoads)
	}
}

func TestPostgresLoadPolicyStopsCallbackBeforeScan(t *testing.T) {
	store := &failingWatchSymbolStore{loadErr: errors.New("migration failed")}
	scans := 0
	err := runAfterWatcherSymbolLoad(context.Background(), "postgres", "project", store, func() error { scans++; return nil })
	if err == nil || scans != 0 || store.closes != 0 {
		t.Fatalf("err=%v scans=%d closes=%d", err, scans, store.closes)
	}
}

func TestPostgresLoadFailureMarkedRequiredInit(t *testing.T) {
	loadErr := errors.New("migration failed")
	store := &failingWatchSymbolStore{loadErr: loadErr}

	err := runAfterWatcherSymbolLoad(context.Background(), "postgres", "project", store, nil)

	if !isRequiredSymbolStoreInitError(err) {
		t.Fatalf("postgres load error = %v, want required symbol store init error", err)
	}
	if !errors.Is(err, loadErr) {
		t.Fatalf("postgres load error = %v, want errors.Is(%v)", err, loadErr)
	}
	var registrationErr *watcher.RegistrationError
	if errors.As(err, &registrationErr) {
		t.Fatalf("postgres load error = %v, must not be mislabeled as RegistrationError", err)
	}
}

func TestGOBLoadWarningRemainsOptional(t *testing.T) {
	store := &failingWatchSymbolStore{loadErr: errors.New("legacy snapshot damaged")}

	err := runAfterWatcherSymbolLoad(context.Background(), "gob", "project", store, nil)

	if err != nil {
		t.Fatalf("gob load error = %v, want warning-only nil", err)
	}
	if isRequiredSymbolStoreInitError(err) {
		t.Fatal("gob load warning must not become a required init error")
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

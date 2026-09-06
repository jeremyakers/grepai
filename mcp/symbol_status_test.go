package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/yoanbernabeu/grepai/trace"
)

type closeCountingSymbolStore struct {
	trace.SymbolStore
	loadErr  error
	statsErr error
	closes   int
}

func (s *closeCountingSymbolStore) Load(context.Context) error { return s.loadErr }
func (s *closeCountingSymbolStore) GetStats(context.Context) (*trace.SymbolStats, error) {
	return &trace.SymbolStats{TotalSymbols: 3}, s.statsErr
}
func (s *closeCountingSymbolStore) Close() error { s.closes++; return nil }

func TestReadSymbolStatusClosesStoreOnLoadFailure(t *testing.T) {
	store := &closeCountingSymbolStore{loadErr: errors.New("load failed")}
	ready, total := readAndCloseSymbolStatus(context.Background(), store)
	if ready || total != 0 || store.closes != 1 {
		t.Fatalf("ready=%v total=%d closes=%d", ready, total, store.closes)
	}
}

func TestReadSymbolStatusClosesStoreOnStatsFailure(t *testing.T) {
	store := &closeCountingSymbolStore{statsErr: errors.New("stats failed")}
	ready, total := readAndCloseSymbolStatus(context.Background(), store)
	if ready || total != 0 || store.closes != 1 {
		t.Fatalf("ready=%v total=%d closes=%d", ready, total, store.closes)
	}
}

func TestReadSymbolStatusReturnsReadyAndClosesOnSuccess(t *testing.T) {
	store := &closeCountingSymbolStore{}
	ready, total := readAndCloseSymbolStatus(context.Background(), store)
	if !ready || total != 3 || store.closes != 1 {
		t.Fatalf("ready=%v total=%d closes=%d", ready, total, store.closes)
	}
}

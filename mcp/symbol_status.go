package mcp

import (
	"context"

	"github.com/yoanbernabeu/grepai/trace"
)

func readAndCloseSymbolStatus(ctx context.Context, symbolStore trace.SymbolStore) (bool, int) {
	defer symbolStore.Close()
	if err := symbolStore.Load(ctx); err != nil {
		return false, 0
	}
	total, err := trace.CountSymbolsForReadiness(ctx, symbolStore)
	if err != nil || total == 0 {
		return false, 0
	}
	return true, total
}

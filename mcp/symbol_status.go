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
	stats, err := symbolStore.GetStats(ctx)
	if err != nil || stats.TotalSymbols == 0 {
		return false, 0
	}
	return true, stats.TotalSymbols
}

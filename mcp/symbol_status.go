package mcp

import (
	"context"

	"github.com/yoanbernabeu/grepai/trace"
)

type symbolCounter interface {
	CountSymbols(context.Context) (int, error)
}

func readAndCloseSymbolStatus(ctx context.Context, symbolStore trace.SymbolStore) (bool, int) {
	defer symbolStore.Close()
	if err := symbolStore.Load(ctx); err != nil {
		return false, 0
	}
	if counter, ok := symbolStore.(symbolCounter); ok {
		total, err := counter.CountSymbols(ctx)
		if err != nil || total == 0 {
			return false, 0
		}
		return true, total
	}
	stats, err := symbolStore.GetStats(ctx)
	if err != nil || stats.TotalSymbols == 0 {
		return false, 0
	}
	return true, stats.TotalSymbols
}

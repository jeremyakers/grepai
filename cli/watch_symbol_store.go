package cli

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/yoanbernabeu/grepai/trace"
)

func runAfterWatcherSymbolLoad(ctx context.Context, backend, project string, symbolStore trace.SymbolStore, next func() error) error {
	if err := symbolStore.Load(ctx); err != nil {
		if backend == "postgres" {
			return fmt.Errorf("failed to load Postgres symbol index for %s: %w", project, err)
		}
		log.Printf("Warning: failed to load symbol index for %s: %v", project, err)
	}
	if next != nil {
		return next()
	}
	return nil
}

func initializeWorkspaceSymbolStore(ctx context.Context, backend, project string, symbolStore trace.SymbolStore, scan func() error) error {
	if err := runAfterWatcherSymbolLoad(ctx, backend, project, symbolStore, scan); err != nil {
		return errors.Join(err, symbolStore.Close())
	}
	return nil
}

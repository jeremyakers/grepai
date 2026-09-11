package trace

import "context"

// CallerLookupResult contains the target and caller definitions plus the call
// references that connect them. Symbols is keyed by symbol name.
type CallerLookupResult struct {
	Symbols    map[string][]Symbol
	References []Reference
}

// CallerResultStore optionally reads all data needed for a caller trace from a
// single store snapshot.
type CallerResultStore interface {
	LookupCallerResult(ctx context.Context, symbolName string) (CallerLookupResult, error)
}

// LookupCallerResult prefers a store's compound snapshot capability. Stores
// without it retain the loaded-store lookup path with one deterministic batch
// for the target and all unique caller names.
func LookupCallerResult(ctx context.Context, store SymbolStore, symbolName string) (CallerLookupResult, error) {
	if source, ok := store.(CallerResultStore); ok {
		result, err := source.LookupCallerResult(ctx, symbolName)
		if err != nil {
			return CallerLookupResult{}, err
		}
		return result, nil
	}

	refs, err := store.LookupCallers(ctx, symbolName)
	if err != nil {
		return CallerLookupResult{}, err
	}
	names := make([]string, 0, len(refs)+1)
	names = append(names, symbolName)
	seen := map[string]struct{}{symbolName: {}}
	for _, ref := range refs {
		if _, ok := seen[ref.CallerName]; ok {
			continue
		}
		seen[ref.CallerName] = struct{}{}
		names = append(names, ref.CallerName)
	}
	symbols, err := store.LookupSymbolsBatch(ctx, names)
	if err != nil {
		return CallerLookupResult{}, err
	}
	return CallerLookupResult{Symbols: symbols, References: refs}, nil
}

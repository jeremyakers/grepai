package mcp

import (
	"context"
	"log"

	"github.com/yoanbernabeu/grepai/trace"
)

// resolveCalleeSymbol resolves a callee reference to its definition,
// preferring the originating store, then a cross-project fallback, then a
// name-only placeholder when no loaded store defines the symbol.
func resolveCalleeSymbol(originByName map[string][]trace.Symbol, crossProject map[string]trace.Symbol, name string) trace.Symbol {
	if defs := originByName[name]; len(defs) > 0 {
		return defs[0]
	}
	if sym, ok := crossProject[name]; ok {
		return sym
	}
	return trace.Symbol{Name: name}
}

// lookupMissingCalleeSymbols resolves callee names that their originating
// store could not define by falling back to the remaining loaded stores in
// deterministic loaded order. Missingness is evaluated per reference origin
// before name dedup, so a name defined by one reference's origin still earns
// a fallback for a later reference whose origin lacks it. Each store receives
// at most one batch query for the still-unresolved names, so the fallback
// adds no per-callee point lookups. Single-store (project mode) resolution
// is unchanged.
func lookupMissingCalleeSymbols(ctx context.Context, stores []trace.SymbolStore, refs []storeReference, origin []map[string][]trace.Symbol) map[string]trace.Symbol {
	resolved := make(map[string]trace.Symbol)
	if len(stores) < 2 {
		return resolved
	}
	missing := make([]string, 0, len(refs))
	seen := make(map[string]struct{}, len(refs))
	for _, item := range refs {
		name := item.ref.SymbolName
		if len(origin[item.storeIndex][name]) > 0 {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		missing = append(missing, name)
	}
	for _, store := range stores {
		remaining := make([]string, 0, len(missing))
		for _, name := range missing {
			if _, ok := resolved[name]; !ok {
				remaining = append(remaining, name)
			}
		}
		if len(remaining) == 0 {
			break
		}
		symbols, err := store.LookupSymbolsBatch(ctx, remaining)
		if err != nil {
			log.Printf("Warning: failed to lookup cross-project callee symbols: %v", err)
			continue
		}
		for _, name := range remaining {
			if defs := symbols[name]; len(defs) > 0 {
				resolved[name] = defs[0]
			}
		}
	}
	return resolved
}

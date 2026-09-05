package trace

type migrationRefKey struct {
	caller string
	callee string
	file   string
	line   int
}

// migrationRefsByFile reconstructs source reference order from the GOB call
// graph in O(totalRefs). GOB stores references grouped by symbol name, while
// CallGraph preserves the original per-file reference insertion order.
func migrationRefsByFile(store *GOBSymbolStore) map[string][]Reference {
	buckets := make(map[migrationRefKey][]Reference)
	remaining := make(map[migrationRefKey]int)
	for _, refs := range store.index.References {
		for _, ref := range refs {
			key := migrationRefKey{caller: ref.CallerName, callee: ref.SymbolName, file: ref.File, line: ref.Line}
			buckets[key] = append(buckets[key], ref)
		}
	}
	result := make(map[string][]Reference, len(store.fileIndex))
	for _, edge := range store.index.CallGraph {
		key := migrationRefKey{caller: edge.Caller, callee: edge.Callee, file: edge.File, line: edge.Line}
		index := remaining[key]
		if refs := buckets[key]; index < len(refs) {
			result[refs[index].File] = append(result[refs[index].File], refs[index])
			remaining[key] = index + 1
		}
	}
	for key, refs := range buckets {
		for _, ref := range refs[remaining[key]:] {
			result[ref.File] = append(result[ref.File], ref)
		}
	}
	return result
}

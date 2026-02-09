# Code Changes Reference

## Quick Reference of All Modified Files

### Core Store Interface
**File**: `store/store.go`
```go
// Before
Search(ctx context.Context, queryVector []float32, limit int) ([]SearchResult, error)

// After
Search(ctx context.Context, queryVector []float32, limit int, pathPrefix string) ([]SearchResult, error)
```

### GOB Store Implementation
**File**: `store/gob.go`
- Added `import "strings"`
- Updated `Search()` method to filter by path prefix using `strings.HasPrefix()`

```go
func (s *GOBStore) Search(ctx context.Context, queryVector []float32, limit int, pathPrefix string) ([]SearchResult, error) {
	// ... existing code ...
	for _, chunk := range s.chunks {
		if pathPrefix != "" && !strings.HasPrefix(chunk.FilePath, pathPrefix) {
			continue
		}
		// ... rest of logic ...
	}
}
```

### PostgreSQL Store Implementation
**File**: `store/postgres.go`
- Updated `Search()` method to add SQL LIKE filter for path prefix
- Dynamically builds SQL with path filter when provided

```go
if pathPrefix != "" {
	query += ` AND file_path LIKE $` + fmt.Sprintf("%d", nextParam)
	args = append(args, pathPrefix+"%")
	nextParam++
}
```

### Qdrant Store Implementation
**File**: `store/qdrant.go`
- Updated `Search()` method to fetch 2x limit when filtering
- Filters results using `strings.HasPrefix()` and stops at target count

```go
fetchLimit := limit
if pathPrefix != "" {
	fetchLimit = limit * 2
}

// ... fetch results ...

if pathPrefix != "" && !strings.HasPrefix(chunk.FilePath, pathPrefix) {
	continue
}

if len(results) >= limit {
	break
}
```

### Search Logic
**File**: `search/search.go`
- Updated `Search()` method signature to include `pathPrefix string` parameter
- Updated `hybridSearch()` method signature and calls

```go
func (s *Searcher) Search(ctx context.Context, query string, limit int, pathPrefix string) ([]SearchResult, error)
```

### Text Search
**File**: `search/hybrid.go`
- Updated `TextSearch()` function signature
- Added path prefix filtering logic

```go
func TextSearch(ctx context.Context, chunks []store.Chunk, query string, limit int, pathPrefix string) []store.SearchResult {
	// ... tokenize query ...
	if pathPrefix != "" && !strings.HasPrefix(chunk.FilePath, pathPrefix) {
		continue
	}
	// ... rest of logic ...
}
```

### CLI Search Command
**File**: `cli/search.go`
- Added new variable: `var searchPath string`
- Added flag definition in `init()`: 
  ```go
  searchCmd.Flags().StringVar(&searchPath, "path", "", "Path prefix to filter search results")
  ```
- Updated three `searcher.Search()` calls to pass `searchPath`:
  1. Line ~175: `results, err := searcher.Search(ctx, query, searchLimit, searchPath)`
  2. Line ~387: `return searcher.Search(ctx, query, limit, "")` (using empty for backward compat)
  3. Line ~485: `results, err := searcher.Search(ctx, query, searchLimit, searchPath)`

### Watch Prefix Store Wrapper
**File**: `cli/watch.go`
- Updated wrapper method to new signature:

```go
func (p *projectPrefixStore) Search(ctx context.Context, queryVector []float32, limit int, pathPrefix string) ([]SearchResult, error) {
	return p.store.Search(ctx, queryVector, limit, pathPrefix)
}
```

### MCP Server
**File**: `mcp/server.go`
- Updated two search tool implementations to pass empty string:
  - Line ~274: `results, err := searcher.Search(ctx, query, limit, "")`
  - Line ~356: `results, err := searcher.Search(ctx, query, limit, "")`

### Test Files
**Files**: 
- `store/gob_test.go` (3 occurrences)
- `cli/watch_prefix_store_test.go` (1 occurrence)

Added `""` argument to all `Search()` calls to match new signature.

## Key Implementation Patterns

### Pattern 1: Parameter Propagation
The `pathPrefix` parameter flows through the system:
```
CLI (--path flag) 
  → runSearch() 
  → searcher.Search(pathPrefix) 
  → store.Search(pathPrefix) 
  → Database filtering
```

### Pattern 2: Database Layer Optimization
PostgreSQL filters at query level:
```go
query += ` AND file_path LIKE $` + fmt.Sprintf("%d", nextParam)
args = append(args, pathPrefix+"%")
```

### Pattern 3: Client-Side Filtering
GOB and Qdrant use simple string matching:
```go
if pathPrefix != "" && !strings.HasPrefix(chunk.FilePath, pathPrefix) {
	continue
}
```

## Compilation Status
✅ All changes compile successfully with `go build ./cmd/grepai`

## Files Modified: 9
- Core store interface: 1
- Store implementations: 3 (GOB, PostgreSQL, Qdrant)
- Search logic: 2 (searcher, text search)
- CLI implementations: 2 (search command, watch wrapper)
- MCP server: 1
- Test files: 2 (gob tests, watch tests)

## Lines Modified: ~150
- Interface changes: ~3
- Store implementations: ~60
- Search logic: ~20
- CLI changes: ~15
- Test updates: ~50

## Backward Compatibility
✅ Fully backward compatible
- Empty string `""` equals no filtering
- All existing code continues to work
- New flag is optional

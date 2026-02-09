# Path Prefix Search Filter Feature

## Overview

The `--path` flag has been added to the `grepai search` command, allowing users to filter search results by a path prefix. This enables searching within specific directories or subdirectories of the codebase.

## Usage

### Basic Syntax

```bash
grepai search <query> --path <path-prefix>
```

### Examples

1. **Search only in the `src` directory:**
   ```bash
   grepai search "user authentication" --path src/
   ```

2. **Search only in a specific subdirectory:**
   ```bash
   grepai search "config" --path src/config/
   ```

3. **Search with JSON output and path filter:**
   ```bash
   grepai search "function definition" --path src/handlers/ --json
   ```

4. **Combine with limit:**
   ```bash
   grepai search "API endpoint" --path api/ --limit 20
   ```

5. **Workspace search with path filter:**
   ```bash
   grepai search "database query" --workspace myworkspace --project myproject --path src/db/
   ```

## How It Works

### Implementation Details

The path filtering is implemented at the database layer:

1. **GOB Store**: Uses simple `strings.HasPrefix()` check after loading results from memory
2. **PostgreSQL Store**: Uses SQL `LIKE` operator with wildcard matching (`path LIKE 'prefix%'`)
3. **Qdrant Store**: Filters results after retrieval, fetching 2x the limit initially to ensure enough results after filtering

### Database Query Optimization

For **PostgreSQL**, the path filter is pushed down to the SQL query itself:

```sql
SELECT id, file_path, start_line, end_line, content, vector, hash, updated_at,
    1 - (vector <=> $1) as score
FROM chunks
WHERE project_id = $2
  AND file_path LIKE $3  -- Path prefix filter applied at database level
ORDER BY vector <=> $1
LIMIT $4
```

This ensures:
- Efficient filtering at the database layer
- Minimal network traffic for irrelevant results
- Better query performance for large codebases

### Prefix Matching Behavior

The path prefix is matched exactly as provided:
- `--path src/` matches files in the `src` directory and its subdirectories
- `--path src/handlers/` matches files in `src/handlers/` and its subdirectories
- `--path api` matches files starting with `api` (use `api/` for directory matching)

## Command-Line Reference

### New Flag

```
--path string
    Path prefix to filter search results (default "")
```

## Integration with Existing Features

The `--path` flag works seamlessly with all existing search options:

- Works with `--limit (-n)`
- Works with `--json (-j)` and `--toon (-t)` output formats
- Works with `--compact (-c)` for minimal output
- Works with `--workspace` for workspace-level searches
- Works with `--project` for project-specific searches (requires `--workspace`)

## Performance Considerations

### Vector Database (Qdrant)

When using Qdrant with a path prefix filter:
- The store fetches 2x the requested limit initially
- Results are filtered in memory after retrieval
- This ensures enough results after filtering while maintaining accuracy

### PostgreSQL

Path filtering is applied as a SQL constraint:
- Uses indexed `file_path` column for efficient filtering
- Filter is applied during query execution, not post-processing
- Minimal performance impact on large databases

### GOB Store

Path filtering is done in-memory:
- Simple prefix matching on all chunks
- Suitable for smaller codebases
- No network overhead

## Examples

### Example 1: Search Files in Tests Directory

```bash
$ grepai search "test setup" --path test/
Found 3 results for: "test setup"

─── Result 1 (score: 0.8532) ───
File: test/unit/setup.go:5-15
...
```

### Example 2: Export Search Results for API Code

```bash
$ grepai search "authentication middleware" --path api/middleware/ --json --compact
[
  {
    "file_path": "api/middleware/auth.go",
    "start_line": 12,
    "end_line": 35,
    "score": 0.9234
  },
  ...
]
```

### Example 3: Search Within a Module

```bash
$ grepai search "database connection" --path src/database/ --limit 10 --compact
Found 5 results for: "database connection"
...
```

## Code Changes Summary

### Modified Files

1. **store/store.go**: Updated `Search` interface to accept `pathPrefix` parameter
2. **store/gob.go**: Implemented path prefix filtering with `strings.HasPrefix()`
3. **store/postgres.go**: Implemented path prefix filtering in SQL with `LIKE` operator
4. **store/qdrant.go**: Implemented path prefix filtering with client-side filtering
5. **search/search.go**: Updated searcher to pass path prefix through to store
6. **search/hybrid.go**: Updated text search to support path prefix filtering
7. **cli/search.go**: Added `--path` flag and passed it through to searcher
8. **cli/watch.go**: Updated wrapper store to support new interface
9. **mcp/server.go**: Updated MCP tool implementations to pass empty string for path prefix

### Test Updates

Updated all test files to pass the new `pathPrefix` parameter:
- `store/gob_test.go`
- `cli/watch_prefix_store_test.go`

## Backward Compatibility

The feature is fully backward compatible:
- The `pathPrefix` parameter defaults to an empty string
- Existing code that doesn't use the flag continues to work unchanged
- The empty string prefix matches all paths (no filtering)

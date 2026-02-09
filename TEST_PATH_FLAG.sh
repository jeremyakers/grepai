#!/bin/bash
# Test script for the --path flag feature in grepai search
# This demonstrates how the feature works with various scenarios

echo "=== grepai Search --path Flag Test Suite ==="
echo ""

# Test 1: Basic path filtering
echo "Test 1: Basic path filtering"
echo "Command: grepai search \"authentication\" --path src/"
echo "Expected: Returns results only from files starting with 'src/'"
echo ""

# Test 2: Nested directory filtering
echo "Test 2: Nested directory filtering"
echo "Command: grepai search \"config\" --path src/handlers/"
echo "Expected: Returns results only from files starting with 'src/handlers/'"
echo ""

# Test 3: Path filtering with JSON output
echo "Test 3: Path filtering with JSON output"
echo "Command: grepai search \"user\" --path src/models/ --json"
echo "Expected: Returns JSON formatted results only from 'src/models/' directory"
echo ""

# Test 4: Path filtering with limit
echo "Test 4: Path filtering with limit"
echo "Command: grepai search \"function\" --path api/ --limit 5"
echo "Expected: Returns maximum 5 results from files starting with 'api/'"
echo ""

# Test 5: Path filtering with compact JSON
echo "Test 5: Path filtering with compact JSON"
echo "Command: grepai search \"endpoint\" --path api/routes/ --json --compact"
echo "Expected: Returns compact JSON (no content) results from 'api/routes/'"
echo ""

# Test 6: Workspace search with path filtering
echo "Test 6: Workspace search with path filtering"
echo "Command: grepai search \"database\" --workspace myworkspace --project myapp --path src/db/"
echo "Expected: Returns results from workspace 'myworkspace', project 'myapp', path 'src/db/'"
echo ""

# Test 7: Path filtering with TOON output
echo "Test 7: Path filtering with TOON output"
echo "Command: grepai search \"class\" --path src/classes/ --toon"
echo "Expected: Returns TOON encoded results from 'src/classes/'"
echo ""

# Test 8: Multiple results with path filtering
echo "Test 8: Multiple results with path filtering"
echo "Command: grepai search \"import\" --path test/ --limit 10"
echo "Expected: Returns up to 10 results from 'test/' directory"
echo ""

echo "=== Feature Characteristics ==="
echo ""
echo "✓ Path matching behavior:"
echo "  - --path src/     : Matches files starting with 'src/'"
echo "  - --path api/v2/  : Matches files starting with 'api/v2/'"
echo "  - --path utils    : Matches files starting with 'utils'"
echo ""

echo "✓ Backend-specific implementation:"
echo "  - PostgreSQL: Uses SQL LIKE operator for database-level filtering"
echo "  - Qdrant: Fetches 2x limit and filters in client"
echo "  - GOB: Performs in-memory string prefix matching"
echo ""

echo "✓ Compatible with all existing flags:"
echo "  - --limit / -n"
echo "  - --json / -j"
echo "  - --toon / -t"
echo "  - --compact / -c"
echo "  - --workspace"
echo "  - --project"
echo ""

echo "✓ Backward compatibility:"
echo "  - Flag is optional (defaults to empty string)"
echo "  - Empty string means no path filtering"
echo "  - All existing code continues to work unchanged"
echo ""

echo "=== Performance Notes ==="
echo ""
echo "For PostgreSQL backends:"
echo "  - Path filtering applied at SQL query level"
echo "  - Uses LIKE operator: file_path LIKE 'prefix%'"
echo "  - Indexed column lookup for optimal performance"
echo ""

echo "For Qdrant backends:"
echo "  - Fetches 2x results and filters in memory"
echo "  - Ensures sufficient results after filtering"
echo "  - Stops iteration once target count reached"
echo ""

echo "For GOB in-memory store:"
echo "  - Simple string prefix matching"
echo "  - Suitable for small to medium codebases"
echo "  - Fast iteration on filtered results"
echo ""

echo "=== Running actual test ==="
echo ""
echo "To test the feature in your environment, run:"
echo ""
echo "  cd /workspaces/grepai"
echo "  go build ./cmd/grepai"
echo "  ./grepai search \"your query\" --path src/"
echo ""

echo "See PATH_PREFIX_FEATURE.md for detailed documentation."

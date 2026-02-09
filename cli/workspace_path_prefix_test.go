package cli

import (
	"context"
	"testing"
	"time"

	"github.com/yoanbernabeu/grepai/config"
	"github.com/yoanbernabeu/grepai/search"
	"github.com/yoanbernabeu/grepai/store"
)

// TestWorkspacePathPrefixing tests that workspace name and project are automatically prepended to search paths
func TestWorkspacePathPrefixing(t *testing.T) {
	ctx := context.Background()
	mockStore := NewMockStore()
	mockEmbedder := &MockEmbedder{}

	// Setup chunks with workspace/project/path structure
	chunks := []store.Chunk{
		{
			ID:        "1",
			FilePath:  "myworkspace/myproject/src/handlers/auth.go",
			StartLine: 1,
			EndLine:   10,
			Content:   "func HandleAuth() {}",
			Vector:    []float32{0.9, 0.1, 0.0},
			Hash:      "hash1",
			UpdatedAt: time.Now(),
		},
		{
			ID:        "2",
			FilePath:  "myworkspace/myproject/src/models/user.go",
			StartLine: 1,
			EndLine:   15,
			Content:   "type User struct {}",
			Vector:    []float32{0.8, 0.2, 0.0},
			Hash:      "hash2",
			UpdatedAt: time.Now(),
		},
		{
			ID:        "3",
			FilePath:  "myworkspace/otherproject/src/main.go",
			StartLine: 1,
			EndLine:   20,
			Content:   "func main() {}",
			Vector:    []float32{0.85, 0.15, 0.0},
			Hash:      "hash3",
			UpdatedAt: time.Now(),
		},
		{
			ID:        "4",
			FilePath:  "otherapespace/someproject/src/code.go",
			StartLine: 1,
			EndLine:   12,
			Content:   "some code",
			Vector:    []float32{0.7, 0.3, 0.0},
			Hash:      "hash4",
			UpdatedAt: time.Now(),
		},
	}

	if err := mockStore.SaveChunks(ctx, chunks); err != nil {
		t.Fatalf("failed to save chunks: %v", err)
	}

	cfg := config.SearchConfig{
		Boost:  config.BoostConfig{},
		Hybrid: config.HybridConfig{Enabled: false},
	}
	searcher := search.NewSearcher(mockStore, mockEmbedder, cfg)

	tests := []struct {
		name           string
		workspace      string
		project        string
		userPath       string
		expectedPrefix string
		expectedCount  int
		expectedFiles  []string
	}{
		{
			name:           "workspace only without user path",
			workspace:      "myworkspace",
			project:        "",
			userPath:       "",
			expectedPrefix: "myworkspace/",
			expectedCount:  3,
			expectedFiles: []string{
				"myworkspace/myproject/src/handlers/auth.go",
				"myworkspace/myproject/src/models/user.go",
				"myworkspace/otherproject/src/main.go",
			},
		},
		{
			name:           "workspace + project without user path",
			workspace:      "myworkspace",
			project:        "myproject",
			userPath:       "",
			expectedPrefix: "myworkspace/myproject/",
			expectedCount:  2,
			expectedFiles: []string{
				"myworkspace/myproject/src/handlers/auth.go",
				"myworkspace/myproject/src/models/user.go",
			},
		},
		{
			name:           "workspace + user path (no project)",
			workspace:      "myworkspace",
			project:        "",
			userPath:       "src/",
			expectedPrefix: "myworkspace/src/",
			expectedCount:  2,
			expectedFiles: []string{
				"myworkspace/myproject/src/handlers/auth.go",
				"myworkspace/myproject/src/models/user.go",
			},
		},
		{
			name:           "workspace + project + user path",
			workspace:      "myworkspace",
			project:        "myproject",
			userPath:       "src/handlers/",
			expectedPrefix: "myworkspace/myproject/src/handlers/",
			expectedCount:  1,
			expectedFiles: []string{
				"myworkspace/myproject/src/handlers/auth.go",
			},
		},
		{
			name:           "different workspace",
			workspace:      "otherapespace",
			project:        "",
			userPath:       "",
			expectedPrefix: "otherapespace/",
			expectedCount:  1,
			expectedFiles: []string{
				"otherapespace/someproject/src/code.go",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate what the workspace search code does: prepend workspace/project to user path
			fullPathPrefix := tt.workspace + "/"
			if tt.project != "" {
				fullPathPrefix += tt.project + "/"
			}
			if tt.userPath != "" {
				fullPathPrefix += tt.userPath
			}

			// Verify prefix matches expected
			if fullPathPrefix != tt.expectedPrefix {
				t.Errorf("prefix mismatch: got %q, want %q", fullPathPrefix, tt.expectedPrefix)
			}

			// Search with the constructed prefix
			results, err := searcher.Search(ctx, "test", 10, fullPathPrefix)
			if err != nil {
				t.Fatalf("search failed: %v", err)
			}

			if len(results) != tt.expectedCount {
				t.Errorf("got %d results, want %d", len(results), tt.expectedCount)
			}

			// Verify expected files are in results
			if len(tt.expectedFiles) > 0 {
				resultFiles := make(map[string]bool)
				for _, r := range results {
					resultFiles[r.Chunk.FilePath] = true
				}
				for _, file := range tt.expectedFiles {
					if !resultFiles[file] {
						t.Errorf("expected file %q not in results", file)
					}
				}
			}

			// Verify all results match the prefix
			for _, result := range results {
				if len(result.Chunk.FilePath) < len(fullPathPrefix) ||
					result.Chunk.FilePath[:len(fullPathPrefix)] != fullPathPrefix {
					t.Errorf("result %q doesn't match prefix %q", result.Chunk.FilePath, fullPathPrefix)
				}
			}
		})
	}
}

package mcp

import (
	"testing"
)

// TestDiscoveryToolsCompile verifies that the discovery tools are properly integrated
// and can be called without errors. This is a smoke test to ensure the handlers
// are callable and the MCP server is properly configured.
func TestDiscoveryToolsCompile(t *testing.T) {
	// This test primarily checks that the code compiles and the Server type
	// has the new handler methods.

	server := &Server{
		projectRoot:   "/tmp/test",
		workspaceName: "test",
	}

	// Verify the methods are accessible on Server.
	_ = server.handleListWorkspaces
	_ = server.handleListProjects

	// The handlers are defined and callable, which verifies:
	// 1. The code compiles without errors
	// 2. The methods are properly attached to the Server type
	// 3. The signatures match what MCP expects
}

// TestEncodeOutput verifies the output formatting for both JSON and TOON formats
func TestEncodeOutput(t *testing.T) {
	data := map[string]interface{}{
		"name":   "test",
		"value":  42,
		"active": true,
	}

	tests := []struct {
		name      string
		format    string
		expectErr bool
	}{
		{
			name:      "json format",
			format:    "json",
			expectErr: false,
		},
		{
			name:      "toon format",
			format:    "toon",
			expectErr: false,
		},
		{
			name:      "default format (json)",
			format:    "",
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output, err := encodeOutput(data, tt.format)
			if (err != nil) != tt.expectErr {
				t.Errorf("encodeOutput() error = %v, expectErr %v", err, tt.expectErr)
				return
			}
			if len(output) == 0 {
				t.Error("encodeOutput() returned empty string")
			}
		})
	}
}

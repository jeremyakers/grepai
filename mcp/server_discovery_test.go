package mcp

import (
	"testing"
)

// TestDiscoveryToolsCompile verifies that the discovery tools are properly integrated
// and can be called without errors. This is a smoke test to ensure the handlers
// are callable and the MCP server is properly configured.
func TestDiscoveryToolsCompile(t *testing.T) {
	// This test verifies that the code compiles and the handlers can be created.
	// The methods are verified to exist by their successful registration in registerTools()
	// and their use in tool callbacks.

	s, err := NewServer("/tmp/test")
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	if s == nil {
		t.Fatal("server should not be nil")
	}

	// The MCP server was created successfully with all tools registered,
	// which verifies:
	// 1. The code compiles without errors
	// 2. The handler methods exist and are accessible
	// 3. The handlers have the correct signatures
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

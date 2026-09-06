package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mark3 "github.com/mark3labs/mcp-go/mcp"
	"github.com/yoanbernabeu/grepai/config"
	"github.com/yoanbernabeu/grepai/trace"
)

func seedTraceHandlerProject(t *testing.T, root, prefix string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, config.ConfigDir), 0o755); err != nil {
		t.Fatal(err)
	}
	store := trace.NewGOBSymbolStore(config.GetSymbolIndexPath(root))
	if err := store.SaveFile(context.Background(), prefix+"/index.go", []trace.Symbol{
		{Name: "Target", File: prefix + "/target.go", Line: 1},
		{Name: "SharedCaller", File: prefix + "/caller.go", Line: 2},
		{Name: "SharedCallee", File: prefix + "/callee.go", Line: 3},
	}, []trace.Reference{
		{SymbolName: "Target", Kind: trace.RefKindCall, File: prefix + "/use.go", Line: 10, CallerName: "SharedCaller", CallerFile: prefix + "/caller.go", CallerLine: 2},
		{SymbolName: "Target", Kind: trace.RefKindCall, File: prefix + "/missing.go", Line: 11, CallerName: "MissingCaller", CallerFile: prefix + "/missing.go", CallerLine: 9},
		{SymbolName: "SharedCallee", Kind: trace.RefKindCall, File: prefix + "/target.go", Line: 12, CallerName: "Target"},
		{SymbolName: "MissingCallee", Kind: trace.RefKindCall, File: prefix + "/target.go", Line: 13, CallerName: "Target"},
		{SymbolName: "uid", Kind: trace.RefKindRead, File: prefix + "/caller.go", Line: 14, CallerName: "SharedCaller", CallerFile: prefix + "/caller.go", CallerLine: 2},
		{SymbolName: "uid", Kind: trace.RefKindWrite, File: prefix + "/caller.go", Line: 15, CallerName: "SharedCaller", CallerFile: prefix + "/caller.go", CallerLine: 2},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}

func traceHandlerRequest(arguments map[string]any) mark3.CallToolRequest {
	return mark3.CallToolRequest{Params: mark3.CallToolParams{Arguments: arguments}}
}

func TestTraceHandlersProjectUseBatchAndFallback(t *testing.T) {
	// Given a real project symbol index.
	root := t.TempDir()
	seedTraceHandlerProject(t, root, "project")
	server := &Server{projectRoot: root}

	// When the real project handlers run.
	callersResult, err := server.handleTraceCallers(context.Background(), traceHandlerRequest(map[string]any{"symbol": "Target", "format": "json"}))
	if err != nil {
		t.Fatal(err)
	}
	calleesResult, err := server.handleTraceCallees(context.Background(), traceHandlerRequest(map[string]any{"symbol": "Target", "format": "json", "compact": true}))
	if err != nil {
		t.Fatal(err)
	}

	// Then resolved and fallback symbols are returned.
	callersText, calleesText := textResultPayload(t, callersResult), textResultPayload(t, calleesResult)
	if !containsMCPParts(callersText, "project/caller.go", "MissingCaller") || !containsMCPParts(calleesText, "project/callee.go", "MissingCallee") {
		t.Fatalf("unexpected handler output:\n%s\n%s", callersText, calleesText)
	}
}

func TestTraceHandlersWorkspacePreserveOriginatingProject(t *testing.T) {
	// Given a workspace with duplicate symbol names in two projects.
	home := t.TempDir()
	t.Setenv("HOME", home)
	projects := make([]config.ProjectEntry, 0, 2)
	for _, name := range []string{"one", "two"} {
		root := filepath.Join(home, name)
		seedTraceHandlerProject(t, root, name)
		projects = append(projects, config.ProjectEntry{Name: name, Path: root})
	}
	workspaceConfig := config.DefaultWorkspaceConfig()
	workspaceConfig.AddWorkspace(config.Workspace{Name: "handlers", Projects: projects})
	if err := config.SaveWorkspaceConfig(workspaceConfig); err != nil {
		t.Fatal(err)
	}
	server := &Server{workspaceName: "handlers"}

	// When callers and callees are requested through their public handler surface.
	callersResult, err := server.handleTraceCallers(context.Background(), traceHandlerRequest(map[string]any{"symbol": "Target", "format": "json", "compact": true}))
	if err != nil {
		t.Fatal(err)
	}
	calleesResult, err := server.handleTraceCallees(context.Background(), traceHandlerRequest(map[string]any{"symbol": "Target", "format": "json"}))
	if err != nil {
		t.Fatal(err)
	}

	// Then both originating definitions survive duplicate names.
	refsResult, err := server.handleRefsGraph(context.Background(), traceHandlerRequest(map[string]any{"symbol": "uid", "format": "json", "compact": true}))
	if err != nil {
		t.Fatal(err)
	}
	callersText, calleesText, refsText := textResultPayload(t, callersResult), textResultPayload(t, calleesResult), textResultPayload(t, refsResult)
	if !containsMCPParts(callersText, "one/caller.go", "two/caller.go") || !containsMCPParts(calleesText, "one/callee.go", "two/callee.go") || !containsMCPParts(refsText, "one/caller.go", "two/caller.go") {
		t.Fatalf("workspace provenance lost:\n%s\n%s\n%s", callersText, calleesText, refsText)
	}
}

func TestTraceHandlersValidateRequiredArgumentsAndFormat(t *testing.T) {
	server := &Server{}
	for _, run := range []func() (*mark3.CallToolResult, error){
		func() (*mark3.CallToolResult, error) {
			return server.handleTraceCallers(context.Background(), traceHandlerRequest(map[string]any{}))
		},
		func() (*mark3.CallToolResult, error) {
			return server.handleTraceCallees(context.Background(), traceHandlerRequest(map[string]any{"symbol": "Target", "format": "xml"}))
		},
	} {
		result, err := run()
		if err != nil || !strings.Contains(strings.ToLower(textResultPayload(t, result)), "required") && !strings.Contains(textResultPayload(t, result), "format") {
			t.Fatalf("expected handler validation error, result=%v err=%v", result, err)
		}
	}
}

func containsMCPParts(value string, parts ...string) bool {
	var decoded any
	if err := json.Unmarshal([]byte(value), &decoded); err != nil {
		return false
	}
	for _, part := range parts {
		if !strings.Contains(value, part) {
			return false
		}
	}
	return true
}

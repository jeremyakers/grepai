package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/yoanbernabeu/grepai/config"
)

func TestIndexStatusHandlerProjectReportsSymbolReadiness(t *testing.T) {
	// Given a configured project with a real GOB symbol index.
	root := t.TempDir()
	cfg := config.DefaultConfig()
	if err := cfg.Save(root); err != nil {
		t.Fatal(err)
	}
	seedTraceHandlerProject(t, root, "project")
	server := &Server{projectRoot: root}

	// When the actual status handler runs.
	result, err := server.handleIndexStatus(context.Background(), traceHandlerRequest(map[string]any{"format": "json"}))
	if err != nil {
		t.Fatal(err)
	}

	// Then it observes the loaded symbol store before closing it.
	var status IndexStatus
	if err := json.Unmarshal([]byte(textResultPayload(t, result)), &status); err != nil {
		t.Fatal(err)
	}
	if !status.SymbolsReady || status.Provider != cfg.Embedder.Provider {
		t.Fatalf("status = %#v", status)
	}
}

func TestIndexStatusHandlerWorkspaceReportsEachProject(t *testing.T) {
	// Given an isolated workspace with two configured symbol projects.
	home := isolateMCPTestHome(t)
	projects := make([]config.ProjectEntry, 0, 2)
	for _, name := range []string{"one", "two"} {
		root := filepath.Join(home, name)
		cfg := config.DefaultConfig()
		if err := cfg.Save(root); err != nil {
			t.Fatal(err)
		}
		seedTraceHandlerProject(t, root, name)
		projects = append(projects, config.ProjectEntry{Name: name, Path: root})
	}
	workspaceConfig := config.DefaultWorkspaceConfig()
	workspaceConfig.AddWorkspace(config.Workspace{Name: "status", Projects: projects})
	if err := config.SaveWorkspaceConfig(workspaceConfig); err != nil {
		t.Fatal(err)
	}
	server := &Server{workspaceName: "status"}

	// When workspace status runs through the registered handler implementation.
	result, err := server.handleIndexStatus(context.Background(), traceHandlerRequest(map[string]any{"format": "json"}))
	if err != nil {
		t.Fatal(err)
	}

	// Then both projects report their independently loaded symbol counts.
	var status WorkspaceIndexStatus
	if err := json.Unmarshal([]byte(textResultPayload(t, result)), &status); err != nil {
		t.Fatal(err)
	}
	if len(status.Projects) != 2 || !status.Projects[0].SymbolsReady || !status.Projects[1].SymbolsReady {
		t.Fatalf("workspace status = %#v", status)
	}
}

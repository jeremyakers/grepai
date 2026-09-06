package rpg

import (
	"context"
	"encoding/gob"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yoanbernabeu/grepai/trace"
)

func TestGOBRPGStore_PersistLoad(t *testing.T) {
	// Create temp directory for test
	tmpDir := t.TempDir()
	indexPath := filepath.Join(tmpDir, "rpg.gob")

	// Create store and add some data
	store := NewGOBRPGStore(indexPath)
	graph := store.mutableGraph()

	// Add nodes
	node1 := &Node{
		ID:         "sym1",
		Kind:       KindSymbol,
		Feature:    "handle-request",
		SymbolName: "HandleRequest",
		Path:       "server.go",
		StartLine:  10,
		EndLine:    20,
		UpdatedAt:  time.Now(),
	}
	node2 := &Node{
		ID:        "area:cli",
		Kind:      KindArea,
		Feature:   "cli",
		UpdatedAt: time.Now(),
	}

	graph.AddNode(node1)
	graph.AddNode(node2)

	// Add edge
	edge := &Edge{
		From:      node1.ID,
		To:        node2.ID,
		Type:      EdgeFeatureParent,
		Weight:    1.0,
		UpdatedAt: time.Now(),
	}
	graph.AddEdge(edge)

	// Persist
	err := store.Persist(context.Background())
	if err != nil {
		t.Fatalf("Persist failed: %v", err)
	}

	// Verify file was created
	if _, err := os.Stat(indexPath); os.IsNotExist(err) {
		t.Fatal("Index file was not created")
	}
	file, err := os.Open(indexPath)
	if err != nil {
		t.Fatalf("failed to open persisted file: %v", err)
	}
	var persisted gobRPGData
	if err := gob.NewDecoder(file).Decode(&persisted); err != nil {
		file.Close()
		t.Fatalf("failed to decode persisted payload: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("failed to close persisted file: %v", err)
	}
	if persisted.Version != CurrentRPGIndexVersion {
		t.Fatalf("expected persisted version %d, got %d", CurrentRPGIndexVersion, persisted.Version)
	}

	// Create new store and load
	store2 := NewGOBRPGStore(indexPath)
	err = store2.Load(context.Background())
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	graph2 := store2.GetGraph()

	// Verify nodes were loaded
	if len(graph2.Nodes) != 2 {
		t.Errorf("Expected 2 nodes, got %d", len(graph2.Nodes))
	}

	loadedNode1 := graph2.GetNode(node1.ID)
	if loadedNode1 == nil {
		t.Fatal("Node 'sym1' not loaded")
	}
	if loadedNode1.Kind != KindSymbol {
		t.Errorf("Wrong kind for sym1: %s", loadedNode1.Kind)
	}
	if loadedNode1.Feature != "handle-request" {
		t.Errorf("Wrong feature for sym1: %s", loadedNode1.Feature)
	}
	if loadedNode1.SymbolName != "HandleRequest" {
		t.Errorf("Wrong symbol name for sym1: %s", loadedNode1.SymbolName)
	}
	if loadedNode1.Path != "server.go" {
		t.Errorf("Wrong path for sym1: %s", loadedNode1.Path)
	}

	loadedNode2 := graph2.GetNode(node2.ID)
	if loadedNode2 == nil {
		t.Fatal("Node 'area:cli' not loaded")
	}

	// Verify edges were loaded
	if len(graph2.Edges) != 1 {
		t.Errorf("Expected 1 edge, got %d", len(graph2.Edges))
	}

	// Verify indexes were rebuilt
	symbolNodes := graph2.GetNodesByKind(KindSymbol)
	if len(symbolNodes) != 1 {
		t.Errorf("Expected 1 symbol in byKind index, got %d", len(symbolNodes))
	}

	areaNodes := graph2.GetNodesByKind(KindArea)
	if len(areaNodes) != 1 {
		t.Errorf("Expected 1 area in byKind index, got %d", len(areaNodes))
	}

	fileNodes := graph2.GetNodesByFile("server.go")
	if len(fileNodes) != 1 {
		t.Errorf("Expected 1 node for server.go in byFile index, got %d", len(fileNodes))
	}

	// Verify adjacency indexes
	outgoing := graph2.GetOutgoing(node1.ID)
	if len(outgoing) != 1 {
		t.Errorf("Expected 1 outgoing edge from sym1, got %d", len(outgoing))
	}

	incoming := graph2.GetIncoming(node2.ID)
	if len(incoming) != 1 {
		t.Errorf("Expected 1 incoming edge to area:cli, got %d", len(incoming))
	}
}

func TestGOBRPGStore_EmptyLoad(t *testing.T) {
	tmpDir := t.TempDir()
	indexPath := filepath.Join(tmpDir, "nonexistent.gob")

	store := NewGOBRPGStore(indexPath)

	// Load should succeed even if file doesn't exist
	err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load on non-existent file should succeed, got error: %v", err)
	}

	graph := store.GetGraph()

	// Graph should be empty but initialized
	if graph.Nodes == nil {
		t.Error("Nodes map should be initialized")
	}
	if len(graph.Nodes) != 0 {
		t.Errorf("Expected 0 nodes in empty graph, got %d", len(graph.Nodes))
	}

	if graph.Edges == nil {
		t.Error("Edges slice should be initialized")
	}
	if len(graph.Edges) != 0 {
		t.Errorf("Expected 0 edges in empty graph, got %d", len(graph.Edges))
	}
}

func TestGOBRPGStore_UntouchedMissingReaderCloseWritesNothing(t *testing.T) {
	indexPath := filepath.Join(t.TempDir(), "rpg.gob")
	store := NewGOBRPGStore(indexPath)
	if err := store.Load(context.Background()); err != nil {
		t.Fatalf("Load missing index failed: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if _, err := os.Stat(indexPath); !os.IsNotExist(err) {
		t.Fatalf("untouched missing-index reader wrote %s: %v", indexPath, err)
	}
}

func TestGOBRPGStore_MissingLoadPreservesPendingMutation(t *testing.T) {
	indexPath := filepath.Join(t.TempDir(), "rpg.gob")
	store := NewGOBRPGStore(indexPath)
	store.AddNode(&Node{ID: "pending", Kind: KindSymbol})
	if err := store.Load(context.Background()); err != nil {
		t.Fatalf("Load missing index failed: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	reloaded := NewGOBRPGStore(indexPath)
	if err := reloaded.Load(context.Background()); err != nil {
		t.Fatalf("reload failed: %v", err)
	}
	if reloaded.GetGraph().GetNode("pending") == nil {
		t.Fatal("pending graph mutation was lost")
	}
}

func TestGOBRPGStore_MissingReaderCannotOverwriteLaterWriter(t *testing.T) {
	indexPath := filepath.Join(t.TempDir(), "rpg.gob")
	ctx := context.Background()
	reader := NewGOBRPGStore(indexPath)
	if err := reader.Load(ctx); err != nil {
		t.Fatalf("reader Load failed: %v", err)
	}
	writer := NewGOBRPGStore(indexPath)
	writer.AddNode(&Node{ID: "writer", Kind: KindSymbol})
	if err := writer.Close(); err != nil {
		t.Fatalf("writer Close failed: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("reader Close failed: %v", err)
	}
	check := NewGOBRPGStore(indexPath)
	if err := check.Load(ctx); err != nil {
		t.Fatalf("check Load failed: %v", err)
	}
	if check.GetGraph().GetNode("writer") == nil {
		t.Fatal("reader Close overwrote writer graph")
	}
}

func TestGOBRPGStore_DirtyPersistWritesOnceAndCleanCloseIsNoOp(t *testing.T) {
	indexPath := filepath.Join(t.TempDir(), "rpg.gob")
	ctx := context.Background()
	store := NewGOBRPGStore(indexPath)
	if err := store.Persist(ctx); err != nil {
		t.Fatalf("initial Persist failed: %v", err)
	}
	oldTime := time.Unix(1, 0)
	if err := os.Chtimes(indexPath, oldTime, oldTime); err != nil {
		t.Fatalf("Chtimes failed: %v", err)
	}
	store.AddNode(&Node{ID: "dirty", Kind: KindSymbol})
	if err := store.Persist(ctx); err != nil {
		t.Fatalf("dirty Persist failed: %v", err)
	}
	info, err := os.Stat(indexPath)
	if err != nil || info.ModTime().Equal(oldTime) {
		t.Fatalf("dirty Persist did not rewrite index: info=%v err=%v", info, err)
	}
	if err := os.Chtimes(indexPath, oldTime, oldTime); err != nil {
		t.Fatalf("second Chtimes failed: %v", err)
	}
	if err := store.Persist(ctx); err != nil {
		t.Fatalf("clean Persist failed: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("clean Close failed: %v", err)
	}
	info, err = os.Stat(indexPath)
	if err != nil || !info.ModTime().Equal(oldTime) {
		t.Fatalf("clean Persist/Close rewrote index: info=%v err=%v", info, err)
	}
}

func TestGOBRPGStore_ValidLoadMakesReaderCloseClean(t *testing.T) {
	indexPath := filepath.Join(t.TempDir(), "rpg.gob")
	ctx := context.Background()
	seed := NewGOBRPGStore(indexPath)
	seed.AddNode(&Node{ID: "seed", Kind: KindSymbol})
	if err := seed.Persist(ctx); err != nil {
		t.Fatalf("seed Persist failed: %v", err)
	}
	reader := NewGOBRPGStore(indexPath)
	if err := reader.Load(ctx); err != nil {
		t.Fatalf("reader Load failed: %v", err)
	}
	oldTime := time.Unix(1, 0)
	if err := os.Chtimes(indexPath, oldTime, oldTime); err != nil {
		t.Fatalf("Chtimes failed: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("reader Close failed: %v", err)
	}
	info, err := os.Stat(indexPath)
	if err != nil || !info.ModTime().Equal(oldTime) {
		t.Fatalf("clean loaded reader rewrote index: info=%v err=%v", info, err)
	}
}

func TestGOBRPGStore_FailedPersistRemainsDirtyForRetry(t *testing.T) {
	tmpDir := t.TempDir()
	indexPath := filepath.Join(tmpDir, "rpg.gob")
	store := NewGOBRPGStore(indexPath)
	store.AddNode(&Node{ID: "retry", Kind: KindSymbol})
	if err := os.Mkdir(indexPath, 0o755); err != nil {
		t.Fatalf("Mkdir failed: %v", err)
	}
	blocker := filepath.Join(indexPath, "blocker")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	if err := store.Persist(context.Background()); err == nil {
		t.Fatal("Persist should fail when target is a non-empty directory")
	}
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(indexPath); err != nil {
		t.Fatal(err)
	}
	if err := store.Persist(context.Background()); err != nil {
		t.Fatalf("retry Persist failed: %v", err)
	}
	loaded := NewGOBRPGStore(indexPath)
	if err := loaded.Load(context.Background()); err != nil || loaded.GetGraph().GetNode("retry") == nil {
		t.Fatalf("retry mutation not persisted: err=%v", err)
	}
}

func TestGOBRPGStore_EveryGraphMutationMarksDirty(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Graph)
	}{
		{"AddNode", func(g *Graph) { g.AddNode(&Node{ID: "added", Kind: KindSymbol}) }},
		{"UpdateNode", func(g *Graph) { g.UpdateNode("base-a", func(n *Node) { n.Summary = "updated" }) }},
		{"RemoveNode", func(g *Graph) { g.RemoveNode("base-a") }},
		{"AddEdge", func(g *Graph) { g.AddEdge(&Edge{From: "base-a", To: "base-b", Type: EdgeInvokes}) }},
		{"RemoveEdgesBetween", func(g *Graph) { g.RemoveEdgesBetween("base-a", "base-b") }},
		{"RemoveEdgesBetweenOfType", func(g *Graph) { g.RemoveEdgesBetweenOfType("base-a", "base-b", EdgeContains) }},
		{"RemoveEdgesIf", func(g *Graph) { g.RemoveEdgesIf(func(*Edge) bool { return true }) }},
		{"Reset", func(g *Graph) { g.Reset() }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			indexPath := filepath.Join(t.TempDir(), "rpg.gob")
			ctx := context.Background()
			store := NewGOBRPGStore(indexPath)
			graph := store.mutableGraph()
			graph.AddNode(&Node{ID: "base-a", Kind: KindSymbol})
			graph.AddNode(&Node{ID: "base-b", Kind: KindSymbol})
			graph.AddEdge(&Edge{From: "base-a", To: "base-b", Type: EdgeContains})
			if err := store.Persist(ctx); err != nil {
				t.Fatalf("seed Persist failed: %v", err)
			}
			tt.mutate(graph)
			if !store.hasPendingPersist() {
				t.Fatalf("%s did not mark store dirty", tt.name)
			}
		})
	}
}

func TestGOBRPGStoreOwnsInputsAndReturnsGraphSnapshot(t *testing.T) {
	indexPath := filepath.Join(t.TempDir(), "rpg.gob")
	ctx := context.Background()
	store := NewGOBRPGStore(indexPath)
	node := &Node{ID: "node", Kind: KindSymbol, Feature: "original", Features: []string{"one"}}
	edge := &Edge{From: "node", To: "node", Type: EdgeInvokes, Weight: 1}
	store.AddNode(node)
	store.AddEdge(edge)
	if err := store.Persist(ctx); err != nil {
		t.Fatal(err)
	}
	node.Feature = "input-mutated"
	node.Features[0] = "input-mutated"
	edge.Weight = 99
	snapshot := store.GetGraph()
	snapshot.Nodes["node"].Feature = "snapshot-mutated"
	snapshot.Nodes["node"].Features[0] = "snapshot-mutated"
	snapshot.Edges[0].Weight = 88
	got := store.GetGraph().GetNode("node")
	got.Feature = "getter-mutated"
	got.Features[0] = "getter-mutated"
	gotEdges := store.GetGraph().GetOutgoing("node")
	gotEdges[0].Weight = 77
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reloaded := NewGOBRPGStore(indexPath)
	if err := reloaded.Load(ctx); err != nil {
		t.Fatal(err)
	}
	got = reloaded.GetGraph().GetNode("node")
	gotEdges = reloaded.GetGraph().GetOutgoing("node")
	if got.Feature != "original" || len(got.Features) != 1 || got.Features[0] != "one" || len(gotEdges) != 1 || gotEdges[0].Weight != 1 {
		t.Fatalf("persisted graph changed through alias: node=%#v edges=%#v", got, gotEdges)
	}
}

func TestGOBRPGStoreConcurrentPersistAndTrackedMutations(t *testing.T) {
	indexPath := filepath.Join(t.TempDir(), "rpg.gob")
	ctx := context.Background()
	store := NewGOBRPGStore(indexPath)
	encoder := NewRPGEncoder(store, NewLocalExtractor(), ".", RPGEncoderConfig{DriftThreshold: 0.3})
	start := make(chan struct{})
	done := make(chan error, 2)
	go func() {
		<-start
		for i := 0; i < 50; i++ {
			if err := store.Persist(ctx); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	go func() {
		<-start
		for i := 0; i < 50; i++ {
			if err := encoder.HandleFileEvent(ctx, "modify", "main.go", []trace.Symbol{{Name: "Run", File: "main.go", Kind: trace.KindFunction}}); err != nil {
				done <- err
				return
			}
			encoder.hierarchy.BuildHierarchy()
			encoder.hierarchy.EnrichLabels()
			if err := NewSummarizer(encoder.graph, NewLocalExtractor()).SummarizeHierarchy(ctx, true); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	close(start)
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatalf("concurrent operation failed: %v", err)
		}
	}
	if err := store.Persist(ctx); err != nil {
		t.Fatal(err)
	}
	reloaded := NewGOBRPGStore(indexPath)
	if err := reloaded.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if reloaded.GetGraph().GetNode("file:main.go") == nil || reloaded.GetGraph().GetNode("sym:main.go:Run") == nil {
		t.Fatal("tracked concurrent mutations did not survive reload")
	}
}

func TestGOBRPGStore_GetGraph(t *testing.T) {
	tmpDir := t.TempDir()
	indexPath := filepath.Join(tmpDir, "rpg.gob")

	store := NewGOBRPGStore(indexPath)
	graph := store.mutableGraph()

	if graph == nil {
		t.Fatal("GetGraph should return non-nil graph")
	}

	// Add a node
	node := &Node{
		ID:        "test",
		Kind:      KindSymbol,
		UpdatedAt: time.Now(),
	}
	graph.AddNode(node)

	// GetGraph returns a detached snapshot.
	graph2 := store.GetGraph()
	if graph2.GetNode("test") == nil {
		t.Error("GetGraph snapshot should contain stored nodes")
	}
	graph2.RemoveNode("test")
	if store.GetGraph().GetNode("test") == nil {
		t.Error("mutating GetGraph snapshot changed the store")
	}
}

func TestGOBRPGStore_GetStats(t *testing.T) {
	tmpDir := t.TempDir()
	indexPath := filepath.Join(tmpDir, "rpg.gob")

	store := NewGOBRPGStore(indexPath)
	graph := store.mutableGraph()

	// Add some nodes and edges
	node1 := &Node{ID: "node1", Kind: KindSymbol, UpdatedAt: time.Now()}
	node2 := &Node{ID: "node2", Kind: KindSymbol, UpdatedAt: time.Now()}
	node3 := &Node{ID: "node3", Kind: KindFile, UpdatedAt: time.Now()}

	graph.AddNode(node1)
	graph.AddNode(node2)
	graph.AddNode(node3)

	edge1 := &Edge{From: "node1", To: "node2", Type: EdgeInvokes, UpdatedAt: time.Now()}
	edge2 := &Edge{From: "node3", To: "node1", Type: EdgeContains, UpdatedAt: time.Now()}

	graph.AddEdge(edge1)
	graph.AddEdge(edge2)

	// Get stats
	stats, err := store.GetStats(context.Background())
	if err != nil {
		t.Fatalf("GetStats failed: %v", err)
	}

	if stats.TotalNodes != 3 {
		t.Errorf("Expected 3 total nodes, got %d", stats.TotalNodes)
	}
	if stats.TotalEdges != 2 {
		t.Errorf("Expected 2 total edges, got %d", stats.TotalEdges)
	}

	if stats.NodesByKind[KindSymbol] != 2 {
		t.Errorf("Expected 2 symbol nodes, got %d", stats.NodesByKind[KindSymbol])
	}
	if stats.NodesByKind[KindFile] != 1 {
		t.Errorf("Expected 1 file node, got %d", stats.NodesByKind[KindFile])
	}

	if stats.EdgesByType[EdgeInvokes] != 1 {
		t.Errorf("Expected 1 invokes edge, got %d", stats.EdgesByType[EdgeInvokes])
	}
	if stats.EdgesByType[EdgeContains] != 1 {
		t.Errorf("Expected 1 contains edge, got %d", stats.EdgesByType[EdgeContains])
	}
}

func TestGOBRPGStore_Close(t *testing.T) {
	tmpDir := t.TempDir()
	indexPath := filepath.Join(tmpDir, "rpg.gob")

	store := NewGOBRPGStore(indexPath)
	graph := store.mutableGraph()

	// Add a node
	node := &Node{
		ID:        "test",
		Kind:      KindSymbol,
		UpdatedAt: time.Now(),
	}
	graph.AddNode(node)

	// Close should persist
	err := store.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Verify file was created
	if _, err := os.Stat(indexPath); os.IsNotExist(err) {
		t.Error("Index file should be created on Close")
	}

	// Load in new store to verify
	store2 := NewGOBRPGStore(indexPath)
	err = store2.Load(context.Background())
	if err != nil {
		t.Fatalf("Load after Close failed: %v", err)
	}

	if store2.GetGraph().GetNode("test") == nil {
		t.Error("Node should be persisted on Close")
	}
}

func TestGOBRPGStore_ConcurrentAccess(t *testing.T) {
	tmpDir := t.TempDir()
	indexPath := filepath.Join(tmpDir, "rpg.gob")

	store := NewGOBRPGStore(indexPath)
	graph := store.mutableGraph()

	// Add initial node
	node := &Node{
		ID:        "test",
		Kind:      KindSymbol,
		UpdatedAt: time.Now(),
	}
	graph.AddNode(node)

	// Test concurrent reads
	done := make(chan bool, 2)

	go func() {
		for i := 0; i < 100; i++ {
			_, err := store.GetStats(context.Background())
			if err != nil {
				t.Errorf("Concurrent GetStats failed: %v", err)
			}
		}
		done <- true
	}()

	go func() {
		for i := 0; i < 100; i++ {
			g := store.GetGraph()
			_ = g.GetNode("test")
		}
		done <- true
	}()

	<-done
	<-done
}

func TestGOBRPGStore_LargeGraph(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping large graph test in short mode")
	}

	tmpDir := t.TempDir()
	indexPath := filepath.Join(tmpDir, "rpg.gob")

	store := NewGOBRPGStore(indexPath)
	graph := store.mutableGraph()

	// Add many nodes
	numNodes := 1000
	for i := 0; i < numNodes; i++ {
		node := &Node{
			ID:        string(rune(i)),
			Kind:      KindSymbol,
			Feature:   "test-feature",
			UpdatedAt: time.Now(),
		}
		graph.AddNode(node)
	}

	// Persist
	err := store.Persist(context.Background())
	if err != nil {
		t.Fatalf("Persist large graph failed: %v", err)
	}

	// Load
	store2 := NewGOBRPGStore(indexPath)
	err = store2.Load(context.Background())
	if err != nil {
		t.Fatalf("Load large graph failed: %v", err)
	}

	graph2 := store2.GetGraph()
	if len(graph2.Nodes) != numNodes {
		t.Errorf("Expected %d nodes after load, got %d", numNodes, len(graph2.Nodes))
	}
}

func TestGOBRPGStore_PersistCreatesMissingParentDir(t *testing.T) {
	indexPath := filepath.Join(t.TempDir(), "missing", ".grepai", "rpg.gob")

	store := NewGOBRPGStore(indexPath)
	if err := store.Persist(context.Background()); err != nil {
		t.Fatalf("Persist failed: %v", err)
	}

	if _, err := os.Stat(indexPath); err != nil {
		t.Fatalf("expected persisted rpg index file at %s: %v", indexPath, err)
	}
}

func TestGOBRPGStore_PersistCreatesLockFileAndNoTempFiles(t *testing.T) {
	tmpDir := t.TempDir()
	indexPath := filepath.Join(tmpDir, "rpg.gob")

	store := NewGOBRPGStore(indexPath)
	if err := store.Persist(context.Background()); err != nil {
		t.Fatalf("Persist failed: %v", err)
	}

	lockPath := indexPath + ".lock"
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("expected lock file at %s: %v", lockPath, err)
	}

	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "rpg.gob.tmp-") {
			t.Fatalf("unexpected temporary file left behind: %s", entry.Name())
		}
	}
}

func TestGOBRPGStore_LoadOutdatedVersion(t *testing.T) {
	tmpDir := t.TempDir()
	indexPath := filepath.Join(tmpDir, "rpg.gob")

	file, err := os.Create(indexPath)
	if err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	oldData := gobRPGData{
		Version: 1,
		Nodes: map[string]*Node{
			"node1": {
				ID:        "node1",
				Kind:      KindFile,
				Path:      "server.go",
				UpdatedAt: time.Now(),
			},
		},
		Edges: []*Edge{
			{
				From:      "node1",
				To:        "node2",
				Type:      EdgeContains,
				UpdatedAt: time.Now(),
			},
		},
	}
	if err := gob.NewEncoder(file).Encode(oldData); err != nil {
		file.Close()
		t.Fatalf("failed to encode old payload: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("failed to close test file: %v", err)
	}

	store := NewGOBRPGStore(indexPath)
	err = store.Load(context.Background())
	if !errors.Is(err, ErrRPGIndexOutdated) {
		t.Fatalf("expected ErrRPGIndexOutdated, got %v", err)
	}

	graph := store.GetGraph()
	if len(graph.Nodes) != 0 {
		t.Fatalf("expected cleared graph nodes on outdated index, got %d", len(graph.Nodes))
	}
	if len(graph.Edges) != 0 {
		t.Fatalf("expected cleared graph edges on outdated index, got %d", len(graph.Edges))
	}
}

func TestGOBRPGStore_LoadLegacyEmptyVersionlessFile(t *testing.T) {
	tmpDir := t.TempDir()
	indexPath := filepath.Join(tmpDir, "rpg.gob")

	file, err := os.Create(indexPath)
	if err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	legacyEmpty := gobRPGData{
		Version: 0,
		Nodes:   map[string]*Node{},
		Edges:   []*Edge{},
	}
	if err := gob.NewEncoder(file).Encode(legacyEmpty); err != nil {
		file.Close()
		t.Fatalf("failed to encode legacy payload: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("failed to close test file: %v", err)
	}

	store := NewGOBRPGStore(indexPath)
	if err := store.Load(context.Background()); err != nil {
		t.Fatalf("expected nil error for empty legacy index, got %v", err)
	}
	if len(store.GetGraph().Nodes) != 0 {
		t.Fatalf("expected empty graph nodes, got %d", len(store.GetGraph().Nodes))
	}
	if len(store.GetGraph().Edges) != 0 {
		t.Fatalf("expected empty graph edges, got %d", len(store.GetGraph().Edges))
	}
}

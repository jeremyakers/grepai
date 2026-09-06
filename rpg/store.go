package rpg

import "context"

// RPGStore persists and loads the RPG graph.
type RPGStore interface {
	// Load reads the graph from persistent storage.
	Load(ctx context.Context) error
	// Persist writes the graph to persistent storage.
	Persist(ctx context.Context) error
	// Close cleanly shuts down the store.
	Close() error
	// GetGraph returns a detached graph snapshot for reading.
	GetGraph() *Graph
	// GetStats returns graph statistics.
	GetStats(ctx context.Context) (*GraphStats, error)
}

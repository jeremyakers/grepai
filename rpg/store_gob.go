package rpg

import (
	"context"
	"encoding/gob"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/yoanbernabeu/grepai/internal/fileutil"
)

// GOBRPGStore implements RPGStore using GOB encoding.
type GOBRPGStore struct {
	indexPath                 string
	lockPath                  string
	graph                     *Graph
	constructorPersistPending bool
	mutationGeneration        atomic.Uint64
	persistedGeneration       uint64
	mu                        sync.RWMutex
}

type gobRPGData struct {
	Version int
	Nodes   map[string]*Node
	Edges   []*Edge
}

// NewGOBRPGStore creates a new GOB-based RPG store.
func NewGOBRPGStore(indexPath string) *GOBRPGStore {
	s := &GOBRPGStore{
		indexPath:                 indexPath,
		lockPath:                  indexPath + ".lock",
		graph:                     NewGraph(),
		constructorPersistPending: true,
	}
	s.graph.onMutation = func() { s.mutationGeneration.Add(1) }
	return s
}

// Load reads the graph from persistent storage.
func (s *GOBRPGStore) Load(ctx context.Context) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	loaded := false
	defer func() {
		if err == nil {
			s.constructorPersistPending = false
			if loaded {
				s.persistedGeneration = s.mutationGeneration.Load()
			}
		}
	}()

	lockFile, err := os.OpenFile(s.lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		log.Printf("rpg: flock open failed, proceeding without lock: %v", err)
		loaded, err = s.loadUnlocked()
		return err
	}
	defer lockFile.Close()
	if err := fileutil.FlockShared(lockFile, true); err != nil {
		log.Printf("rpg: flock shared acquire failed, proceeding without lock: %v", err)
		loaded, err = s.loadUnlocked()
		return err
	}
	defer func() {
		_ = fileutil.Funlock(lockFile)
	}()
	loaded, err = s.loadUnlocked()
	return err
}

func (s *GOBRPGStore) loadUnlocked() (bool, error) {
	file, err := os.Open(s.indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil // No existing index, start fresh
		}
		return false, fmt.Errorf("failed to open rpg index: %w", err)
	}
	defer file.Close()

	var data gobRPGData
	if err := gob.NewDecoder(file).Decode(&data); err != nil {
		return false, fmt.Errorf("failed to decode rpg index: %w", err)
	}

	if data.Version != CurrentRPGIndexVersion {
		hasData := len(data.Nodes) > 0 || len(data.Edges) > 0
		s.graph.Reset()
		if hasData {
			return false, ErrRPGIndexOutdated
		}
		return true, nil
	}

	if data.Nodes == nil {
		data.Nodes = make(map[string]*Node)
	}
	if data.Edges == nil {
		data.Edges = make([]*Edge, 0)
	}

	s.graph.mu.Lock()
	s.graph.Nodes = data.Nodes
	s.graph.Edges = data.Edges
	s.graph.rebuildIndexesLocked()
	s.graph.mu.Unlock()

	return true, nil
}

// Persist writes the graph to persistent storage.
func (s *GOBRPGStore) Persist(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.hasPendingPersistLocked() {
		return nil
	}
	if err := fileutil.EnsureParentDir(s.indexPath); err != nil {
		return fmt.Errorf("failed to prepare rpg index directory: %w", err)
	}

	lockFile, err := os.OpenFile(s.lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		log.Printf("rpg: flock open failed for persist, proceeding without lock: %v", err)
		generation, persistErr := s.persistUnlocked()
		if persistErr == nil {
			s.markPersisted(generation)
		}
		return persistErr
	}
	defer lockFile.Close()
	if err := fileutil.FlockExclusive(lockFile, true); err != nil {
		log.Printf("rpg: flock exclusive acquire failed, proceeding without lock: %v", err)
		generation, persistErr := s.persistUnlocked()
		if persistErr == nil {
			s.markPersisted(generation)
		}
		return persistErr
	}
	defer func() {
		_ = fileutil.Funlock(lockFile)
	}()
	generation, err := s.persistUnlocked()
	if err == nil {
		s.markPersisted(generation)
	}
	return err
}

func (s *GOBRPGStore) persistUnlocked() (uint64, error) {
	// Snapshot Nodes and Edges under the graph's read lock so no concurrent
	// mutation can modify them while gob iterates the maps/slices.
	s.graph.mu.RLock()
	generation := s.mutationGeneration.Load()
	nodes := make(map[string]*Node, len(s.graph.Nodes))
	for k, v := range s.graph.Nodes {
		nodes[k] = v
	}
	edges := make([]*Edge, len(s.graph.Edges))
	copy(edges, s.graph.Edges)
	s.graph.mu.RUnlock()

	data := gobRPGData{
		Version: CurrentRPGIndexVersion,
		Nodes:   nodes,
		Edges:   edges,
	}

	tmpFile, err := os.CreateTemp(filepath.Dir(s.indexPath), filepath.Base(s.indexPath)+".tmp-*")
	if err != nil {
		return 0, fmt.Errorf("failed to create rpg index temp file: %w", err)
	}

	tmpPath := tmpFile.Name()
	cleanupTemp := true
	defer func() {
		if cleanupTemp {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := gob.NewEncoder(tmpFile).Encode(data); err != nil {
		_ = tmpFile.Close()
		return 0, fmt.Errorf("failed to encode rpg index: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return 0, fmt.Errorf("failed to sync rpg index temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return 0, fmt.Errorf("failed to close rpg index temp file: %w", err)
	}
	if err := fileutil.ReplaceFileAtomically(tmpPath, s.indexPath); err != nil {
		return 0, fmt.Errorf("failed to replace rpg index file: %w", err)
	}
	cleanupTemp = false

	return generation, nil
}

func (s *GOBRPGStore) markPersisted(generation uint64) {
	s.constructorPersistPending = false
	s.persistedGeneration = generation
}

func (s *GOBRPGStore) hasPendingPersistLocked() bool {
	return s.constructorPersistPending || s.mutationGeneration.Load() != s.persistedGeneration
}

func (s *GOBRPGStore) hasPendingPersist() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.hasPendingPersistLocked()
}

// Close cleanly shuts down the store by persisting data.
func (s *GOBRPGStore) Close() error {
	return s.Persist(context.Background())
}

// GetGraph returns the in-memory graph.
func (s *GOBRPGStore) GetGraph() *Graph {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.graph
}

// GetStats returns graph statistics.
func (s *GOBRPGStore) GetStats(ctx context.Context) (*GraphStats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := s.graph.Stats()
	return &stats, nil
}

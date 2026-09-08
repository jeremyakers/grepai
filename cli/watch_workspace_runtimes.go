package cli

import (
	"context"
	"fmt"
	"log"

	"github.com/yoanbernabeu/grepai/config"
	"github.com/yoanbernabeu/grepai/embedder"
	"github.com/yoanbernabeu/grepai/store"
	"github.com/yoanbernabeu/grepai/watcher"
)

type workspaceRuntimeInitializer func(context.Context, *config.Workspace, config.ProjectEntry, embedder.Embedder, store.VectorStore, bool) (*workspaceProjectRuntime, *watcher.Watcher, error)

func initializeWorkspaceRuntimes(ctx context.Context, ws *config.Workspace, emb embedder.Embedder, sharedStore store.VectorStore, isBackgroundChild bool, initialize workspaceRuntimeInitializer) (map[string]*workspaceProjectRuntime, []*watcher.Watcher, error) {
	runtimes := make(map[string]*workspaceProjectRuntime, len(ws.Projects))
	watchers := make([]*watcher.Watcher, 0, len(ws.Projects))
	for _, project := range ws.Projects {
		if !isBackgroundChild {
			fmt.Printf("\nIndexing project: %s (%s)\n", project.Name, project.Path)
		} else {
			log.Printf("Indexing project: %s (%s)", project.Name, project.Path)
		}

		runtime, w, err := initialize(ctx, ws, project, emb, sharedStore, isBackgroundChild)
		if err != nil {
			closeWorkspaceRuntimes(runtimes, watchers)
			return nil, nil, fmt.Errorf("failed to initialize workspace project %s (%s): %w", project.Name, project.Path, err)
		}
		runtimes[canonicalPath(project.Path)] = runtime
		if w != nil {
			watchers = append(watchers, w)
		}
	}
	return runtimes, watchers, nil
}

func initializeWorkspaceRuntimesBeforeReady(ctx context.Context, ws *config.Workspace, emb embedder.Embedder, sharedStore store.VectorStore, isBackgroundChild bool, initialize workspaceRuntimeInitializer, publishReady func() error) (map[string]*workspaceProjectRuntime, []*watcher.Watcher, error) {
	runtimes, watchers, err := initializeWorkspaceRuntimes(ctx, ws, emb, sharedStore, isBackgroundChild, initialize)
	if err != nil {
		return nil, nil, err
	}
	if publishReady != nil {
		if err := publishReady(); err != nil {
			closeWorkspaceRuntimes(runtimes, watchers)
			return nil, nil, fmt.Errorf("failed to publish workspace readiness: %w", err)
		}
	}
	return runtimes, watchers, nil
}

func closeWorkspaceRuntimes(runtimes map[string]*workspaceProjectRuntime, watchers []*watcher.Watcher) {
	for _, w := range watchers {
		_ = w.Close()
	}
	for _, runtime := range runtimes {
		if runtime.symbolStore != nil {
			if err := runtime.symbolStore.Close(); err != nil {
				log.Printf("Warning: failed to close symbol store for %s: %v", runtime.project.Path, err)
			}
		}
		if runtime.rpgStore != nil {
			if err := runtime.rpgStore.Close(); err != nil {
				log.Printf("Warning: failed to close RPG store for %s: %v", runtime.project.Path, err)
			}
		}
	}
}

package cli

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/yoanbernabeu/grepai/config"
	"github.com/yoanbernabeu/grepai/embedder"
	"github.com/yoanbernabeu/grepai/store"
	"github.com/yoanbernabeu/grepai/watcher"
)

type watchSource interface {
	Events() <-chan watcher.FileEvent
	Errors() <-chan error
	Close() error
}

type workspaceWatcherError struct {
	ProjectName string
	ProjectPath string
	Cause       error
}

func (e *workspaceWatcherError) Error() string {
	return fmt.Sprintf("filesystem watcher failed for project %s (%s): %v", e.ProjectName, e.ProjectPath, e.Cause)
}

func (e *workspaceWatcherError) Unwrap() error { return e.Cause }

func isFatalWatcherError(err error) bool {
	var registrationErr *watcher.RegistrationError
	var fatalErr *watcher.FatalError
	return errors.As(err, &registrationErr) || errors.As(err, &fatalErr)
}

func forwardWorkspaceWatcher(ctx context.Context, runtime *workspaceProjectRuntime, events chan<- workspaceWatchEvent, fatals chan<- error) {
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-runtime.watcher.Events():
			select {
			case events <- workspaceWatchEvent{projectPath: runtime.project.Path, event: event}:
			case <-ctx.Done():
				return
			}
		case err := <-runtime.watcher.Errors():
			fatal := &workspaceWatcherError{ProjectName: runtime.project.Name, ProjectPath: runtime.project.Path, Cause: err}
			select {
			case fatals <- fatal:
			default:
			}
			return
		}
	}
}

type workspaceRuntimeInitializer func(context.Context, *config.Workspace, config.ProjectEntry, embedder.Embedder, store.VectorStore, bool) (*workspaceProjectRuntime, watchSource, error)

func initializeWorkspaceRuntimes(ctx context.Context, ws *config.Workspace, emb embedder.Embedder, sharedStore store.VectorStore, isBackgroundChild bool, initialize workspaceRuntimeInitializer) (map[string]*workspaceProjectRuntime, []watchSource, error) {
	runtimes := make(map[string]*workspaceProjectRuntime, len(ws.Projects))
	watchers := make([]watchSource, 0, len(ws.Projects))
	for _, project := range ws.Projects {
		if !isBackgroundChild {
			fmt.Printf("\nIndexing project: %s (%s)\n", project.Name, project.Path)
		} else {
			log.Printf("Indexing project: %s (%s)", project.Name, project.Path)
		}
		runtime, w, err := initialize(ctx, ws, project, emb, sharedStore, isBackgroundChild)
		if err != nil {
			var registrationErr *watcher.RegistrationError
			if errors.As(err, &registrationErr) {
				closeWorkspaceRuntimes(runtimes, watchers)
				return nil, nil, fmt.Errorf("failed to initialize watcher for project %s (%s): %w", project.Name, project.Path, err)
			}
			log.Printf("Warning: failed to initialize runtime for %s: %v", project.Name, err)
			continue
		}
		runtimes[canonicalPath(project.Path)] = runtime
		watchers = append(watchers, w)
	}
	return runtimes, watchers, nil
}

func closeWorkspaceRuntimes(runtimes map[string]*workspaceProjectRuntime, watchers []watchSource) {
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

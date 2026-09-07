package cli

import (
	"context"
	"errors"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/yoanbernabeu/grepai/config"
	"github.com/yoanbernabeu/grepai/embedder"
	"github.com/yoanbernabeu/grepai/indexer"
	"github.com/yoanbernabeu/grepai/store"
	"github.com/yoanbernabeu/grepai/trace"
	"github.com/yoanbernabeu/grepai/watcher"
)

type fakeWatchSource struct {
	events chan watcher.FileEvent
	errors chan error
	closed int
}

func newFakeWatchSource() *fakeWatchSource {
	return &fakeWatchSource{
		events: make(chan watcher.FileEvent),
		errors: make(chan error, 1),
	}
}

func (w *fakeWatchSource) Events() <-chan watcher.FileEvent { return w.events }
func (w *fakeWatchSource) Errors() <-chan error             { return w.errors }
func (w *fakeWatchSource) Close() error {
	w.closed++
	return nil
}

type persistCountingStore struct {
	mockVectorStore
	persists int
}

func (s *persistCountingStore) Persist(context.Context) error {
	s.persists++
	return nil
}

func TestRunProjectWatchLoopFatalWatcherErrorPersistsAndCloses(t *testing.T) {
	root := t.TempDir()
	source := newFakeWatchSource()
	fatal := &watcher.FatalError{Operation: "process filesystem events", Path: root, Cause: syscall.ENOSPC}
	source.errors <- fatal
	vectorStore := &persistCountingStore{}
	symbolStore := trace.NewGOBSymbolStore(filepath.Join(root, "symbols.gob"))
	cfg := config.DefaultConfig()

	err := runProjectWatchLoop(context.Background(), vectorStore, symbolStore, source, nil, nil, nil, nil, nil, nil, root, cfg, nil, nil, nil)
	if !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("runProjectWatchLoop() error = %v, want ENOSPC", err)
	}
	if vectorStore.persists != 1 {
		t.Fatalf("vector store persisted %d times, want 1", vectorStore.persists)
	}
	if source.closed != 1 {
		t.Fatalf("watcher closed %d times, want 1", source.closed)
	}
}

func TestFatalWatcherErrorClassificationPreservesWrapping(t *testing.T) {
	registrationErr := &watcher.RegistrationError{Operation: "add watch", Path: "/project", Cause: syscall.EMFILE}
	if !isFatalWatcherError(errors.Join(errors.New("session failed"), registrationErr)) {
		t.Fatal("wrapped registration error was not classified as fatal")
	}
	if isFatalWatcherError(errors.New("optional initialization warning")) {
		t.Fatal("ordinary initialization error was classified as fatal")
	}
}

func TestDynamicWatchSupervisorLinkedWatcherFatalStopsAllSessions(t *testing.T) {
	mainRoot := t.TempDir()
	linkedRoot := t.TempDir()
	fatal := &watcher.FatalError{Operation: "process filesystem events", Path: linkedRoot, Cause: syscall.ENOSPC}
	runner := func(ctx context.Context, projectRoot string, _ embedder.Embedder, _ bool, onReady func(), _ watchSessionEventObserver, _ func(int, int, string), _ func(indexer.BatchProgressInfo), _ func(string, int, int), _ watchActivityObserver, _ watchStatsObserver) error {
		if projectRoot == linkedRoot {
			return fatal
		}
		onReady()
		<-ctx.Done()
		return ctx.Err()
	}

	err := runDynamicWatchSupervisor(
		context.Background(),
		mainRoot,
		nil,
		withWatchSupervisorInitialLinkedWorktrees([]string{linkedRoot}),
		withWatchSupervisorSessionRunner(runner),
	)
	if !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("runDynamicWatchSupervisor() error = %v, want ENOSPC", err)
	}
}

func TestForwardWorkspaceWatcherFatalIdentifiesProjectAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := newFakeWatchSource()
	runtime := &workspaceProjectRuntime{
		project: config.ProjectEntry{Name: "api", Path: "/workspace/api"},
		watcher: source,
	}
	events := make(chan workspaceWatchEvent)
	fatals := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		forwardWorkspaceWatcher(ctx, runtime, events, fatals)
		close(done)
	}()
	source.errors <- &watcher.FatalError{Operation: "process filesystem events", Cause: syscall.ENOSPC}

	err := <-fatals
	var projectErr *workspaceWatcherError
	if !errors.As(err, &projectErr) || projectErr.ProjectName != "api" || projectErr.ProjectPath != "/workspace/api" {
		t.Fatalf("fatal error = %#v, want tagged api project", err)
	}
	if !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("fatal error = %v, want ENOSPC", err)
	}
	<-done
}

func TestInitializeWorkspaceRuntimesRegistrationFailureCleansPriorRuntime(t *testing.T) {
	first := newFakeWatchSource()
	ws := &config.Workspace{Projects: []config.ProjectEntry{
		{Name: "first", Path: "/first"},
		{Name: "second", Path: "/second"},
	}}
	initCalls := 0
	initFn := func(context.Context, *config.Workspace, config.ProjectEntry, embedder.Embedder, store.VectorStore, bool) (*workspaceProjectRuntime, watchSource, error) {
		initCalls++
		if initCalls == 1 {
			return &workspaceProjectRuntime{project: ws.Projects[0], watcher: first}, first, nil
		}
		return nil, nil, &watcher.RegistrationError{Operation: "add watch", Path: "/second", Cause: syscall.ENOSPC}
	}

	_, _, err := initializeWorkspaceRuntimes(context.Background(), ws, nil, nil, false, initFn)
	if !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("initializeWorkspaceRuntimes() error = %v, want ENOSPC", err)
	}
	if first.closed != 1 {
		t.Fatalf("prior watcher closed %d times, want 1", first.closed)
	}
}

func TestInitializeWorkspaceRuntimesKeepsOptionalInitializationWarningBehavior(t *testing.T) {
	ws := &config.Workspace{Projects: []config.ProjectEntry{
		{Name: "optional-failure", Path: "/optional"},
		{Name: "healthy", Path: "/healthy"},
	}}
	healthy := newFakeWatchSource()
	initFn := func(_ context.Context, _ *config.Workspace, project config.ProjectEntry, _ embedder.Embedder, _ store.VectorStore, _ bool) (*workspaceProjectRuntime, watchSource, error) {
		if project.Name == "optional-failure" {
			return nil, nil, errors.New("optional index initialization failed")
		}
		return &workspaceProjectRuntime{project: project, watcher: healthy}, healthy, nil
	}

	runtimes, watchers, err := initializeWorkspaceRuntimes(context.Background(), ws, nil, nil, true, initFn)
	if err != nil {
		t.Fatalf("initializeWorkspaceRuntimes() error = %v", err)
	}
	if len(runtimes) != 1 || len(watchers) != 1 {
		t.Fatalf("initialized %d runtimes and %d watchers, want 1 each", len(runtimes), len(watchers))
	}
	closeWorkspaceRuntimes(runtimes, watchers)
}

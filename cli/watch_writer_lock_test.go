package cli

import (
	"context"
	"errors"
	"testing"

	"github.com/yoanbernabeu/grepai/config"
	"github.com/yoanbernabeu/grepai/internal/fileutil"
)

func TestProjectWatchWriterLockContendsAcrossModesAndLogDirs(t *testing.T) {
	projectRoot := t.TempDir()
	foregroundLogDir := t.TempDir()
	backgroundLogDir := t.TempDir()
	lock, err := fileutil.AcquireProjectWriterLock(projectRoot)
	if err != nil {
		t.Fatalf("AcquireProjectWriterLock() error = %v", err)
	}
	defer lock.Close()

	originalLogDir := watchLogDir
	defer func() { watchLogDir = originalLogDir }()
	cases := []struct {
		name string
		run  func() error
	}{
		{
			name: "foreground",
			run: func() error {
				watchLogDir = foregroundLogDir
				return watchProject(context.Background(), projectRoot, nil, false, nil)
			},
		},
		{
			name: "background different log directory",
			run: func() error {
				watchLogDir = backgroundLogDir
				return watchProjectWithEventObserver(context.Background(), projectRoot, nil, true, nil, nil, nil, nil, nil, nil, nil)
			},
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run()
			var activeErr *fileutil.ProjectWriterActiveError
			if !errors.As(err, &activeErr) {
				t.Fatalf("watch entrypoint error = %T %v, want *ProjectWriterActiveError", err, err)
			}
		})
	}
}

func TestProjectWatchWriterLockReleasesAfterStartupError(t *testing.T) {
	projectRoot := t.TempDir()
	wantErr := errors.New("store initialization failed")
	if err := runProjectWatchWithWriterLock(projectRoot, func(string) error { return wantErr }); !errors.Is(err, wantErr) {
		t.Fatalf("runProjectWatchWithWriterLock() error = %v, want %v", err, wantErr)
	}

	if err := runProjectWatchWithWriterLock(projectRoot, func(string) error { return nil }); err != nil {
		t.Fatalf("lock leaked after startup error: %v", err)
	}
}

func TestProjectWatchWriterLockReleasesOnContextCancellation(t *testing.T) {
	projectRoot := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- runProjectWatchWithWriterLock(projectRoot, func(string) error {
			close(started)
			<-ctx.Done()
			return nil
		})
	}()
	<-started
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("canceled watcher error = %v", err)
	}

	if err := runProjectWatchWithWriterLock(projectRoot, func(string) error { return nil }); err != nil {
		t.Fatalf("lock leaked after cancellation: %v", err)
	}
}

func TestWorkspaceProjectWriterLocksCoverEveryProjectAndReleasePartialAcquisition(t *testing.T) {
	firstRoot := t.TempDir()
	secondRoot := t.TempDir()
	projects := []config.ProjectEntry{
		{Name: "first", Path: firstRoot},
		{Name: "second", Path: secondRoot},
	}

	locks, err := acquireWorkspaceProjectWriterLocks(projects)
	if err != nil {
		t.Fatalf("acquireWorkspaceProjectWriterLocks() error = %v", err)
	}
	for _, project := range projects {
		if err := runProjectWatchWithWriterLock(project.Path, func(string) error { return nil }); err == nil {
			t.Fatalf("project %s was not protected by its workspace writer lock", project.Name)
		}
	}
	for _, lock := range locks {
		if err := lock.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	}

	blocker, err := fileutil.AcquireProjectWriterLock(secondRoot)
	if err != nil {
		t.Fatalf("AcquireProjectWriterLock(blocker) error = %v", err)
	}
	defer blocker.Close()
	if _, err := acquireWorkspaceProjectWriterLocks(projects); err == nil {
		t.Fatal("acquireWorkspaceProjectWriterLocks() succeeded with a contended project")
	}
	if err := runProjectWatchWithWriterLock(firstRoot, func(string) error { return nil }); err != nil {
		t.Fatalf("first project lock leaked after partial workspace acquisition: %v", err)
	}
}

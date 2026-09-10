package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/yoanbernabeu/grepai/internal/fileutil"
)

// copyFileIfExists atomically publishes src at dst when src exists.
func copyFileIfExists(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err := fileutil.EnsureParentDir(dst); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := fileutil.ReplaceFileAtomically(tmpPath, dst); err != nil {
		return fmt.Errorf("failed to publish worktree seed: %w", err)
	}
	cleanup = false
	return nil
}

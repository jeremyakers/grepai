package fileutil

import (
	"os"
	"path/filepath"
)

// EnsureParentDir creates parent directories for the given path if they do not exist.
func EnsureParentDir(filePath string) error {
	dir := filepath.Dir(filePath)
	return os.MkdirAll(dir, 0755)
}

// ReplaceFileAtomically replaces targetPath with tempPath using one filesystem
// namespace operation. Callers create tempPath beside targetPath, so a
// cross-device fallback is neither necessary nor safe.
func ReplaceFileAtomically(tempPath, targetPath string) error {
	return replaceFileAtomically(tempPath, targetPath)
}

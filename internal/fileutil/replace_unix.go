//go:build !windows

package fileutil

import "os"

func replaceFileAtomically(tempPath, targetPath string) error {
	return os.Rename(tempPath, targetPath)
}

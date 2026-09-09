//go:build windows

package fileutil

import (
	"fmt"

	"golang.org/x/sys/windows"
)

func replaceFileAtomically(tempPath, targetPath string) error {
	from, err := windows.UTF16PtrFromString(tempPath)
	if err != nil {
		return fmt.Errorf("invalid replacement path: %w", err)
	}
	to, err := windows.UTF16PtrFromString(targetPath)
	if err != nil {
		return fmt.Errorf("invalid target path: %w", err)
	}
	if err := windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH); err != nil {
		return fmt.Errorf("failed to atomically replace file: %w", err)
	}
	return nil
}

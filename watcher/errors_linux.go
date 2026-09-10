//go:build linux

package watcher

import (
	"errors"
	"syscall"
)

func inotifyLimitHint(err error) string {
	if errors.Is(err, syscall.ENOSPC) {
		return "inotify watch limit reached; increase fs.inotify.max_user_watches or stop other file watchers"
	}
	return ""
}

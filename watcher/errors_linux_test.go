//go:build linux

package watcher

import (
	"strings"
	"syscall"
	"testing"
)

func TestENOSPCHasActionableInotifyHintOnLinux(t *testing.T) {
	err := (&RegistrationError{Operation: "add watch", Path: "project", Cause: syscall.ENOSPC}).Error()
	if !strings.Contains(err, "inotify watch limit") || strings.Contains(err, "disk") {
		t.Fatalf("Linux ENOSPC error is not actionable: %v", err)
	}
}

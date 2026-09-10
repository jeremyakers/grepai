//go:build !linux

package watcher

import (
	"strings"
	"syscall"
	"testing"
)

func TestENOSPCDoesNotClaimInotifyOffLinux(t *testing.T) {
	err := (&RegistrationError{Operation: "add watch", Path: "project", Cause: syscall.ENOSPC}).Error()
	if strings.Contains(err, "inotify") {
		t.Fatalf("non-Linux ENOSPC error claims inotify: %v", err)
	}
}

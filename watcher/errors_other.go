//go:build !linux

package watcher

func inotifyLimitHint(error) string { return "" }

//go:build !unix

package indexer

import "os"

func openSnapshotFile(path string) (*os.File, error) { return os.Open(path) }

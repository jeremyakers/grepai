package indexer

import (
	"os"
	"path/filepath"
	"strings"
)

// PathExclusionReason identifies an intentional scanner policy exclusion.
// An empty reason means the path is eligible or could not be classified safely.
type PathExclusionReason string

const (
	PathExcludedIgnored     PathExclusionReason = "ignored"
	PathExcludedUnsupported PathExclusionReason = "unsupported extension"
	PathExcludedMinified    PathExclusionReason = "minified"
	PathExcludedTooLarge    PathExclusionReason = "too large"
	PathExcludedBinary      PathExclusionReason = "binary"
)

// ExistingPathExclusion classifies a currently existing path using the same
// policies as scanning. Filesystem and read errors are returned so callers can
// preserve uncertain index entries rather than treating them as exclusions.
func (s *Scanner) ExistingPathExclusion(relPath string) (PathExclusionReason, error) {
	relPath = filepath.FromSlash(relPath)
	absPath := filepath.Join(s.root, relPath)
	info, err := os.Stat(absPath)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", nil
	}
	if s.ignore.ShouldIgnore(relPath) {
		return PathExcludedIgnored, nil
	}
	if !s.isSupported(strings.ToLower(filepath.Ext(relPath))) {
		return PathExcludedUnsupported, nil
	}
	if isMinifiedFile(relPath) {
		return PathExcludedMinified, nil
	}
	if info.Size() > maxFileSize {
		return PathExcludedTooLarge, nil
	}
	snapshot, err := s.readSnapshot(absPath, filepath.ToSlash(relPath))
	if err != nil {
		return "", err
	}
	if snapshot != nil {
		return "", nil
	}
	// A stable regular in-range file can only be rejected here as binary.
	current, err := os.Stat(absPath)
	if err != nil {
		return "", err
	}
	if !current.Mode().IsRegular() {
		return "", nil
	}
	if current.Size() > maxFileSize {
		return PathExcludedTooLarge, nil
	}
	return PathExcludedBinary, nil
}

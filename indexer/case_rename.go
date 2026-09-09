package indexer

import (
	"os"
	"path/filepath"
	"strings"
)

type caseRenameFS struct {
	stat    func(string) (os.FileInfo, error)
	readDir func(string) ([]os.DirEntry, error)
}

// FindCaseRenameWitnesses identifies stale path spellings that resolve to a
// differently spelled file observed by the current scan. Case folding only
// finds candidates; fresh identity and directory-entry checks authorize them.
func FindCaseRenameWitnesses(root string, candidates []string, scanned []FileMeta) map[string]string {
	return findCaseRenameWitnessesWith(root, candidates, scanned, caseRenameFS{stat: os.Stat, readDir: os.ReadDir})
}

func findCaseRenameWitnessesWith(root string, candidates []string, scanned []FileMeta, filesystem caseRenameFS) map[string]string {
	type observedPath struct {
		path      string
		ambiguous bool
	}
	observed := make(map[string]observedPath, len(scanned))
	for _, file := range scanned {
		path, ok := cleanRelativePath(file.Path)
		if !ok {
			continue
		}
		key := casePathKey(path)
		if prior, exists := observed[key]; exists && prior.path != path {
			prior.ambiguous = true
			observed[key] = prior
		} else if !exists {
			observed[key] = observedPath{path: path}
		}
	}

	directoryCache := make(map[string][]os.DirEntry)
	witnesses := make(map[string]string)
	for _, candidate := range candidates {
		oldPath, ok := cleanRelativePath(candidate)
		if !ok {
			continue
		}
		witness, ok := observed[casePathKey(oldPath)]
		if !ok || witness.ambiguous || witness.path == oldPath {
			continue
		}
		actual, ok := actualPathSpelling(root, oldPath, filesystem.readDir, directoryCache)
		if !ok || actual == oldPath || actual != witness.path {
			continue
		}
		oldInfo, err := filesystem.stat(filepath.Join(root, filepath.FromSlash(oldPath)))
		if err != nil {
			continue
		}
		newInfo, err := filesystem.stat(filepath.Join(root, filepath.FromSlash(witness.path)))
		if err != nil || !os.SameFile(oldInfo, newInfo) {
			continue
		}
		witnesses[candidate] = witness.path
	}
	return witnesses
}

func cleanRelativePath(path string) (string, bool) {
	clean := filepath.Clean(filepath.FromSlash(path))
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(clean), true
}

func casePathKey(path string) string { return strings.ToLower(filepath.ToSlash(path)) }

func actualPathSpelling(root, relative string, readDir func(string) ([]os.DirEntry, error), cache map[string][]os.DirEntry) (string, bool) {
	parts := strings.Split(filepath.ToSlash(relative), "/")
	parent := root
	actual := make([]string, 0, len(parts))
	for _, requested := range parts {
		entries, ok := cache[parent]
		if !ok {
			var err error
			entries, err = readDir(parent)
			if err != nil {
				return "", false
			}
			cache[parent] = entries
		}
		name, found := actualEntryName(entries, requested)
		if !found {
			return "", false
		}
		actual = append(actual, name)
		parent = filepath.Join(parent, name)
	}
	return strings.Join(actual, "/"), true
}

func actualEntryName(entries []os.DirEntry, requested string) (string, bool) {
	for _, entry := range entries {
		if entry.Name() == requested {
			return requested, true
		}
	}
	match := ""
	for _, entry := range entries {
		if strings.EqualFold(entry.Name(), requested) {
			if match != "" {
				return "", false
			}
			match = entry.Name()
		}
	}
	return match, match != ""
}

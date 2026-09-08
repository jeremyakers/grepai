package cli

import (
	"context"
	"strings"
	"time"

	"github.com/yoanbernabeu/grepai/store"
)

func (p *projectPrefixStore) ListDocumentMetadata(ctx context.Context) ([]store.DocumentMetadata, error) {
	all, err := store.LoadDocumentMetadata(ctx, p.store)
	if err != nil {
		return nil, err
	}
	prefix := p.getPrefix() + "/"
	out := make([]store.DocumentMetadata, 0, len(all))
	for _, metadata := range all {
		if strings.HasPrefix(metadata.Path, prefix) {
			metadata.Path = strings.TrimPrefix(metadata.Path, prefix)
			out = append(out, metadata)
		}
	}
	return out, nil
}

func (p *projectPrefixStore) RefreshDocumentModTime(ctx context.Context, path, expectedHash string, modTime time.Time) (bool, error) {
	refresher, ok := p.store.(store.DocumentModTimeRefresher)
	if !ok {
		return false, store.ErrRefreshUnsupported
	}
	return refresher.RefreshDocumentModTime(ctx, p.getPrefix()+"/"+p.toRelSlash(path), expectedHash, modTime)
}

var _ store.DocumentMetadataSource = (*projectPrefixStore)(nil)
var _ store.DocumentModTimeRefresher = (*projectPrefixStore)(nil)

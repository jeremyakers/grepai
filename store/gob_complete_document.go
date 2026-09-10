package store

import "context"

func (s *GOBStore) GetCompleteDocument(ctx context.Context, filePath string) (*Document, error) {
	return s.GetCompleteDocumentWithPrefix(ctx, filePath, "")
}

func (s *GOBStore) GetCompleteDocumentWithPrefix(_ context.Context, filePath, chunkIDPrefix string) (*Document, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	doc, ok := s.documents[filePath]
	if !ok || len(doc.ChunkIDs) == 0 {
		return nil, nil
	}
	for _, id := range doc.ChunkIDs {
		if !s.hasValidChunkForReference(id, filePath, chunkIDPrefix) {
			return nil, nil
		}
	}

	doc.ChunkIDs = append([]string(nil), doc.ChunkIDs...)
	return &doc, nil
}

// hasValidChunkForReference reports whether any chunk stored under the raw
// reference or its prefixed form belongs to filePath and carries a vector. An
// exact-ID chunk with the wrong owner or an empty vector does not block a
// valid prefixed mapping.
func (s *GOBStore) hasValidChunkForReference(id, filePath, chunkIDPrefix string) bool {
	if chunk, ok := s.chunks[id]; ok && chunk.FilePath == filePath && len(chunk.Vector) > 0 {
		return true
	}
	if chunkIDPrefix == "" {
		return false
	}
	chunk, ok := s.chunks[chunkIDPrefix+"/"+id]
	return ok && chunk.FilePath == filePath && len(chunk.Vector) > 0
}

var _ CompleteDocumentSource = (*GOBStore)(nil)
var _ PrefixedCompleteDocumentSource = (*GOBStore)(nil)

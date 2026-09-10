package store

import "context"

func (s *GOBStore) GetCompleteDocument(_ context.Context, filePath string) (*Document, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	doc, ok := s.documents[filePath]
	if !ok || len(doc.ChunkIDs) == 0 {
		return nil, nil
	}
	for _, id := range doc.ChunkIDs {
		chunk, ok := s.chunks[id]
		if !ok || chunk.FilePath != filePath || len(chunk.Vector) == 0 {
			return nil, nil
		}
	}

	doc.ChunkIDs = append([]string(nil), doc.ChunkIDs...)
	return &doc, nil
}

var _ CompleteDocumentSource = (*GOBStore)(nil)

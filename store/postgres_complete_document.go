package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const getCompleteDocumentSQL = `
SELECT d.path, d.hash, d.mod_time, d.mod_time_ns, d.chunk_ids
FROM documents d
WHERE d.project_id = $1
  AND d.path = $2
  AND cardinality(d.chunk_ids) > 0
  AND NOT EXISTS (
    SELECT 1
    FROM unnest(d.chunk_ids) AS referenced(id)
    LEFT JOIN chunks c
      ON c.project_id = d.project_id
     AND c.id = referenced.id
     AND c.file_path = d.path
    WHERE c.id IS NULL OR c.vector IS NULL
  )`

func (s *PostgresStore) GetCompleteDocument(ctx context.Context, filePath string) (*Document, error) {
	var doc Document
	var modTime time.Time
	var modTimeNS *int64
	err := s.pool.QueryRow(ctx, getCompleteDocumentSQL, s.projectID, filePath).Scan(
		&doc.Path, &doc.Hash, &modTime, &modTimeNS, &doc.ChunkIDs,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get complete document: %w", err)
	}

	doc.ModTime, doc.HasExactModTime = decodeExactModTime(modTime, modTimeNS)
	doc.ChunkIDs = append([]string(nil), doc.ChunkIDs...)
	return &doc, nil
}

var _ CompleteDocumentSource = (*PostgresStore)(nil)

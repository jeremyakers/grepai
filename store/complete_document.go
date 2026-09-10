package store

import (
	"context"
	"errors"
)

// ErrCompleteDocumentUnsupported indicates that a store cannot atomically
// validate document metadata and all of its referenced chunks.
var ErrCompleteDocumentUnsupported = errors.New("complete document lookup unsupported")

// CompleteDocumentSource optionally provides a coherent store observation of
// document metadata and every chunk it references. The path is interpreted in
// the store's namespace. Missing or incomplete documents return (nil, nil), and
// successful reads return a detached Document. This contract does not imply an
// atomic observation of the filesystem represented by the store.
type CompleteDocumentSource interface {
	GetCompleteDocument(ctx context.Context, filePath string) (*Document, error)
}

// PrefixedCompleteDocumentSource optionally extends the complete-document
// contract for callers whose stored documents reference chunk IDs relative to
// a fixed namespace prefix while chunks may be stored under prefixed IDs. Each
// reference is satisfied by any valid chunk (same file, non-empty vector)
// whose ID equals the reference or, when chunkIDPrefix is non-empty,
// chunkIDPrefix+"/"+reference. An empty prefix matches exact IDs only.
type PrefixedCompleteDocumentSource interface {
	GetCompleteDocumentWithPrefix(ctx context.Context, filePath, chunkIDPrefix string) (*Document, error)
}

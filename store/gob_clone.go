package store

func cloneChunk(chunk Chunk) Chunk {
	chunk.Vector = append([]float32(nil), chunk.Vector...)
	return chunk
}

func cloneDocument(doc Document) Document {
	doc.ChunkIDs = append([]string(nil), doc.ChunkIDs...)
	return doc
}

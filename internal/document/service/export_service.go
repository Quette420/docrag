package service

import (
	"context"
	"github.com/Quette420/docrag/internal/model"
)

type DocumentSource interface {
	LoadDocuments(ctx context.Context) ([]*model.Document, error)
}

// Chunker splits documents into chunks.
type Chunker interface {
	SplitDocuments(ctx context.Context, docs []*model.Document) ([]*model.Chunk, error)
}

// Embedder fills in chunk embeddings in place.
type Embedder interface {
	Embed(ctx context.Context, chunks []*model.Chunk) error
}

// Indexer persists chunks for later retrieval.
type Indexer interface {
	Store(ctx context.Context, chunks []*model.Chunk) error
}

// ExportService runs the indexing pipeline: load -> chunk -> embed -> store.
type ExportService struct {
	source   DocumentSource
	chunker  Chunker
	embedder Embedder
	indexer  Indexer
}

func NewExportService(src DocumentSource, ch Chunker, emb Embedder, idx Indexer) *ExportService {
	return &ExportService{
		source:   src,
		chunker:  ch,
		embedder: emb,
		indexer:  idx,
	}
}

// Run executes the full pipeline and returns the produced chunks.
func (s *ExportService) Run(ctx context.Context) ([]*model.Chunk, error) {
	docs, err := s.source.LoadDocuments(ctx)
	if err != nil {
		return nil, err
	}

	chunks, err := s.chunker.SplitDocuments(ctx, docs)
	if err != nil {
		return nil, err
	}

	if err := s.embedder.Embed(ctx, chunks); err != nil {
		return nil, err
	}

	if err := s.indexer.Store(ctx, chunks); err != nil {
		return nil, err
	}

	return chunks, nil
}

package inmemory

import (
	"context"
	"fmt"
	"github.com/Quette420/docrag/internal/model"
	"math"
	"sort"
	"sync"
)

// Indexer keeps chunks in memory and performs brute-force cosine similarity search.
type Indexer struct {
	mu     sync.RWMutex
	chunks map[string]*model.Chunk // key = chunk.ID
}

func NewInMemoryIndexer() *Indexer {
	return &Indexer{
		chunks: make(map[string]*model.Chunk),
	}
}

func (i *Indexer) Store(ctx context.Context, chunks []*model.Chunk) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	for _, ch := range chunks {
		if ch.ID == "" {
			return fmt.Errorf("chunk missing ID")
		}
		i.chunks[ch.ID] = ch
	}
	return nil
}

// GetChunk returns a chunk by ID.
func (i *Indexer) GetChunk(id string) (*model.Chunk, bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	ch, ok := i.chunks[id]
	return ch, ok
}

// Search returns the topK chunks most similar to queryEmbedding (cosine similarity).
func (i *Indexer) Search(ctx context.Context, queryEmbedding []float32, topK int) ([]*model.Chunk, error) {
	i.mu.RLock()
	defer i.mu.RUnlock()

	if len(queryEmbedding) == 0 {
		return nil, fmt.Errorf("query embedding is empty")
	}

	type scoredChunk struct {
		chunk *model.Chunk
		score float64
	}
	var scored []scoredChunk

	for _, ch := range i.chunks {
		if len(ch.Embedding) == 0 {
			continue
		}
		sim := cosineSimilarity(queryEmbedding, ch.Embedding)
		scored = append(scored, scoredChunk{chunk: ch, score: sim})
	}

	sort.Slice(scored, func(a, b int) bool {
		return scored[a].score > scored[b].score
	})

	if topK > len(scored) {
		topK = len(scored)
	}
	result := make([]*model.Chunk, topK)
	for i := 0; i < topK; i++ {
		result[i] = scored[i].chunk
	}
	return result, nil
}

// cosineSimilarity computes the cosine similarity of two float32 vectors.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

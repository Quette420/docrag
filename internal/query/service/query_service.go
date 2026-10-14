package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/Quette420/docrag/internal/model"
)

const noResultsMessage = "No relevant documents found."

type Embedder interface {
	Embed(ctx context.Context, chunks []*model.Chunk) error
}

type Searcher interface {
	Search(ctx context.Context, queryEmbedding []float32, topK int) ([]*model.Chunk, error)
}

type Generator interface {
	Generate(ctx context.Context, prompt string) (string, error)
}

type StreamGenerator interface {
	GenerateStream(ctx context.Context, prompt string, callback func(token string)) error
}

type QueryService struct {
	embedder        Embedder
	searcher        Searcher
	generator       Generator
	streamGenerator StreamGenerator
	topK            int
	answerLang      string // empty = answer in the language of the question
}

func NewQueryService(embedder Embedder, searcher Searcher, generator Generator, streamGen StreamGenerator, topK int, answerLang string) *QueryService {
	if topK <= 0 {
		topK = 5
	}
	return &QueryService{
		embedder:        embedder,
		searcher:        searcher,
		generator:       generator,
		streamGenerator: streamGen,
		topK:            topK,
		answerLang:      answerLang,
	}
}

func (q *QueryService) Ask(ctx context.Context, question string) (string, error) {
	prompt, found, err := q.buildPrompt(ctx, question)
	if err != nil {
		return "", err
	}
	if !found {
		return noResultsMessage, nil
	}

	answer, err := q.generator.Generate(ctx, prompt)
	if err != nil {
		return "", fmt.Errorf("generate answer: %w", err)
	}
	return answer, nil
}

func (q *QueryService) AskStream(ctx context.Context, question string, callback func(token string)) error {
	prompt, found, err := q.buildPrompt(ctx, question)
	if err != nil {
		return err
	}
	if !found {
		callback(noResultsMessage)
		return nil
	}
	return q.streamGenerator.GenerateStream(ctx, prompt, callback)
}

// buildPrompt embeds the question, retrieves the topK most similar chunks and
// assembles the final prompt. found is false when retrieval returned nothing.
func (q *QueryService) buildPrompt(ctx context.Context, question string) (prompt string, found bool, err error) {
	queryChunk := &model.Chunk{Content: question}
	if err := q.embedder.Embed(ctx, []*model.Chunk{queryChunk}); err != nil {
		return "", false, fmt.Errorf("embed question: %w", err)
	}
	if len(queryChunk.Embedding) == 0 {
		return "", false, fmt.Errorf("embed question: empty embedding")
	}

	similar, err := q.searcher.Search(ctx, queryChunk.Embedding, q.topK)
	if err != nil {
		return "", false, fmt.Errorf("search: %w", err)
	}
	if len(similar) == 0 {
		return "", false, nil
	}

	contextParts := make([]string, 0, len(similar))
	for i, ch := range similar {
		header := strings.Join(ch.Headings, " > ")
		if header != "" {
			contextParts = append(contextParts, fmt.Sprintf("[%d] %s\n%s", i+1, header, ch.Content))
		} else {
			contextParts = append(contextParts, fmt.Sprintf("[%d] %s", i+1, ch.Content))
		}
	}

	langRule := "Answer in the same language as the question."
	if q.answerLang != "" {
		langRule = fmt.Sprintf("Answer strictly in %s.", q.answerLang)
	}

	prompt = fmt.Sprintf(`You are a helpful assistant. Answer the question using only the information from the provided context.
If the context does not contain the answer, say that the information was not found in the documentation.
%s

Context:
%s

Question: %s

Answer:`, langRule, strings.Join(contextParts, "\n\n"), question)
	return prompt, true, nil
}

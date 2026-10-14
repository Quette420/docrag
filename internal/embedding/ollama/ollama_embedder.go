package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/Quette420/docrag/internal/model"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type OllamaEmbedder struct {
	baseURL    string
	model      string
	httpClient *http.Client
	limiter    *rate.Limiter
	workerPool int
	logger     *log.Logger
}

type ollamaRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

type ollamaResponse struct {
	Embedding []float32 `json:"embedding"`
}

func NewOllamaEmbedder(baseURL, model string, rps int, workers int) *OllamaEmbedder {
	if rps <= 0 {
		rps = 10
	}
	if workers <= 0 {
		workers = 4
	}
	return &OllamaEmbedder{
		baseURL:    baseURL,
		model:      model,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		limiter:    rate.NewLimiter(rate.Limit(rps), 1),
		workerPool: workers,
		logger:     log.New(log.Writer(), "[OllamaEmbedder] ", log.LstdFlags),
	}
}

func (e *OllamaEmbedder) Embed(ctx context.Context, chunks []*model.Chunk) error {
	if len(chunks) == 0 {
		return nil
	}

	e.logger.Printf("generating embeddings for %d chunks", len(chunks))

	type task struct {
		index int
		chunk *model.Chunk
	}
	tasks := make(chan task, len(chunks))
	results := make(chan error, len(chunks))

	var wg sync.WaitGroup
	for i := 0; i < e.workerPool; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range tasks {
				select {
				case <-ctx.Done():
					results <- ctx.Err()
					return
				default:
				}
				if err := e.limiter.Wait(ctx); err != nil {
					results <- err
					return
				}
				vec, err := e.embedOne(ctx, t.chunk.Content)
				if err != nil {
					errMsg := fmt.Errorf("chunk %d: %w", t.index, err)
					e.logger.Printf("error: %v", errMsg)
					results <- errMsg
					return
				}
				t.chunk.Embedding = vec
				results <- nil
			}
		}()
	}

	for i, ch := range chunks {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case tasks <- task{index: i, chunk: ch}:
		}
	}
	close(tasks)

	go func() {
		wg.Wait()
		close(results)
	}()

	for err := range results {
		if err != nil {
			return err
		}
	}

	e.logger.Printf("embeddings generated for %d chunks", len(chunks))
	return nil
}

func (e *OllamaEmbedder) embedOne(ctx context.Context, text string) ([]float32, error) {
	reqBody := ollamaRequest{
		Model:  e.model,
		Prompt: text,
	}
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", e.baseURL+"/api/embeddings", bytes.NewReader(jsonData))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama returned %s: %s", resp.Status, string(body))
	}

	var result ollamaResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result.Embedding, nil
}

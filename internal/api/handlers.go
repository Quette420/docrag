package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/Quette420/docrag/internal/query/service"
)

const requestTimeout = 180 * time.Second

type QueryHandler struct {
	queryService *service.QueryService
}

func NewQueryHandler(qs *service.QueryService) *QueryHandler {
	return &QueryHandler{queryService: qs}
}

type QueryRequest struct {
	Question string `json:"question"`
}

type QueryResponse struct {
	Answer  string        `json:"answer"`
	Elapsed time.Duration `json:"elapsed"`
}

// ServeHTTP handles POST /query. Responds with JSON by default, or streams
// tokens as Server-Sent Events when the client sends Accept: text/event-stream.
func (h *QueryHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req QueryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Question == "" {
		http.Error(w, "Question is empty", http.StatusBadRequest)
		return
	}

	if r.Header.Get("Accept") == "text/event-stream" {
		h.handleStreaming(w, r, req.Question)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()

	start := time.Now()
	answer, err := h.queryService.Ask(ctx, req.Question)
	if err != nil {
		slog.Error("query failed", "err", err)
		http.Error(w, fmt.Sprintf("Generation error: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(QueryResponse{Answer: answer, Elapsed: time.Since(start)})
}

// handleStreaming sends events "token" (data: JSON-encoded string), then
// either "done" or "error". Tokens are JSON-encoded so that newlines and
// leading spaces survive the SSE framing.
func (h *QueryHandler) handleStreaming(w http.ResponseWriter, r *http.Request, question string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()

	tokenChan := make(chan string, 100)
	errChan := make(chan error, 1)

	go func() {
		err := h.queryService.AskStream(ctx, question, func(token string) {
			select {
			case tokenChan <- token:
			case <-ctx.Done():
			}
		})
		// errChan is buffered, so the result is available before tokenChan closes.
		errChan <- err
		close(tokenChan)
	}()

	for {
		select {
		case token, ok := <-tokenChan:
			if !ok {
				if err := <-errChan; err != nil {
					slog.Error("streaming query failed", "err", err)
					writeEvent(w, "error", err.Error())
				} else {
					writeEvent(w, "done", "")
				}
				flusher.Flush()
				return
			}
			writeEvent(w, "token", token)
			flusher.Flush()
		case <-ctx.Done():
			writeEvent(w, "error", "timeout")
			flusher.Flush()
			return
		}
	}
}

func writeEvent(w http.ResponseWriter, event, data string) {
	payload, _ := json.Marshal(data)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload)
}

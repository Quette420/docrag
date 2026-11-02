package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/Quette420/docrag/internal/model"
	"github.com/Quette420/docrag/internal/query/service"
)

const requestTimeout = 180 * time.Second

type QueryHandler struct{ queryService *service.QueryService }

func NewQueryHandler(qs *service.QueryService) *QueryHandler { return &QueryHandler{qs} }

type QueryRequest struct {
	Question string `json:"question"`
}
type QueryResponse struct {
	Answer  string         `json:"answer"`
	Elapsed time.Duration  `json:"elapsed"`
	Sources []model.Source `json:"sources"`
}

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
	answer, err := h.queryService.AskWithSources(ctx, req.Question)
	if err != nil {
		slog.Error("query failed", "err", err)
		http.Error(w, fmt.Sprintf("Generation error: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(QueryResponse{Answer: answer.Text, Elapsed: time.Since(start), Sources: answer.Sources})
}

// Source metadata is an additive event. Existing token/done/error payloads
// remain JSON strings. A failed/truncated upstream stream never emits done.
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

	var writeErr error
	send := func(event string, data any) {
		if writeErr != nil || ctx.Err() != nil {
			return
		}
		if err := writeEvent(w, event, data); err != nil {
			writeErr = err
			cancel()
			return
		}
		flusher.Flush()
	}
	err := h.queryService.AskStreamWithSources(ctx, question,
		func(sources []model.Source) { send("sources", sources) },
		func(token string) { send("token", token) })
	if writeErr != nil || r.Context().Err() != nil {
		return
	}
	if err != nil {
		slog.Error("streaming query failed", "err", err)
		message := err.Error()
		if ctx.Err() == context.DeadlineExceeded {
			message = "timeout"
		}
		// The server deadline may already have expired; send the terminal error
		// directly while the request's client is still connected.
		_ = writeEvent(w, "error", message)
		flusher.Flush()
		return
	}
	send("done", "")
}

func writeEvent(w http.ResponseWriter, event string, data any) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload)
	return err
}

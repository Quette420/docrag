package api_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Quette420/docrag/internal/api"
	chunker "github.com/Quette420/docrag/internal/chunk/service"
	documents "github.com/Quette420/docrag/internal/document/service"
	"github.com/Quette420/docrag/internal/document/service/source"
	embedding "github.com/Quette420/docrag/internal/embedding/ollama"
	generation "github.com/Quette420/docrag/internal/generation/ollama"
	"github.com/Quette420/docrag/internal/index/inmemory"
	query "github.com/Quette420/docrag/internal/query/service"
	storage "github.com/Quette420/docrag/internal/storage/file"
)

// The fixture replaces only Ollama's HTTP service. Documents, tokenizer,
// embeddings adapter, cache, index, retrieval, generation adapter and handler
// are the real implementations. No claim about real model quality follows.
func newStack(t *testing.T, mode string, empty bool, canceled ...chan struct{}) (http.Handler, *atomic.Int32) {
	t.Helper()
	var generated atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/embeddings":
			var request struct {
				Prompt string `json:"prompt"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				return
			}
			vector := []float32{1, 0}
			if strings.Contains(request.Prompt, "beta-source") || strings.HasPrefix(request.Prompt, "# Guide") && !strings.Contains(request.Prompt, "alpha-source") {
				vector = []float32{0, 1}
			}
			json.NewEncoder(w).Encode(map[string]any{"embedding": vector})
		case "/api/generate":
			generated.Add(1)
			var request struct {
				Prompt string `json:"prompt"`
				Stream bool   `json:"stream"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				return
			}
			if !strings.Contains(request.Prompt, "[1]") || !strings.Contains(request.Prompt, "alpha-source") || strings.Contains(request.Prompt, "beta-source") {
				t.Errorf("wrong retrieved context: %s", request.Prompt)
			}
			if mode == "http-error" {
				w.WriteHeader(503)
				fmt.Fprint(w, "unavailable")
				return
			}
			if !request.Stream {
				json.NewEncoder(w).Encode(map[string]any{"response": "Found [1]."})
				return
			}
			fmt.Fprintln(w, `{"response":"Found "}`)
			if mode == "cancel" {
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				if len(canceled) > 0 {
					close(canceled[0])
				}
				return
			}
			if mode == "truncated" {
				return
			}
			if mode == "malformed" {
				fmt.Fprintln(w, `not-json`)
				return
			}
			if mode == "stream-error" {
				fmt.Fprintln(w, `{"error":"model failed"}`)
				return
			}
			fmt.Fprintln(w, `{"response":"[1].","done":true}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)
	embedder := embedding.NewOllamaEmbedder(upstream.URL, "fixture-embed", 10000, 2)
	idx := inmemory.NewInMemoryIndexer()
	if !empty {
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, "guides"), 0o755); err != nil {
			t.Fatal(err)
		}
		for name, content := range map[string]string{"alpha.md": "# Guide\n\n## Setup\n\nalpha-source installation.\n", "beta.md": "# Other\n\nbeta-source unrelated.\n"} {
			if err := os.WriteFile(filepath.Join(root, "guides", name), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		export := documents.NewExportService(source.NewFolderSource(root, []string{".md"}), chunker.NewRecursiveChunker(500, 0), embedder, idx)
		chunks, err := export.Run(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		// Restart the retrieval index from the actual serialized cache.
		cache := storage.NewFileStore()
		path := filepath.Join(t.TempDir(), "chunks.json")
		if err := cache.SaveChunks(path, chunks); err != nil {
			t.Fatal(err)
		}
		restored, err := cache.LoadChunks(path)
		if err != nil {
			t.Fatal(err)
		}
		idx = inmemory.NewInMemoryIndexer()
		if err := idx.Store(context.Background(), restored); err != nil {
			t.Fatal(err)
		}
	}
	generator := generation.NewGenerator(upstream.URL, "fixture-gen")
	return api.NewQueryHandler(query.NewQueryService(embedder, idx, generator, generator, 1, "")), &generated
}

func request(t *testing.T, handler http.Handler, stream bool) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(`{"question":"How to install alpha?"}`))
	if stream {
		r.Header.Set("Accept", "text/event-stream")
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestJSONSourcesSurviveRealPipelineAndCacheRestart(t *testing.T) {
	handler, generated := newStack(t, "good", false)
	w := request(t, handler, false)
	var result struct {
		Answer  string `json:"answer"`
		Sources []struct {
			ID       int      `json:"id"`
			Path     string   `json:"path"`
			Headings []string `json:"headings"`
			ChunkID  string   `json:"chunk_id"`
		} `json:"sources"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || result.Answer != "Found [1]." || generated.Load() != 1 {
		t.Fatalf("response: %d %s", w.Code, w.Body.String())
	}
	if len(result.Sources) != 1 || result.Sources[0].ID != 1 || result.Sources[0].Path != "guides/alpha.md" || strings.Join(result.Sources[0].Headings, " > ") != "Guide > Setup" || result.Sources[0].ChunkID == "" {
		t.Fatalf("missing/wrong retrieved provenance: %s", w.Body.String())
	}
}

func TestSSESourcesAndTerminalEvents(t *testing.T) {
	for _, mode := range []string{"good", "truncated", "malformed", "stream-error", "http-error"} {
		t.Run(mode, func(t *testing.T) {
			handler, _ := newStack(t, mode, false)
			w := request(t, handler, true)
			body := w.Body.String()
			if !strings.Contains(body, "event: sources\n") || !strings.Contains(body, `"path":"guides/alpha.md"`) {
				t.Errorf("no source event: %s", body)
			}
			if mode == "good" {
				if strings.Count(body, "event: done\n") != 1 || strings.Contains(body, "event: error\n") || !strings.Contains(body, "event: token\ndata: \"Found \"") {
					t.Fatalf("bad success framing: %s", body)
				}
			} else if strings.Count(body, "event: error\n") != 1 || strings.Contains(body, "event: done\n") {
				t.Fatalf("false completion after upstream failure: %s", body)
			}
		})
	}
}

func TestEmptyRetrievalNeverGenerates(t *testing.T) {
	handler, generated := newStack(t, "good", true)
	for _, stream := range []bool{false, true} {
		w := request(t, handler, stream)
		if !strings.Contains(w.Body.String(), "No relevant documents found.") {
			t.Fatal(w.Body.String())
		}
	}
	if generated.Load() != 0 {
		t.Fatalf("empty retrieval generated %d times", generated.Load())
	}
}

func TestClientCancellationReachesOllamaRequest(t *testing.T) {
	canceled := make(chan struct{})
	handler, _ := newStack(t, "cancel", false, canceled)
	server := httptest.NewServer(handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, strings.NewReader(`{"question":"How to install alpha?"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line == "event: token\n" {
			break
		}
	}
	cancel()
	resp.Body.Close()
	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("client cancellation did not cancel upstream generation")
	}
}

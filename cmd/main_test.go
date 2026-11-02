package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Quette420/docrag/internal/api"
	embedding "github.com/Quette420/docrag/internal/embedding/ollama"
	generation "github.com/Quette420/docrag/internal/generation/ollama"
	"github.com/Quette420/docrag/internal/index/inmemory"
	"github.com/Quette420/docrag/internal/model"
	query "github.com/Quette420/docrag/internal/query/service"
	store "github.com/Quette420/docrag/internal/storage/file"
)

func TestStartupCacheFreshness(t *testing.T) {
	var calls atomic.Int32
	var failEmbed, failTags atomic.Bool
	var digest atomic.Value
	digest.Store("digest-v1")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/tags":
			if failTags.Load() {
				http.Error(w, "unavailable", 503)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"models": []map[string]string{
				{"name": "model-a:latest", "digest": digest.Load().(string)},
				{"name": "model-b:latest", "digest": digest.Load().(string)},
			}})
		case "/api/embeddings":
			calls.Add(1)
			if failEmbed.Load() {
				http.Error(w, "embedding unavailable", 503)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"embedding": []float32{1, 0}})
		case "/api/generate":
			json.NewEncoder(w).Encode(map[string]any{"response": "Found [1]."})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	root := t.TempDir()
	writeDoc := func(root, name, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	readCache := func(path string) []byte {
		t.Helper()
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	cfg := config{docsDir: root, extensions: ".md,.txt", cacheFile: filepath.Join(t.TempDir(), "cache.json"),
		ollamaURL: upstream.URL, embedModel: "model-a", chunkSize: 100, chunkOverlap: 0}
	fs := store.NewFileStore()
	start := func(c config) ([]*model.Chunk, *inmemory.Indexer, error) {
		idx := inmemory.NewInMemoryIndexer()
		emb := embedding.NewOllamaEmbedder(c.ollamaURL, c.embedModel, 10000, 2)
		chunks, err := loadOrBuildIndex(context.Background(), c, fs, emb, idx)
		return chunks, idx, err
	}
	check := func(c config, want, absent, path string) []*model.Chunk {
		t.Helper()
		chunks, idx, err := start(c)
		if err != nil {
			t.Fatal(err)
		}
		var content strings.Builder
		for _, ch := range chunks {
			content.WriteString(ch.Content)
		}
		if !strings.Contains(content.String(), want) || absent != "" && strings.Contains(content.String(), absent) {
			t.Fatalf("stale startup content: %q; want %q without %q", content.String(), want, absent)
		}
		// Exercise the same retrieval/query/JSON handler as the server after restart.
		emb := embedding.NewOllamaEmbedder(c.ollamaURL, c.embedModel, 10000, 2)
		gen := generation.NewGenerator(c.ollamaURL, "fixture-generator")
		handler := api.NewQueryHandler(query.NewQueryService(emb, idx, gen, gen, 10, ""))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest("POST", "/query", strings.NewReader("{\"question\":\"fixture\"}")))
		var response struct {
			Sources []struct {
				Path     string   `json:"path"`
				Headings []string `json:"headings"`
			} `json:"sources"`
		}
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &response) != nil || len(response.Sources) == 0 {
			t.Fatalf("query after restart: %d %s", rec.Code, rec.Body.String())
		}
		for _, src := range response.Sources {
			if src.Path != path || len(src.Headings) == 0 {
				t.Fatalf("stale provenance: %+v, want %s and heading", src, path)
			}
		}
		return chunks
	}

	writeDoc(root, "guide.md", "# Guide\nalpha-v1")
	first := check(cfg, "alpha-v1", "", "guide.md")
	before := calls.Load()
	cache := readCache(cfg.cacheFile)
	warm, _, err := start(cfg)
	if err != nil || len(warm) != len(first) || warm[0].ID != first[0].ID || calls.Load() != before ||
		!bytes.Equal(cache, readCache(cfg.cacheFile)) {
		t.Fatalf("unchanged restart should reuse cache: %v", err)
	}

	info, err := os.Stat(filepath.Join(root, "guide.md"))
	if err != nil {
		t.Fatal(err)
	}
	writeDoc(root, "guide.md", "# Guide\nalpha-v2")
	if err := os.Chtimes(filepath.Join(root, "guide.md"), info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	check(cfg, "alpha-v2", "alpha-v1", "guide.md")
	if calls.Load() <= before+1 {
		t.Fatal("edited corpus reused old embeddings")
	}

	if err := os.Remove(filepath.Join(root, "guide.md")); err != nil {
		t.Fatal(err)
	}
	writeDoc(root, "new.md", "# New\nbeta-source")
	check(cfg, "beta-source", "alpha-v2", "new.md")
	rootB := t.TempDir()
	writeDoc(rootB, "new.md", "# New\nbeta-source")
	cfg.docsDir = rootB
	before = calls.Load()
	check(cfg, "beta-source", "", "new.md")
	if calls.Load() <= before+1 {
		t.Fatal("different root reused old embeddings")
	}

	cfg.embedModel = "model-b"
	before = calls.Load()
	check(cfg, "beta-source", "", "new.md")
	if calls.Load() <= before+1 {
		t.Fatal("different model reused old embeddings")
	}
	digest.Store("digest-v2")
	before = calls.Load()
	check(cfg, "beta-source", "", "new.md")
	if calls.Load() <= before+1 {
		t.Fatal("changed model digest reused old embeddings")
	}

	cache = readCache(cfg.cacheFile)
	failTags.Store(true)
	if _, _, err := start(cfg); err == nil {
		t.Fatal("model identity outage silently reused cache")
	}
	failTags.Store(false)
	if !bytes.Equal(cache, readCache(cfg.cacheFile)) {
		t.Fatal("identity failure replaced cache")
	}
	writeDoc(rootB, "new.md", "# New\ngamma-source")
	failEmbed.Store(true)
	if _, _, err := start(cfg); err == nil {
		t.Fatal("embedding failure ignored")
	}
	failEmbed.Store(false)
	if !bytes.Equal(cache, readCache(cfg.cacheFile)) {
		t.Fatal("embedding failure replaced cache")
	}
	missing := cfg
	missing.docsDir = filepath.Join(rootB, "missing")
	if _, _, err := start(missing); err == nil {
		t.Fatal("missing root reused cache")
	}
	if !bytes.Equal(cache, readCache(cfg.cacheFile)) {
		t.Fatal("scan failure replaced cache")
	}
	recovered := check(cfg, "gamma-source", "beta-source", "new.md")

	// Legacy arrays have provenance but no reliable identity; rebuild once.
	if err := fs.SaveChunks(cfg.cacheFile, recovered); err != nil {
		t.Fatal(err)
	}
	before = calls.Load()
	check(cfg, "gamma-source", "", "new.md")
	if calls.Load() <= before+1 {
		t.Fatal("unverified legacy cache reused")
	}

	// Deletion of the last document must also replace the index with empty data.
	if err := os.Remove(filepath.Join(rootB, "new.md")); err != nil {
		t.Fatal(err)
	}
	empty, idx, err := start(cfg)
	if err != nil || len(empty) != 0 {
		t.Fatalf("deleted last document: %v %v", empty, err)
	}
	found, err := idx.Search(context.Background(), []float32{1, 0}, 10)
	if err != nil || len(found) != 0 {
		t.Fatalf("deleted chunks survived: %v %v", found, err)
	}
	cache = readCache(cfg.cacheFile)
	before = calls.Load()
	empty, _, err = start(cfg)
	if err != nil || len(empty) != 0 || calls.Load() != before ||
		!bytes.Equal(cache, readCache(cfg.cacheFile)) {
		t.Fatalf("empty snapshot not reusable: %v", err)
	}
}

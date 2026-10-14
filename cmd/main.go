package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Quette420/docrag/internal/api"
	chunkService "github.com/Quette420/docrag/internal/chunk/service"
	docService "github.com/Quette420/docrag/internal/document/service"
	"github.com/Quette420/docrag/internal/document/service/source"
	embeddingOllama "github.com/Quette420/docrag/internal/embedding/ollama"
	generationOllama "github.com/Quette420/docrag/internal/generation/ollama"
	"github.com/Quette420/docrag/internal/index/inmemory"
	"github.com/Quette420/docrag/internal/model"
	queryService "github.com/Quette420/docrag/internal/query/service"
	store "github.com/Quette420/docrag/internal/storage/file"
)

type config struct {
	docsDir      string
	extensions   string
	cacheFile    string
	reindex      bool
	ollamaURL    string
	embedModel   string
	genModel     string
	chunkSize    int
	chunkOverlap int
	topK         int
	answerLang   string
	addr         string
	staticDir    string
}

func parseFlags() config {
	var c config
	flag.StringVar(&c.docsDir, "docs", "./docs", "directory with documents to index")
	flag.StringVar(&c.extensions, "ext", ".md,.markdown,.txt", "comma-separated list of file extensions to index")
	flag.StringVar(&c.cacheFile, "cache", "chunks_cache.json", "path to the chunk/embedding cache file")
	flag.BoolVar(&c.reindex, "reindex", false, "ignore the cache and rebuild the index")
	flag.StringVar(&c.ollamaURL, "ollama", "http://localhost:11434", "Ollama base URL")
	flag.StringVar(&c.embedModel, "embed-model", "nomic-embed-text", "Ollama embedding model")
	flag.StringVar(&c.genModel, "gen-model", "deepseek-r1:7b", "Ollama generation model")
	flag.IntVar(&c.chunkSize, "chunk-size", 500, "max chunk size in tokens")
	flag.IntVar(&c.chunkOverlap, "chunk-overlap", 50, "overlap between chunks in characters")
	flag.IntVar(&c.topK, "top-k", 5, "number of chunks passed to the model as context")
	flag.StringVar(&c.answerLang, "lang", "", "force answer language (e.g. English); empty = same language as the question")
	flag.StringVar(&c.addr, "addr", ":8080", "HTTP listen address")
	flag.StringVar(&c.staticDir, "static", "./static", "directory with frontend files")
	flag.Parse()
	return c
}

func main() {
	cfg := parseFlags()
	ctx := context.Background()

	fileStore := store.NewFileStore()
	embedder := embeddingOllama.NewOllamaEmbedder(cfg.ollamaURL, cfg.embedModel, 10, 4)
	indexer := inmemory.NewInMemoryIndexer()

	chunks, err := loadOrBuildIndex(ctx, cfg, fileStore, embedder, indexer)
	if err != nil {
		slog.Error("indexing failed", "err", err)
		os.Exit(1)
	}
	if len(chunks) == 0 {
		slog.Warn("index is empty: no documents found", "docs", cfg.docsDir, "ext", cfg.extensions)
	}

	generator := generationOllama.NewGenerator(cfg.ollamaURL, cfg.genModel)
	querySvc := queryService.NewQueryService(embedder, indexer, generator, generator, cfg.topK, cfg.answerLang)

	mux := http.NewServeMux()
	mux.Handle("/query", api.NewQueryHandler(querySvc))
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})
	mux.Handle("/", http.FileServer(http.Dir(cfg.staticDir)))

	slog.Info("server started", "addr", cfg.addr)
	if err := http.ListenAndServe(cfg.addr, mux); err != nil {
		slog.Error("server failed", "err", err)
		os.Exit(1)
	}
}

// loadOrBuildIndex fills the indexer from the cache file, or runs the full
// load -> chunk -> embed pipeline and writes a fresh cache.
func loadOrBuildIndex(ctx context.Context, cfg config, fs *store.FileStore, embedder *embeddingOllama.OllamaEmbedder, indexer *inmemory.Indexer) ([]*model.Chunk, error) {
	if !cfg.reindex {
		chunks, err := fs.LoadChunks(cfg.cacheFile)
		switch {
		case err == nil && len(chunks) > 0:
			if err := indexer.Store(ctx, chunks); err != nil {
				return nil, err
			}
			slog.Info("loaded chunks from cache", "count", len(chunks), "cache", cfg.cacheFile)
			return chunks, nil
		case err != nil && !os.IsNotExist(err):
			slog.Warn("failed to load cache, reindexing", "err", err)
		}
	}

	slog.Info("indexing documents", "docs", cfg.docsDir)
	start := time.Now()

	folderSource := source.NewFolderSource(cfg.docsDir, strings.Split(cfg.extensions, ","))
	chunker := chunkService.NewRecursiveChunker(cfg.chunkSize, cfg.chunkOverlap)
	exportService := docService.NewExportService(folderSource, chunker, embedder, indexer)

	chunks, err := exportService.Run(ctx)
	if err != nil {
		return nil, err
	}
	if err := fs.SaveChunks(cfg.cacheFile, chunks); err != nil {
		slog.Warn("failed to save cache", "err", err)
	}
	slog.Info("indexing finished", "chunks", len(chunks), "elapsed", time.Since(start))
	return chunks, nil
}

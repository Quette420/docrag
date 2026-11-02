package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Quette420/docrag/internal/document/service/source"
	embedding "github.com/Quette420/docrag/internal/embedding/ollama"
	"github.com/Quette420/docrag/internal/model"
)

// Startup indexes these exact loaded bytes, not a second read after hashing.
type loadedDocuments []*model.Document

func (docs loadedDocuments) LoadDocuments(ctx context.Context) ([]*model.Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return docs, nil
}

func corpusSnapshot(ctx context.Context, cfg config, embedder *embedding.OllamaEmbedder) (loadedDocuments, string, error) {
	root, err := filepath.Abs(cfg.docsDir)
	if err != nil {
		return nil, "", err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, "", fmt.Errorf("docs root: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, "", err
	}
	if !info.IsDir() {
		return nil, "", fmt.Errorf("docs root is not a directory: %s", root)
	}
	src := source.NewFolderSource(root, strings.Split(cfg.extensions, ","))
	docs, err := src.LoadDocuments(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("scan corpus: %w", err)
	}
	digest, err := embedder.ModelDigest(ctx)
	if err != nil {
		return nil, "", err
	}
	exts := make([]string, 0, len(src.Extensions))
	for ext := range src.Extensions {
		exts = append(exts, ext)
	}
	sort.Strings(exts)
	type documentHash struct{ Path, Hash string }
	files := make([]documentHash, 0, len(docs))
	for _, doc := range docs {
		files = append(files, documentHash{doc.SourcePath, doc.ContentHash})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	identity := struct {
		Version                       int
		Root, Endpoint, Model, Digest string
		Extensions                    []string
		ChunkSize, ChunkOverlap       int
		Documents                     []documentHash
	}{1, filepath.ToSlash(root), cfg.ollamaURL, cfg.embedModel, digest,
		exts, cfg.chunkSize, cfg.chunkOverlap, files}
	data, err := json.Marshal(identity)
	if err != nil {
		return nil, "", err
	}
	hash := sha256.Sum256(data)
	return docs, hex.EncodeToString(hash[:]), nil
}

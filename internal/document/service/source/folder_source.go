package source

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	"github.com/Quette420/docrag/internal/model"
)

// FolderSource recursively loads all files with the given extensions from a directory.
type FolderSource struct {
	BasePath   string
	Extensions map[string]bool
}

func NewFolderSource(basePath string, extensions []string) *FolderSource {
	exts := make(map[string]bool, len(extensions))
	for _, ext := range extensions {
		ext = strings.ToLower(strings.TrimSpace(ext))
		if ext == "" {
			continue
		}
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		exts[ext] = true
	}
	return &FolderSource{BasePath: basePath, Extensions: exts}
}

func (f *FolderSource) LoadDocuments(ctx context.Context) ([]*model.Document, error) {
	var docs []*model.Document

	err := filepath.WalkDir(f.BasePath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		ext := strings.ToLower(filepath.Ext(d.Name()))
		if d.IsDir() || !f.Extensions[ext] {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		fileInfo, err := d.Info()
		if err != nil {
			return err
		}

		// Store paths relative to the docs root so the cache is portable.
		relPath, err := filepath.Rel(f.BasePath, path)
		if err != nil {
			relPath = path
		}
		relPath = filepath.ToSlash(relPath)

		hash := sha256.Sum256(content)

		docs = append(docs, &model.Document{
			ID:          relPath,
			SourcePath:  relPath,
			FileName:    d.Name(),
			Ext:         ext,
			RawContent:  string(content),
			ModifiedAt:  fileInfo.ModTime(),
			SizeBytes:   fileInfo.Size(),
			ContentHash: hex.EncodeToString(hash[:]),
		})
		return nil
	})

	return docs, err
}

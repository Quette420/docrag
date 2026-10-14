package store

import (
	"encoding/json"
	"github.com/Quette420/docrag/internal/model"
	"os"
)

type FileStore struct{}

func NewFileStore() *FileStore {
	return &FileStore{}
}

func (fs *FileStore) SaveChunks(filename string, chunks []*model.Chunk) error {
	data, err := json.MarshalIndent(chunks, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filename, data, 0644)
}

func (fs *FileStore) LoadChunks(filename string) ([]*model.Chunk, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	var chunks []*model.Chunk
	if err := json.Unmarshal(data, &chunks); err != nil {
		return nil, err
	}
	return chunks, nil
}

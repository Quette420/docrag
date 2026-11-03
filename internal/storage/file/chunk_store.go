package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Quette420/docrag/internal/model"
)

const cacheVersion = 1

// Snapshot binds chunks and embeddings to the corpus/configuration fingerprint.
type Snapshot struct {
	Version     int            `json:"version"`
	Fingerprint string         `json:"fingerprint"`
	Chunks      []*model.Chunk `json:"chunks"`
}

type FileStore struct{}

func NewFileStore() *FileStore {
	return &FileStore{}
}

func (fs *FileStore) SaveChunks(filename string, chunks []*model.Chunk) error {
	data, err := json.MarshalIndent(chunks, "", "  ")
	if err != nil {
		return err
	}
	return writeCache(filename, data)
}

func (fs *FileStore) LoadChunks(filename string) ([]*model.Chunk, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	if bytes.HasPrefix(bytes.TrimSpace(data), []byte("{")) {
		snapshot, err := decodeSnapshot(data)
		if err != nil {
			return nil, err
		}
		return snapshot.Chunks, nil
	}
	var chunks []*model.Chunk
	if err := json.Unmarshal(data, &chunks); err != nil {
		return nil, err
	}
	return chunks, nil
}

func (fs *FileStore) SaveSnapshot(filename, fingerprint string, chunks []*model.Chunk) error {
	data, err := json.MarshalIndent(Snapshot{cacheVersion, fingerprint, chunks}, "", "  ")
	if err != nil {
		return err
	}
	return writeCache(filename, data)
}

func (fs *FileStore) LoadSnapshot(filename string) (*Snapshot, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	return decodeSnapshot(data)
}

func decodeSnapshot(data []byte) (*Snapshot, error) {
	var snapshot Snapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, err
	}
	if snapshot.Version != cacheVersion || snapshot.Fingerprint == "" {
		return nil, fmt.Errorf("cache has no supported corpus/model identity")
	}
	return &snapshot, nil
}

// Publish complete bytes in the destination directory. A failed encode/write
// leaves the previous cache intact. Rename atomicity is filesystem/OS dependent;
// this is not a power-loss durability guarantee.
func writeCache(filename string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(filename), ".docrag-cache-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filename)
}

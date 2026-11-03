package store

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/Quette420/docrag/internal/model"
)

func TestSnapshotCompatibilityAndFailedReplacement(t *testing.T) {
	fs := NewFileStore()
	path := filepath.Join(t.TempDir(), "cache.json")
	chunks := []*model.Chunk{{ID: "fixture", Content: "synthetic", Embedding: []float32{1, 0}}}
	if err := fs.SaveChunks(path, chunks); err != nil {
		t.Fatal(err)
	}
	if got, err := fs.LoadChunks(path); err != nil || len(got) != 1 {
		t.Fatalf("legacy: %v %v", got, err)
	}
	if _, err := fs.LoadSnapshot(path); err == nil {
		t.Fatal("legacy has no freshness identity")
	}
	if err := fs.SaveSnapshot(path, "fingerprint", chunks); err != nil {
		t.Fatal(err)
	}
	if got, err := fs.LoadSnapshot(path); err != nil || got.Fingerprint != "fingerprint" || len(got.Chunks) != 1 {
		t.Fatalf("snapshot: %+v %v", got, err)
	}
	if got, err := fs.LoadChunks(path); err != nil || len(got) != 1 {
		t.Fatalf("compatibility: %v %v", got, err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	chunks[0].Embedding[0] = float32(math.NaN())
	if err := fs.SaveSnapshot(path, "invalid", chunks); err == nil {
		t.Fatal("non-JSON embedding accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("failed replacement damaged cache: %v", err)
	}
	if files, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".docrag-cache-*")); err != nil || len(files) != 0 {
		t.Fatalf("temporary cache files leaked: %v %v", files, err)
	}
	if err := os.WriteFile(path, []byte("{\"version\":999,\"fingerprint\":\"unknown\",\"chunks\":[]}"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.LoadSnapshot(path); err == nil {
		t.Fatal("unknown cache version accepted")
	}
}

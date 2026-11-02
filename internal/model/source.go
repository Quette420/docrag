package model

// Source identifies one retrieved chunk in the context of a single answer.
// Empty Path means the cached chunk has no known file provenance.
type Source struct {
	ID       int      `json:"id"`
	Path     string   `json:"path"`
	Headings []string `json:"headings"`
	ChunkID  string   `json:"chunk_id"`
}

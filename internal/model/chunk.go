package model

import "time"

type Chunk struct {
	ID         string         `json:"id" bson:"_id"`
	DocumentID string         `json:"document_id" bson:"document_id"`
	Index      int            `json:"index" bson:"index"`
	Content    string         `json:"content" bson:"content"`
	Headings   []string       `json:"headings,omitempty" bson:"headings"`
	Embedding  []float32      `json:"embedding,omitempty" bson:"embedding"`
	Metadata   map[string]any `json:"metadata,omitempty" bson:"metadata"`
	CreatedAt  time.Time      `json:"created_at" bson:"created_at"`
	Tokens     int            `json:"tokens" bson:"tokens"`
}

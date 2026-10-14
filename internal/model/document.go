package model

import "time"

type Document struct {
	ID          string    `json:"id"`
	SourcePath  string    `json:"source_path"` // relative to the docs root, e.g. guides/setup.md
	FileName    string    `json:"file_name"`
	Ext         string    `json:"ext"` // .md
	RawContent  string    `json:"raw_content"`
	ModifiedAt  time.Time `json:"modified_at"`
	SizeBytes   int64     `json:"size_bytes"`
	ContentHash string    `json:"content_hash"` // sha256 of the content
}

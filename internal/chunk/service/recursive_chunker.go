package service

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/pkoukk/tiktoken-go"

	"github.com/Quette420/docrag/internal/model"
)

// RecursiveChunker splits Markdown documents into chunks, taking into account:
// - token count (not characters)
// - headings (a heading always starts a new chunk; the heading path is kept as metadata)
// - overlap between consecutive chunks
// - logical blocks (code fences, lists) which are never split
type RecursiveChunker struct {
	ChunkSize    int // max tokens per chunk
	ChunkOverlap int // overlap with the previous chunk, in characters
	enc          *tiktoken.Tiktoken
}

// NewRecursiveChunker creates a chunker.
// chunkSize is measured in tokens (e.g. 500), overlap in characters (e.g. 50).
func NewRecursiveChunker(chunkSize, overlap int) *RecursiveChunker {
	enc, err := tiktoken.GetEncoding("cl100k_base")
	if err != nil {
		// fallback, should not normally happen
		enc, _ = tiktoken.GetEncoding("gpt2")
	}
	return &RecursiveChunker{
		ChunkSize:    chunkSize,
		ChunkOverlap: overlap,
		enc:          enc,
	}
}

// SplitDocuments implements the Chunker interface.
func (c *RecursiveChunker) SplitDocuments(ctx context.Context, docs []*model.Document) ([]*model.Chunk, error) {
	var allChunks []*model.Chunk
	for _, doc := range docs {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		chunks := c.splitDocument(doc)
		allChunks = append(allChunks, chunks...)
	}
	return allChunks, nil
}

// splitDocument splits a single document.
func (c *RecursiveChunker) splitDocument(doc *model.Document) []*model.Chunk {
	lines := strings.Split(doc.RawContent, "\n")
	blocks := groupIntoBlocks(lines)

	var chunks []*model.Chunk
	var currentChunk strings.Builder
	var currentTokens int
	var currentHeadings []string
	var prevChunkContent string

	for _, block := range blocks {
		// A heading closes the current chunk and starts a new one
		if heading, level := isHeading(block); level > 0 {
			if currentTokens > 0 {
				ch := c.createChunk(doc, currentChunk.String(), currentTokens, currentHeadings, prevChunkContent, len(chunks))
				chunks = append(chunks, ch)
				prevChunkContent = ch.Content
				currentChunk.Reset()
				currentTokens = 0
			}
			currentHeadings = updateHeadings(currentHeadings, heading, level)
			currentChunk.WriteString(block)
			currentTokens = len(c.enc.Encode(block, nil, nil))
			continue
		}

		// Regular block: close the chunk first if it would exceed the limit
		blockTokens := len(c.enc.Encode(block, nil, nil))
		if currentTokens > 0 && currentTokens+blockTokens > c.ChunkSize {
			ch := c.createChunk(doc, currentChunk.String(), currentTokens, currentHeadings, prevChunkContent, len(chunks))
			chunks = append(chunks, ch)
			prevChunkContent = ch.Content
			currentChunk.Reset()
			currentTokens = 0
		}

		if currentTokens > 0 {
			currentChunk.WriteString("\n")
		}
		currentChunk.WriteString(block)
		currentTokens += blockTokens
	}

	if currentTokens > 0 {
		ch := c.createChunk(doc, currentChunk.String(), currentTokens, currentHeadings, prevChunkContent, len(chunks))
		chunks = append(chunks, ch)
	}

	return chunks
}

// createChunk builds a chunk, prepending the tail of the previous chunk as overlap.
func (c *RecursiveChunker) createChunk(doc *model.Document, content string, tokens int, headings []string, prevContent string, index int) *model.Chunk {
	finalContent := content
	if c.ChunkOverlap > 0 && prevContent != "" {
		// Slice by runes so multi-byte UTF-8 characters are never cut in half.
		prev := []rune(prevContent)
		overlapStart := max(len(prev)-c.ChunkOverlap, 0)
		finalContent = string(prev[overlapStart:]) + "\n" + content
	}

	finalTokens := len(c.enc.Encode(finalContent, nil, nil))

	return &model.Chunk{
		ID:         uuid.New().String(),
		DocumentID: doc.ID,
		Index:      index,
		Content:    finalContent,
		Headings:   copyHeadings(headings),
		Tokens:     finalTokens,
		Metadata: map[string]any{
			"source_path":  doc.SourcePath,
			"file_name":    filepath.Base(doc.SourcePath),
			"modified_at":  doc.ModifiedAt,
			"size_bytes":   doc.SizeBytes,
			"content_hash": doc.ContentHash,
		},
		CreatedAt: time.Now(),
	}
}

// ---------------------------------------------------------------------
// Markdown helpers
// ---------------------------------------------------------------------

// groupIntoBlocks groups lines into logical blocks:
// - code fences (``` ... ```) are kept intact
// - consecutive list items (-, *, +, "1.") form a single block
// - blank lines separate blocks and are otherwise dropped
func groupIntoBlocks(lines []string) []string {
	var blocks []string
	var currentBlock strings.Builder
	inCodeBlock := false
	inList := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "```") {
			if inCodeBlock {
				currentBlock.WriteString("\n" + line)
				blocks = append(blocks, currentBlock.String())
				currentBlock.Reset()
				inCodeBlock = false
				continue
			} else {
				if currentBlock.Len() > 0 {
					blocks = append(blocks, currentBlock.String())
					currentBlock.Reset()
				}
				inCodeBlock = true
				currentBlock.WriteString(line)
				continue
			}
		}
		if inCodeBlock {
			currentBlock.WriteString("\n" + line)
			continue
		}

		if trimmed == "" {
			if currentBlock.Len() > 0 && !inList {
				blocks = append(blocks, currentBlock.String())
				currentBlock.Reset()
				inList = false
			}
			continue
		}

		isItem := isListItem(trimmed)
		if isItem {
			if !inList && currentBlock.Len() > 0 {
				blocks = append(blocks, currentBlock.String())
				currentBlock.Reset()
			}
			inList = true
			if currentBlock.Len() > 0 {
				currentBlock.WriteString("\n")
			}
			currentBlock.WriteString(line)
			continue
		} else {
			if inList {
				blocks = append(blocks, currentBlock.String())
				currentBlock.Reset()
				inList = false
			}
			if currentBlock.Len() > 0 {
				currentBlock.WriteString("\n")
			}
			currentBlock.WriteString(line)
		}
	}

	if currentBlock.Len() > 0 {
		blocks = append(blocks, currentBlock.String())
	}
	return blocks
}

// isListItem reports whether the line starts with a list marker.
func isListItem(s string) bool {
	if len(s) == 0 {
		return false
	}
	if s[0] == '-' || s[0] == '*' || s[0] == '+' {
		return true
	}
	if len(s) > 1 && s[0] >= '0' && s[0] <= '9' && s[1] == '.' {
		return true
	}
	return false
}

// isHeading reports whether the line is a Markdown ATX heading.
// It returns the heading text (without #) and its level, or "", 0.
func isHeading(line string) (string, int) {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "#") {
		level := 0
		for _, ch := range trimmed {
			if ch == '#' {
				level++
			} else {
				break
			}
		}
		if level > 0 && len(trimmed) > level && trimmed[level] == ' ' {
			return strings.TrimSpace(trimmed[level:]), level
		}
	}
	return "", 0
}

// updateHeadings updates the heading path for a heading of the given level.
func updateHeadings(headings []string, heading string, level int) []string {
	if level <= len(headings) {
		headings[level-1] = heading
		headings = headings[:level]
	} else {
		headings = append(headings, heading)
	}
	return headings
}

// copyHeadings returns a copy of the heading path.
func copyHeadings(src []string) []string {
	dst := make([]string, len(src))
	copy(dst, src)
	return dst
}

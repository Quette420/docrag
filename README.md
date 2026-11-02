# DocRAG

A small, dependency-light Retrieval-Augmented Generation (RAG) server in Go.
Point it at a folder of Markdown/text documentation and ask questions about it
through a web UI or HTTP API. Everything runs locally on top of [Ollama](https://ollama.com).

## Features

- **Markdown-aware chunking**: chunks are sized by tokens (tiktoken `cl100k_base`), a heading
  always starts a new chunk, code fences and lists are never split, and every chunk keeps its
  heading path (`Guide > Install > Linux`) for better retrieval context.
- **Concurrent embedding** with a worker pool and rate limiter.
- **In-memory vector index** with cosine similarity search.
- **Embedding cache**: chunks and embeddings are reused only when the corpus and
  indexing configuration still match the saved fingerprint.
- **Streaming answers** over Server-Sent Events, plus a plain JSON mode.
- **Pluggable components**: source, chunker, embedder, index and generator are small
  interfaces, so it's easy to swap in another vector DB or LLM provider.

## Architecture

```
                 indexing                                   querying
┌──────────────┐   ┌─────────┐   ┌──────────┐   ┌───────┐   ┌─────────┐   ┌───────────┐
│ FolderSource │ → │ Chunker │ → │ Embedder │ → │ Index │ ← │ Query   │ → │ Generator │ → SSE / JSON
└──────────────┘   └─────────┘   └──────────┘   └───────┘   │ Service │   └───────────┘
                                                    ↕       └─────────┘
                                              cache (JSON)
```

| Package | Responsibility |
|---|---|
| `internal/document/service/source` | Recursively loads documents from a directory |
| `internal/chunk/service` | Markdown-aware recursive chunker |
| `internal/embedding/ollama` | Embeddings via Ollama `/api/embeddings` |
| `internal/index/inmemory` | In-memory vector index (cosine similarity) |
| `internal/generation/ollama` | Text generation via Ollama `/api/generate` (regular and streaming) |
| `internal/query/service` | Retrieval + prompt assembly |
| `internal/storage/file` | JSON cache of chunks and embeddings |
| `internal/api` | HTTP handler for `/query` |

## Quick start

1. Install [Ollama](https://ollama.com) and pull the models:

   ```sh
   ollama pull nomic-embed-text
   ollama pull deepseek-r1:7b
   ```

2. Run the server against your documentation folder:

   ```sh
   go run ./cmd -docs /path/to/your/docs
   ```

3. Open http://localhost:8080 and ask a question.

The first start indexes all documents and writes `chunks_cache.json`. On every start,
DocRAG reads the selected corpus and checks content hashes, the canonical docs root,
extensions, chunk size/overlap, Ollama endpoint and embedding model name/content digest.
The digest is resolved from Ollama's [`GET /api/tags`](https://docs.ollama.com/api/tags),
so replacing a model under the same tag also invalidates the cache.
An unchanged snapshot skips document embedding; changed inputs trigger a full rebuild.
`-reindex` forces rebuilding even when the fingerprint matches.

The cache now stores a versioned JSON envelope containing the fingerprint and chunks.
Legacy chunk-array caches are rebuilt once because they have no trustworthy configuration
identity. Cache writes use a temporary file in the destination directory, then rename;
failed scans, model-identity checks or embedding calls do not replace the previous cache.
Startup fails on those errors rather than serving an unverifiable stale index.
Ollama must be available for model identity verification even on a warm start.
Keep the cache outside the selected input corpus.

The snapshot describes the bytes read during startup, not a live filesystem transaction:
there is no file watcher or automatic refresh while the server runs. Restart after editing
documents. Rename atomicity depends on the filesystem/OS; power-loss durability and
concurrent writers to the same cache file are not guaranteed.

## Configuration

| Flag | Default | Description |
|---|---|---|
| `-docs` | `./docs` | Directory with documents to index |
| `-ext` | `.md,.markdown,.txt` | File extensions to index |
| `-cache` | `chunks_cache.json` | Path to the chunk/embedding cache |
| `-reindex` | `false` | Ignore the cache and rebuild the index |
| `-ollama` | `http://localhost:11434` | Ollama base URL |
| `-embed-model` | `nomic-embed-text` | Embedding model |
| `-gen-model` | `deepseek-r1:7b` | Generation model |
| `-chunk-size` | `500` | Max chunk size, in tokens |
| `-chunk-overlap` | `50` | Overlap between chunks, in characters |
| `-top-k` | `5` | Number of chunks passed to the model as context |
| `-lang` | _(empty)_ | Force the answer language (e.g. `English`); by default the model answers in the language of the question |
| `-addr` | `:8080` | HTTP listen address |
| `-static` | `./static` | Directory with frontend files |

## HTTP API

### `POST /query`

```sh
curl -X POST localhost:8080/query -d '{"question": "How do I configure logging?"}'
```

```json
{"answer": "... [1]", "elapsed": 1234567890, "sources": [{"id": 1, "path": "guides/setup.md", "headings": ["Guide", "Install"], "chunk_id": "..."}]}
```

`elapsed` is in nanoseconds. `sources` is an additive field containing the
chunks actually retrieved for this answer, including file paths relative to
the documentation root and their heading paths. IDs match the `[1]`, `[2]`, …
labels supplied in the generation prompt and remain fixed during that answer.
An empty path explicitly means that a legacy cached chunk has no known path;
paths are never reconstructed from model output. A retrieved source list is
not proof that every generated claim is correct or cited correctly.

With `Accept: text/event-stream` the answer is streamed as Server-Sent Events.
An additive `sources` event sends the JSON source array once, before any tokens:

```text
event: sources
data: [{"id":1,"path":"guides/setup.md","headings":["Guide","Install"],"chunk_id":"..."}]
```

The existing `token`, `done` and `error` payloads remain JSON-encoded strings:

```
event: token
data: "Hello"

event: token
data: " world"

event: done
data: ""
```

On failure an `error` event is sent instead of `done`. Malformed or truncated
Ollama streams and upstream error objects are failures, not successful completion.
Client cancellation cancels the upstream request; a disconnected client receives
no completion event. Empty retrieval returns the existing no-results message
with `sources: []` and never calls the generator.

### `GET /health`

Returns `200 OK`.

## Development checks

```sh
go test -race ./...
go vet ./...
go build -o docrag-server ./cmd
```

HTTP integration tests use a temporary synthetic corpus and a deterministic fake
Ollama HTTP endpoint with the real folder source, tokenizer, chunker, embedding
and generation adapters, JSON cache/reload, in-memory index, retrieval and handler.
They cover source provenance, JSON/SSE compatibility, empty retrieval, upstream
failures and client cancellation. Startup tests additionally cover unchanged cache reuse,
content edits with unchanged size/mtime, add/delete/root/model/digest changes, legacy
cache migration and failures followed by recovery with the old cache preserved.
These checks do not require a model or GPU and
do not measure real-model answer quality. Go modules and the tokenizer's encoding
data may require a first-run download.

## Roadmap

- Source references (file + heading) are returned with JSON and streaming answers
- Startup cache freshness is checked against the corpus and embedding configuration
- Incremental reindexing based on content hashes
- Persistent vector DB backend (e.g. Qdrant)
- Hybrid (BM25 + vector) retrieval

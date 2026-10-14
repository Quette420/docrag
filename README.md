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
- **Embedding cache**: the index is saved to a JSON file and reused on the next start.
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

The first start indexes all documents and writes `chunks_cache.json`; subsequent starts load
the cache. Use `-reindex` after the documents change or when switching to another folder or
embedding model.

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
{"answer": "...", "elapsed": 1234567890}
```

`elapsed` is in nanoseconds.

With `Accept: text/event-stream` the answer is streamed as Server-Sent Events. Every `data`
payload is a JSON-encoded string:

```
event: token
data: "Hello"

event: token
data: " world"

event: done
data: ""
```

On failure an `error` event is sent instead of `done`.

### `GET /health`

Returns `200 OK`.

## Roadmap

- Return source references (file + heading) with each answer
- Incremental reindexing based on content hashes
- Persistent vector DB backend (e.g. Qdrant)
- Hybrid (BM25 + vector) retrieval

<div align="center">

# superkb

**Self-hosted knowledge-base RAG engine for AI operators.**

Upload raw documents, group them into knowledge bases, build immutable RAG
snapshots, and search them with source citations — your infrastructure, your
data, your rules.

</div>

---

superkb is a [Clean Architecture](#architecture) Go service that turns piles of
documents into searchable, citeable knowledge bases. Raw files live in
S3-compatible object storage; vectorization and retrieval are owned by
[Hindsight](https://github.com/vectorize-io/hindsight). Each knowledge base is
built into an immutable snapshot, so switching versions is an instant rollback.

## Core model

- **Document** — a raw uploaded file (PDF, DOCX, images, text, …). Stored in
  object storage only. Uploading does **not** vectorize anything; it is plain
  storage + metadata.
- **Knowledge base (KB)** — a named group of documents.
- **Build** — an immutable RAG snapshot of a KB's documents. A build provisions
  a dedicated Hindsight *bank* and ingests every member document (convert →
  chunk → extract → vectorize). Runs asynchronously on a background worker; poll
  status until `ready`. Each build has its own bank.
- **Enable** — points a KB's search at one ready build. A KB is searchable only
  when it is enabled **and** has an active build.
- **Instant rollback** — every build's bank persists independently. Enabling a
  previous build flips search back with no re-processing.

```
Upload doc ──▶ object storage (raw bytes) + Postgres (metadata)
                    │
Add to KB ──▶ kb_documents membership
                    │
Build KB  ──▶ new Hindsight bank ──▶ ingest all member docs ──▶ build = ready
                    │
Enable    ──▶ kb.active_build = build  (instant; previous builds stay intact)
                    │
Search    ──▶ Hindsight recall(active build's bank) + source citations
```

## Features

- **Two upload modes** — JSON (`{title, content}`) or `multipart/form-data`
  (real files up to 100 MB; converted to text server-side).
- **Async builds** — heavy ingestion runs on a background worker behind a
  swappable queue port; the API never blocks.
- **Instant rollback** — switch the active build to any previous ready snapshot.
- **Source citations** — opt-in references return the source filename, the
  **page** an answer was found on, and a public file link, plus the raw chunk
  text for client-side highlighting.
- **Self-hosted** — Postgres + object storage + Hindsight, all under your
  control. No data leaves your servers.
- **HTTP basic auth** — single service credential for service-to-service use.

## Architecture

Clean Architecture — dependencies point inward only:

```
cmd/api                  entrypoint + wiring + graceful shutdown
internal/
  domain/                entities (Document, KnowledgeBase, Build) + ports
  usecase/               document + knowledge-base logic, build worker, references
  delivery/http/         chi router, handlers, DTOs, basic auth, error mapping
  infra/
    postgres/            metadata repositories (no vectors)
    s3store/             S3/R2-compatible raw document storage
    hindsight/           RAG indexer adapter (bank/retain/recall + file upload)
    extract/             pdftotext-based per-page text extractor
  config/                env-based configuration
```

Postgres holds **metadata only** — vectorization is owned entirely by Hindsight.
Builds run on an in-process worker (`usecase.BuildWorker` draining a
`ChannelBuildQueue`); the queue is a `domain.BuildQueue` port, swappable for a
durable queue (Redis, SQS) without touching the usecase.

## Stack

- Go 1.23, chi router
- PostgreSQL (metadata)
- S3-compatible object storage (Cloudflare R2, MinIO, AWS S3, …)
- [Hindsight](https://github.com/vectorize-io/hindsight) — RAG: conversion,
  chunking, embeddings, multi-strategy recall. LLM + embeddings are pluggable
  (OpenAI-compatible gateways, local models, etc.).
- `poppler-utils` (`pdftotext`) for per-page source extraction.

## Standalone Hindsight on EasyPanel

Run Hindsight as its own service (no superkb engine required for the RAG
piece). Use `Dockerfile.hindsight` as the build context and mount a persistent
volume at `/home/hindsight/.pg0` to keep banks across redeploys. Env reference:
`.env.hindsight.example`. Pair it with Hermes by setting `HINDSIGHT_BASE_URL`
on the Hermes service (see `hermes-dockerize/.env.example`).

## Quick start

```bash
cp .env.example .env        # fill in your storage, Hindsight, and auth settings
make docker-up              # postgres + hindsight + superkb
make test                   # run unit tests
```

## MCP server

SuperKB also ships an MCP server (`cmd/mcp`) that exposes the same operations as
MCP tools over stdio, for agent clients. It reuses the same `.env` as the API
(Postgres, storage, Hindsight, OCR).

```bash
go run ./cmd/mcp            # serves MCP over stdio
```

Example client config (stdio):

```json
{
  "mcpServers": {
    "superkb": {
      "command": "go",
      "args": ["run", "./cmd/mcp"],
      "cwd": "/path/to/superkb"
    }
  }
}
```

Tools:
- Documents: `list_documents`, `get_document`, `get_document_source`, `upload_document`, `delete_document`
- Knowledge bases: `list_knowledge_bases`, `get_knowledge_base`, `create_knowledge_base`, `delete_knowledge_base`
- Membership: `add_document_to_knowledge_base`, `remove_document_from_knowledge_base`
- Builds: `build_knowledge_base`, `list_builds`, `enable_knowledge_base_build`, `disable_knowledge_base`, `process_build`
- Search: `search_knowledge_base`
- Memory: `retain_experience`, `curate_memory`, `submit_memory_feedback`

The API listens on `:8080`. See `.env.example` for every setting and
`examples/` for a runnable curl walkthrough and a Postman collection.

> **Configuration** lives entirely in environment variables — never commit real
> secrets. `.env` is gitignored; only `.env.example` (with placeholders) is
> tracked.

## API

| Method | Path                                                  | Description                         |
|--------|-------------------------------------------------------|-------------------------------------|
| GET    | `/healthz`                                            | Liveness (open)                     |
| POST   | `/api/v1/documents`                                   | Upload a document (JSON or file)    |
| GET    | `/api/v1/documents`                                   | List documents                     |
| GET    | `/api/v1/documents/{id}`                              | Get document metadata               |
| GET    | `/api/v1/documents/{id}/source`                       | Extracted text (per page) + file link |
| DELETE | `/api/v1/documents/{id}`                              | Delete document (storage + metadata)|
| POST   | `/api/v1/knowledge-bases`                             | Create a knowledge base             |
| GET    | `/api/v1/knowledge-bases`                             | List knowledge bases                |
| GET    | `/api/v1/knowledge-bases/{id}`                        | Get a knowledge base                |
| DELETE | `/api/v1/knowledge-bases/{id}`                        | Delete KB (+ its banks)             |
| PUT    | `/api/v1/knowledge-bases/{id}/documents/{docID}`      | Add document to KB                  |
| DELETE | `/api/v1/knowledge-bases/{id}/documents/{docID}`      | Remove document from KB             |
| POST   | `/api/v1/knowledge-bases/{id}/builds`                 | Build a RAG snapshot (async)        |
| GET    | `/api/v1/knowledge-bases/{id}/builds`                 | List builds (for rollback)          |
| POST   | `/api/v1/knowledge-bases/{id}/enable`                 | Enable a build for search           |
| POST   | `/api/v1/knowledge-bases/{id}/disable`                | Disable search                      |
| POST   | `/api/v1/knowledge-bases/{id}/search`                 | Search the active build             |
| POST   | `/api/v1/knowledge-bases/{id}/experiences`            | Retain agent/user feedback experience into active build |
| PATCH  | `/api/v1/knowledge-bases/{id}/memories/{memoryID}`    | Curate a Hindsight memory unit (edit/invalidate/restore) |
| POST   | `/api/v1/knowledge-bases/{id}/memories/{memoryID}/feedback` | Vote on a correction; 2 approvals apply it |

All `/api/v1` routes require HTTP basic auth (`AUTH_*` in `.env`). `/healthz` is open.

### Example flow

```bash
AUTH='-u <user>:<pass>'      # your AUTH_USERNAME / AUTH_PASSWORD
B=http://localhost:8080/api/v1

# 1. Upload — JSON or a real file
d1=$(curl -s $AUTH -X POST $B/documents -H 'Content-Type: application/json' \
  -d '{"title":"Handbook","content":"vacation policy is 20 days"}' | jq -r .id)
# or: curl -s $AUTH -X POST $B/documents -F 'file=@report.pdf' -F 'title=Report'

# 2. Create a KB and add the document
kb=$(curl -s $AUTH -X POST $B/knowledge-bases -H 'Content-Type: application/json' \
  -d '{"name":"HR"}' | jq -r .id)
curl $AUTH -X PUT $B/knowledge-bases/$kb/documents/$d1

# 3. Build a snapshot (async → pending build)
build=$(curl -s $AUTH -X POST $B/knowledge-bases/$kb/builds | jq -r .id)

# 4. Poll until ready
curl -s $AUTH $B/knowledge-bases/$kb/builds | jq '.builds[] | {id,status}'

# 5. Enable it
curl $AUTH -X POST $B/knowledge-bases/$kb/enable \
  -H 'Content-Type: application/json' -d "{\"build_id\":\"$build\"}"

# 6. Search — with source citations
curl $AUTH -X POST $B/knowledge-bases/$kb/search -H 'Content-Type: application/json' \
  -d '{"query":"how many vacation days?","top_k":5,"include_references":true}'

# 7. Retain human feedback / agent action as experience memory
curl $AUTH -X POST $B/knowledge-bases/$kb/experiences -H 'Content-Type: application/json' \
  -d '{"content":"User accepted the vacation policy answer.","context":"human feedback","tags":["feedback","accepted"]}'

# 8. Curate a cited memory unit (edit, invalidate, or restore)
curl $AUTH -X PATCH $B/knowledge-bases/$kb/memories/<memory_id> -H 'Content-Type: application/json' \
  -d '{"text":"Corrected memory text","state":"active"}'

# 9. Human consensus correction (2 approvals with same proposed_text applies it)
curl $AUTH -X POST $B/knowledge-bases/$kb/memories/<memory_id>/feedback -H 'Content-Type: application/json' \
  -d '{"reviewer":"alice","vote":"approve","proposed_text":"Corrected memory text"}'
curl $AUTH -X POST $B/knowledge-bases/$kb/memories/<memory_id>/feedback -H 'Content-Type: application/json' \
  -d '{"reviewer":"bob","vote":"approve","proposed_text":"Corrected memory text"}'

# 10. Instant rollback: enable any previous ready build
curl $AUTH -X POST $B/knowledge-bases/$kb/enable \
  -H 'Content-Type: application/json' -d '{"build_id":"<previous-build-id>"}'
```

### Source citations

With `"include_references": true`, each search result carries the data a UI
needs to cite and highlight the answer:

```json
{
  "document_id": "…",
  "content": "…the extracted fact…",
  "context": "Handbook.pdf",
  "entities": ["…"],
  "chunk_text": "…the source passage to highlight…",
  "filename": "Handbook.pdf",
  "page": 1,
  "file_url": "https://<your-public-domain>/documents/<id>"
}
```

Pair it with `GET /documents/{id}/source` (per-page text) to render the original
document and scroll/highlight to the cited `chunk_text` on the reported `page`.
`file_url` is built from `S3_PUBLIC_BASE_URL`; leave it unset to omit links.

## Testing

```bash
make test         # in-memory fakes + httptest; no live Hindsight/Postgres/storage needed
make test-cover
```

Build orchestration, enable/disable, search gating, instant rollback, page
lookup, and the Hindsight adapter are all covered by unit tests.

## Notes

- Builds run **asynchronously**; poll `GET .../builds` until `ready`, then
  `enable`. Tune with `WORKER_CONCURRENCY` / `WORKER_QUEUE_SIZE`. The default
  queue is in-process; swap `domain.BuildQueue` for a durable queue to survive
  restarts.
- Page numbers are derived by matching chunk text against `pdftotext`-extracted
  pages. Text PDFs resolve cleanly; scanned/image-only PDFs may report page `0`.
- **Scanned PDFs / images (OCR).** By default Hindsight converts files with
  markitdown, which cannot OCR scanned PDFs or images (no tesseract in the
  image) and yields empty text. Set the vision-LLM OCR knobs to extract text in
  superkb *before* retain: `VISION_OCR_API_KEY`, `VISION_OCR_BASE_URL`
  (OpenAI-compatible, e.g. 9router), `VISION_OCR_MODEL` (e.g.
  `minimax/MiniMax-VL-01`). PDFs are rasterized to PNG per page (`pdftoppm`,
  bundled) and sent one image per chat call; images are sent directly.
  `VISION_OCR_MAX_PAGES` caps pages per document. OCR runs only at build time;
  leave the key/model empty to disable and fall back to markitdown. Text and
  office documents are passed through untouched.
- Consolidated multi-source facts have no single source chunk and omit
  per-document reference fields.
- **Search latency (sub-500ms).** KB search cost is dominated by two Hindsight
  knobs, both set for the fast path in `.env` / `docker-compose.yml`:
  - **Embeddings** — `HINDSIGHT_API_EMBEDDINGS_PROVIDER=local` (built-in 384-dim
    model, ~50-100ms query embed, no key/WAN hop). Remote providers
    (`google`/`openai`) add ~300ms-3s per query; only switch for higher
    retrieval quality, and note that changing provider changes the vector
    dimension (local=384, gemini=768, openai=1536) and **requires rebuilding
    every bank**.
  - **Reranker** — `HINDSIGHT_API_RERANKER_PROVIDER=flashrank` with
    `ms-marco-TinyBERT-L-2-v2`. Reranking is the single largest search cost;
    TinyBERT-L-2 scores candidates in ~0.25-0.35s on CPU vs ~3.5s
    (local MiniLM-L-6) or ~6.6s (FlashRank MiniLM-L-12). These are recall-only
    knobs — changing them needs only a Hindsight restart, never a rebuild.
  - With both defaults, end-to-end search is ~0.45-0.50s (query embed ~0.1s +
    rerank ~0.3s + retrieval/overhead). For higher rerank quality at some
    latency cost, use a cloud reranker (`cohere`/`zeroentropy`/`siliconflow`)
    or run a local cross-encoder on GPU.
  - Gotcha: `HINDSIGHT_API_EMBEDDINGS_GEMINI_OUTPUT_DIMENSIONALITY` is
    int-parsed and must be **omitted** (not set empty) unless `PROVIDER=google`.

---

<div align="center">

Built by **[Incredible Zetta](https://github.com/incredible-zetta)** in
partnership with **[PT Cipta Dua Saudara — CDS.ID](https://github.com/cds-id)**.

🌐 [ciptadusa.com](https://ciptadusa.com) · 📧 [info@ciptadusa.com](mailto:info@ciptadusa.com)

<sub>Self-hosted first · Agent-native · Single-image deploys</sub>

</div>

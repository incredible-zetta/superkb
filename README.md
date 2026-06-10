# superkb

Document store with **knowledge bases** powered by RAG. Upload raw documents to
object storage (R2), group them into knowledge bases, build immutable RAG
snapshots via [Hindsight](https://github.com/vectorize-io/hindsight), and search
the active snapshot. Switching snapshots is an instant rollback.

## Core model

- **Document** — a raw uploaded file. Stored in object storage (R2) only.
  Uploading does **not** vectorize anything; it is plain storage + metadata.
- **Knowledge base (KB)** — a named group of documents.
- **Build** — an immutable RAG snapshot of a KB's documents at build time. A
  build provisions a dedicated Hindsight *bank* and `retain`s every member
  document into it (chunk → extract → vectorize). Runs asynchronously on a
  background worker; poll build status until `ready`. Each build has its own bank.
- **Enable** — points a KB's search at one ready build. A KB is searchable only
  when it is enabled **and** has an active build.
- **Instant rollback** — every build's bank persists independently. Switching
  the active build (Enable with a previous build id) flips search back with no
  re-processing.

```
Upload doc ──▶ R2 (raw bytes) + Postgres (metadata)
                    │
Add to KB ──▶ kb_documents membership
                    │
Build KB  ──▶ new Hindsight bank ──▶ retain all member docs ──▶ build = ready
                    │
Enable    ──▶ kb.active_build = build  (instant; previous builds stay intact)
                    │
Search    ──▶ Hindsight recall(active build's bank)
```

## Architecture

Clean Architecture — dependencies point inward only:

```
cmd/api                  entrypoint + wiring + graceful shutdown
internal/
  domain/                entities (Document, KnowledgeBase, Build) + ports
  usecase/               DocumentUseCase, KnowledgeBaseUseCase (build/enable/rollback/search)
  delivery/http/         chi router, handlers, DTOs, error mapping
  infra/
    postgres/            metadata repositories (no vectors)
    s3store/             S3/R2-compatible raw document storage
    hindsight/           RAG indexer adapter (createBank/retain/recall/deleteBank)
  config/                env-based configuration
```

Postgres holds **metadata only**. Vectorization is owned entirely by Hindsight.
Builds run on an in-process background worker (`usecase.BuildWorker` draining a
`ChannelBuildQueue`); the queue is a `domain.BuildQueue` port, swappable for a
durable queue (Redis, SQS) without touching the usecase.

## Stack

- Go 1.23, chi router
- PostgreSQL (metadata)
- S3-compatible object storage — Cloudflare R2 in production, MinIO for dev
- Hindsight (RAG: extraction, chunking, embeddings, multi-strategy recall)

## Quick start

```bash
cp .env.example .env        # set S3/R2 creds; OPENAI_API_KEY for hindsight
export OPENAI_API_KEY=sk-xxx
make docker-up              # postgres + minio + hindsight
make test                   # run unit tests
make run                    # start API on :8080
```

## API

| Method | Path                                                  | Description                         |
|--------|-------------------------------------------------------|-------------------------------------|
| GET    | `/healthz`                                            | Liveness                            |
| POST   | `/api/v1/documents`                                   | Upload a raw document               |
| GET    | `/api/v1/documents`                                   | List documents                      |
| GET    | `/api/v1/documents/{id}`                              | Get document metadata               |
| DELETE | `/api/v1/documents/{id}`                              | Delete document (R2 + metadata)     |
| POST   | `/api/v1/knowledge-bases`                             | Create a knowledge base             |
| GET    | `/api/v1/knowledge-bases`                             | List knowledge bases                |
| GET    | `/api/v1/knowledge-bases/{id}`                        | Get a knowledge base                |
| DELETE | `/api/v1/knowledge-bases/{id}`                        | Delete KB (+ its banks)             |
| PUT    | `/api/v1/knowledge-bases/{id}/documents/{docID}`      | Add document to KB                  |
| DELETE | `/api/v1/knowledge-bases/{id}/documents/{docID}`      | Remove document from KB             |
| POST   | `/api/v1/knowledge-bases/{id}/builds`                 | Build a RAG snapshot                |
| GET    | `/api/v1/knowledge-bases/{id}/builds`                 | List builds (for rollback)          |
| POST   | `/api/v1/knowledge-bases/{id}/enable`                 | Enable a build for search           |
| POST   | `/api/v1/knowledge-bases/{id}/disable`                | Disable search                      |
| POST   | `/api/v1/knowledge-bases/{id}/search`                 | Search the active build             |

### Example flow

All `/api/v1` endpoints require HTTP basic auth (see `AUTH_*` in `.env`). Pass
`-u user:pass` to curl. `/healthz` stays open.

```bash
AUTH='-u superkb:change-me'

# 1a. Upload a raw document as JSON (plain text content)
d1=$(curl -s $AUTH -X POST localhost:8080/api/v1/documents \
  -H 'Content-Type: application/json' \
  -d '{"title":"Handbook","content":"vacation policy is 20 days"}' | jq -r .id)

# 1b. Or upload a real file via multipart (PDF, DOCX, images, etc.)
curl -s $AUTH -X POST localhost:8080/api/v1/documents \
  -F 'file=@report.pdf' \
  -F 'title=Quarterly Report' \
  -F 'metadata={"source":"finance"}'

# 2. Create a knowledge base and add the document
kb=$(curl -s $AUTH -X POST localhost:8080/api/v1/knowledge-bases \
  -H 'Content-Type: application/json' \
  -d '{"name":"HR"}' | jq -r .id)
curl $AUTH -X PUT localhost:8080/api/v1/knowledge-bases/$kb/documents/$d1

# 3. Build a RAG snapshot (async: returns a pending build immediately)
build=$(curl -s $AUTH -X POST localhost:8080/api/v1/knowledge-bases/$kb/builds | jq -r .id)

# 4. Poll until the build is ready (worker processes it in the background)
curl -s $AUTH localhost:8080/api/v1/knowledge-bases/$kb/builds | jq '.builds[] | {id,status}'

# 5. Enable the build for search
curl $AUTH -X POST localhost:8080/api/v1/knowledge-bases/$kb/enable \
  -H 'Content-Type: application/json' -d "{\"build_id\":\"$build\"}"

# 6. Search
curl $AUTH -X POST localhost:8080/api/v1/knowledge-bases/$kb/search \
  -H 'Content-Type: application/json' -d '{"query":"how many vacation days?","top_k":5}'

# 7. Instant rollback: re-enable any previous ready build
curl -X POST localhost:8080/api/v1/knowledge-bases/$kb/enable \
  -H 'Content-Type: application/json' -d '{"build_id":"<previous-build-id>"}'
```

## Configuration

Environment variables — see `.env.example`. Key vars: `POSTGRES_DSN`, `S3_*`
(point at R2 in production), `HINDSIGHT_BASE_URL`, `HINDSIGHT_API_KEY`.

## Testing

```bash
make test         # usecase, delivery, and hindsight adapter use in-memory fakes / httptest
make test-cover
```

Build orchestration, enable/disable, search gating, and instant rollback are all
covered by `internal/usecase` tests with a fake RAG indexer — no live Hindsight,
Postgres, or R2 required.

## Notes

- `POST /api/v1/documents` accepts **both** JSON (`{title, content, metadata}`)
  and `multipart/form-data` (`file` part + optional `title` / `metadata`
  fields). Multipart is capped at 100 MB per request.
- All `/api/v1` routes are protected by **HTTP basic auth** (single service
  credential). Set `AUTH_ENABLED=false` to disable for local dev. `/healthz`
  is always open.
- Builds run **asynchronously** on a background worker. `POST .../builds`
  returns a `pending` build; poll `GET .../builds` until `status` is `ready`,
  then `enable` it. Tune the worker with `WORKER_CONCURRENCY` / `WORKER_QUEUE_SIZE`.
  The default queue is in-process; swap `domain.BuildQueue` for a durable queue
  to survive restarts.

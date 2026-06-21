package postgres

import (
	"context"
	"fmt"
	"runtime"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPool opens a pgx connection pool and verifies connectivity.
//
// The pool is explicitly sized rather than relying on pgx defaults
// (max = max(4, numCPU), min = 0). A warm minimum avoids cold-connection
// latency on the first queries after idle, and an explicit ceiling bounds
// connection use under concurrent search + background builds so the database
// is not overwhelmed.
func NewPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres: parse config: %w", err)
	}
	maxConns := int32(4 * runtime.NumCPU())
	if maxConns < 10 {
		maxConns = 10
	}
	cfg.MaxConns = maxConns
	cfg.MinConns = 2

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: new pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: ping: %w", err)
	}
	return pool, nil
}

// schema is the full DDL. Postgres holds metadata only; vectorization is
// owned by the RAG indexer (Hindsight).
const schema = `
CREATE TABLE IF NOT EXISTS documents (
	id           UUID PRIMARY KEY,
	title        TEXT NOT NULL,
	filename     TEXT NOT NULL DEFAULT '',
	content_type TEXT NOT NULL,
	storage_key  TEXT NOT NULL,
	size_bytes   BIGINT NOT NULL,
	metadata     JSONB,
	created_at   TIMESTAMPTZ NOT NULL,
	updated_at   TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS knowledge_bases (
	id              UUID PRIMARY KEY,
	name            TEXT NOT NULL,
	description     TEXT NOT NULL DEFAULT '',
	enabled         BOOLEAN NOT NULL DEFAULT FALSE,
	active_build_id UUID,
	created_at      TIMESTAMPTZ NOT NULL,
	updated_at      TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS kb_documents (
	knowledge_base_id UUID NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
	document_id       UUID NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
	PRIMARY KEY (knowledge_base_id, document_id)
);

CREATE TABLE IF NOT EXISTS kb_builds (
	id                UUID PRIMARY KEY,
	knowledge_base_id UUID NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
	bank_id           TEXT NOT NULL,
	status            TEXT NOT NULL,
	document_count    INT NOT NULL DEFAULT 0,
	error             TEXT NOT NULL DEFAULT '',
	created_at        TIMESTAMPTZ NOT NULL,
	updated_at        TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS memory_feedback (
	knowledge_base_id UUID NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
	memory_id         TEXT NOT NULL,
	reviewer          TEXT NOT NULL,
	vote              TEXT NOT NULL,
	proposed_text     TEXT NOT NULL DEFAULT '',
	created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
	PRIMARY KEY (knowledge_base_id, memory_id, reviewer, proposed_text)
);

CREATE TABLE IF NOT EXISTS memory_consensus_applied (
	knowledge_base_id UUID NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
	memory_id         TEXT NOT NULL,
	proposed_text     TEXT NOT NULL,
	applied_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
	PRIMARY KEY (knowledge_base_id, memory_id, proposed_text)
);

CREATE INDEX IF NOT EXISTS kb_builds_kb_idx ON kb_builds (knowledge_base_id);
CREATE INDEX IF NOT EXISTS kb_documents_doc_idx ON kb_documents (document_id);
CREATE INDEX IF NOT EXISTS memory_feedback_memory_idx ON memory_feedback (knowledge_base_id, memory_id);
`

// Migrate applies the schema.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, schema); err != nil {
		return fmt.Errorf("postgres: migrate: %w", err)
	}
	return nil
}

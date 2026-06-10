package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPool opens a pgx connection pool and verifies connectivity.
func NewPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, dsn)
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

CREATE INDEX IF NOT EXISTS kb_builds_kb_idx ON kb_builds (knowledge_base_id);
CREATE INDEX IF NOT EXISTS kb_documents_doc_idx ON kb_documents (document_id);
`

// Migrate applies the schema.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, schema); err != nil {
		return fmt.Errorf("postgres: migrate: %w", err)
	}
	return nil
}

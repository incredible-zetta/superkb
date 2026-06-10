package domain

import (
	"context"
	"io"

	"github.com/google/uuid"
)

// SearchResult is a single relevant snippet returned from a RAG recall query.
type SearchResult struct {
	DocumentID string  // source document id within the bank (if available)
	Content    string  // recalled fact / chunk text
	Score      float64 // relevance score
}

// DocumentRepository persists raw document metadata.
type DocumentRepository interface {
	Create(ctx context.Context, doc *Document) error
	GetByID(ctx context.Context, id uuid.UUID) (*Document, error)
	List(ctx context.Context, limit, offset int) ([]Document, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

// KnowledgeBaseRepository persists knowledge bases and their build snapshots.
type KnowledgeBaseRepository interface {
	Create(ctx context.Context, kb *KnowledgeBase) error
	GetByID(ctx context.Context, id uuid.UUID) (*KnowledgeBase, error)
	List(ctx context.Context, limit, offset int) ([]KnowledgeBase, error)
	Update(ctx context.Context, kb *KnowledgeBase) error
	Delete(ctx context.Context, id uuid.UUID) error

	// Membership: which documents belong to a knowledge base.
	AddDocument(ctx context.Context, kbID, documentID uuid.UUID) error
	RemoveDocument(ctx context.Context, kbID, documentID uuid.UUID) error
	ListDocuments(ctx context.Context, kbID uuid.UUID) ([]Document, error)

	// Builds.
	CreateBuild(ctx context.Context, b *Build) error
	GetBuild(ctx context.Context, buildID uuid.UUID) (*Build, error)
	UpdateBuild(ctx context.Context, b *Build) error
	ListBuilds(ctx context.Context, kbID uuid.UUID) ([]Build, error)
}

// ObjectStorage stores and retrieves raw document content (S3/R2-compatible).
type ObjectStorage interface {
	Put(ctx context.Context, key string, body io.Reader, size int64, contentType string) error
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
}

// RAGDocument is a document handed to the RAG indexer for processing.
type RAGDocument struct {
	DocumentID string // stable id used for idempotent upsert within the bank
	Title      string
	Filename   string // original filename; drives server-side format conversion
	Content    []byte // raw bytes (may be binary, e.g. PDF/DOCX)
	Metadata   map[string]string
}

// RAGIndexer extracts, chunks, and vectorizes documents into isolated banks,
// then answers similarity queries. Implemented by the Hindsight adapter.
//
// A bank is an immutable snapshot once built. Rollback = point search at a
// different bank; no re-processing required.
type RAGIndexer interface {
	// CreateBank provisions a new isolated bank and returns its id.
	CreateBank(ctx context.Context, bankID string) error
	// Retain ingests documents into a bank (chunk + extract + vectorize).
	Retain(ctx context.Context, bankID string, docs []RAGDocument) error
	// Recall runs a similarity search against a bank.
	Recall(ctx context.Context, bankID, query string, topK int) ([]SearchResult, error)
	// DeleteBank removes a bank and all its indexed data.
	DeleteBank(ctx context.Context, bankID string) error
}

// BuildQueue hands build jobs to the background worker. The default
// implementation is an in-process channel; it can be swapped for a durable
// queue (Redis, SQS) without touching the usecase or worker.
type BuildQueue interface {
	// Enqueue schedules a build for background processing.
	Enqueue(ctx context.Context, buildID uuid.UUID) error
}

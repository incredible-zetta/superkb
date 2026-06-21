package domain

import (
	"context"
	"io"

	"github.com/google/uuid"
)

// SearchResult is a single relevant fact returned from a RAG recall query,
// with the source references needed to cite and highlight it in the UI.
type MemoryType string

const (
	MemoryTypeWorld       MemoryType = "world"
	MemoryTypeExperience  MemoryType = "experience"
	MemoryTypeObservation MemoryType = "observation"
)

type Memory struct {
	ID       string            `json:"id"`
	Content  string            `json:"content"`
	Type     MemoryType        `json:"type"`
	Context  string            `json:"context,omitempty"`
	Tags     []string          `json:"tags,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type MemoryCuration struct {
	Text  string `json:"text,omitempty"`
	State string `json:"state,omitempty"`
}

type MemoryFeedback struct {
	KnowledgeBaseID uuid.UUID
	MemoryID        string
	Reviewer        string
	Vote            string
	ProposedText    string
}

type MemoryConsensus struct {
	MemoryID     string
	ProposedText string
	Approvals    int
	Rejections   int
	Status       string
	Applied      bool
}

type MemoryFeedbackRepository interface {
	AddMemoryFeedback(ctx context.Context, feedback MemoryFeedback) (MemoryConsensus, error)
	MarkMemoryConsensusApplied(ctx context.Context, kbID uuid.UUID, memoryID, proposedText string) error
}

type SearchResult struct {
	MemoryID   string   // Hindsight memory/fact id
	DocumentID string   // source document id within the bank (our document UUID)
	Content    string   // recalled fact / synthesized text
	Score      float64  // relevance score (0 when the backend omits it)
	Context    string   // source document title/label
	Entities   []string // resolved entity names mentioned in the fact
	ChunkID    string   // id of the source chunk the fact came from
	ChunkIndex int      // ordinal of the chunk within the document
	ChunkText  string   // raw source chunk text (the snippet to highlight)

	// Reference fields, populated when references are requested and the source
	// document is resolvable.
	FileURL  string // public, browsable link to the original file (if configured)
	Filename string // original filename
	Page     int    // 1-based page the chunk text was found on (0 = unknown)
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

// TextExtractor extracts plain text from a raw document, split into pages.
// Used to show source context and locate which page a chunk came from.
// Implementations that cannot page-split (e.g. plain text) may return a
// single page.
type TextExtractor interface {
	// ExtractPages returns the document text as an ordered slice of pages
	// (index 0 = page 1). contentType hints the format (e.g. application/pdf).
	ExtractPages(ctx context.Context, content []byte, contentType string) ([]string, error)
}

// DocumentConverter converts a raw document (possibly a scanned PDF or image)
// into plain text / markdown before it is retained. It is the OCR seam: the
// default Hindsight file-retain path uses markitdown server-side and cannot OCR
// scanned/image documents without tesseract, so an external converter (e.g.
// Mistral OCR) is plugged in here to extract text up front.
//
// Implementations should return ok=false when they do not handle a given
// content type (e.g. plain text), so the caller can fall back to sending the
// raw bytes to the indexer unchanged.
type DocumentConverter interface {
	// Convert returns the extracted text for the document and ok=true when the
	// converter handled it. When ok=false the document was intentionally
	// skipped (unsupported type) and the caller should retain raw bytes.
	Convert(ctx context.Context, content []byte, contentType, filename string) (text string, ok bool, err error)
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
	// RetainMemories stores non-file memories such as conversations, actions, and feedback.
	RetainMemories(ctx context.Context, bankID string, memories []Memory) ([]Memory, error)
	// CurateMemory edits, invalidates, or restores an extracted memory unit.
	CurateMemory(ctx context.Context, bankID, memoryID string, curation MemoryCuration) (*Memory, error)
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

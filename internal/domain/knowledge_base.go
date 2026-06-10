package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// BuildStatus is the lifecycle state of a knowledge base build.
type BuildStatus string

const (
	// BuildPending — created, not yet processing.
	BuildPending BuildStatus = "pending"
	// BuildProcessing — documents being retained/vectorized into the RAG bank.
	BuildProcessing BuildStatus = "processing"
	// BuildReady — all documents indexed; eligible to be enabled for search.
	BuildReady BuildStatus = "ready"
	// BuildFailed — processing failed; not eligible for search.
	BuildFailed BuildStatus = "failed"
)

// KnowledgeBase is a named group of documents. Searching a knowledge base
// queries its currently active build. Each build is an immutable RAG snapshot,
// which makes switching the active build an instant rollback.
type KnowledgeBase struct {
	ID            uuid.UUID
	Name          string
	Description   string
	Enabled       bool       // whether this KB participates in search
	ActiveBuildID *uuid.UUID // the build that serves search queries
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Build is an immutable snapshot of a knowledge base's documents indexed into
// a dedicated RAG bank. BankID maps to the underlying RAG indexer bank.
type Build struct {
	ID              uuid.UUID
	KnowledgeBaseID uuid.UUID
	BankID          string // RAG indexer bank identifier
	Status          BuildStatus
	DocumentCount   int
	Error           string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Validate checks required KnowledgeBase invariants.
func (k KnowledgeBase) Validate() error {
	if k.Name == "" {
		return errors.Join(ErrInvalidInput, errors.New("knowledge base name is required"))
	}
	return nil
}

// IsSearchable reports whether the knowledge base can currently serve search.
func (k KnowledgeBase) IsSearchable() bool {
	return k.Enabled && k.ActiveBuildID != nil
}

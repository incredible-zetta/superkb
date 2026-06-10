package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Domain-level sentinel errors. Outer layers map these to transport codes.
var (
	ErrNotFound     = errors.New("resource not found")
	ErrInvalidInput = errors.New("invalid input")
	ErrConflict     = errors.New("resource conflict")
)

// Document is a raw uploaded file. Content lives in object storage (R2);
// only metadata is persisted. Documents are NOT vectorized on upload — they
// become searchable only when included in a knowledge base build.
type Document struct {
	ID          uuid.UUID
	Title       string
	Filename    string
	ContentType string
	StorageKey  string // object storage key (R2)
	SizeBytes   int64
	Metadata    map[string]string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Validate checks required Document invariants.
func (d Document) Validate() error {
	if d.Title == "" {
		return errors.Join(ErrInvalidInput, errors.New("document title is required"))
	}
	if d.StorageKey == "" {
		return errors.Join(ErrInvalidInput, errors.New("document storage key is required"))
	}
	return nil
}

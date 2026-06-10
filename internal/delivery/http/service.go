package http

import (
	"context"

	"github.com/google/uuid"

	"superkb/internal/domain"
	"superkb/internal/usecase"
)

// DocumentService is the delivery-layer view of the document usecase.
type DocumentService interface {
	Upload(ctx context.Context, in usecase.UploadInput) (*domain.Document, error)
	Get(ctx context.Context, id uuid.UUID) (*domain.Document, error)
	List(ctx context.Context, limit, offset int) ([]domain.Document, error)
	Delete(ctx context.Context, id uuid.UUID) error
	Source(ctx context.Context, id uuid.UUID) (*usecase.DocumentSource, error)
}

// KnowledgeBaseService is the delivery-layer view of the knowledge base usecase.
type KnowledgeBaseService interface {
	Create(ctx context.Context, name, description string) (*domain.KnowledgeBase, error)
	Get(ctx context.Context, id uuid.UUID) (*domain.KnowledgeBase, error)
	List(ctx context.Context, limit, offset int) ([]domain.KnowledgeBase, error)
	Delete(ctx context.Context, id uuid.UUID) error
	AddDocument(ctx context.Context, kbID, documentID uuid.UUID) error
	RemoveDocument(ctx context.Context, kbID, documentID uuid.UUID) error
	Build(ctx context.Context, kbID uuid.UUID) (*domain.Build, error)
	Enable(ctx context.Context, kbID, buildID uuid.UUID) (*domain.KnowledgeBase, error)
	Disable(ctx context.Context, kbID uuid.UUID) (*domain.KnowledgeBase, error)
	ListBuilds(ctx context.Context, kbID uuid.UUID) ([]domain.Build, error)
	Search(ctx context.Context, kbID uuid.UUID, query string, opts usecase.SearchOptions) ([]domain.SearchResult, error)
}

package usecase

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"

	"superkb/internal/domain"
)

// DocumentUseCase handles raw document upload and retrieval. Documents are
// stored in object storage (R2) only — they are NOT vectorized here. Indexing
// happens when a document is included in a knowledge base build.
type DocumentUseCase struct {
	repo          domain.DocumentRepository
	storage       domain.ObjectStorage
	extractor     domain.TextExtractor
	publicBaseURL string
	now           func() time.Time
}

// NewDocumentUseCase wires a DocumentUseCase. extractor and publicBaseURL may
// be zero-valued; the Source endpoint then returns a single page and links are
// omitted.
func NewDocumentUseCase(repo domain.DocumentRepository, storage domain.ObjectStorage, extractor domain.TextExtractor, publicBaseURL string) *DocumentUseCase {
	return &DocumentUseCase{repo: repo, storage: storage, extractor: extractor, publicBaseURL: publicBaseURL, now: time.Now}
}

// UploadInput carries data for a raw document upload.
type UploadInput struct {
	Title       string
	Filename    string
	ContentType string
	Content     []byte
	Metadata    map[string]string
}

// Upload stores raw content in object storage and persists its metadata.
func (uc *DocumentUseCase) Upload(ctx context.Context, in UploadInput) (*domain.Document, error) {
	if in.Title == "" {
		return nil, fmt.Errorf("upload: %w: title required", domain.ErrInvalidInput)
	}
	if len(in.Content) == 0 {
		return nil, fmt.Errorf("upload: %w: content required", domain.ErrInvalidInput)
	}

	id := uuid.New()
	key := fmt.Sprintf("documents/%s", id.String())
	contentType := in.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	if err := uc.storage.Put(ctx, key, bytes.NewReader(in.Content), int64(len(in.Content)), contentType); err != nil {
		return nil, fmt.Errorf("upload: store content: %w", err)
	}

	now := uc.now().UTC()
	doc := &domain.Document{
		ID:          id,
		Title:       in.Title,
		Filename:    in.Filename,
		ContentType: contentType,
		StorageKey:  key,
		SizeBytes:   int64(len(in.Content)),
		Metadata:    in.Metadata,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := doc.Validate(); err != nil {
		return nil, err
	}
	if err := uc.repo.Create(ctx, doc); err != nil {
		return nil, fmt.Errorf("upload: persist metadata: %w", err)
	}
	return doc, nil
}

// Get returns document metadata by ID.
func (uc *DocumentUseCase) Get(ctx context.Context, id uuid.UUID) (*domain.Document, error) {
	doc, err := uc.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get: %w", err)
	}
	return doc, nil
}

// DocumentSource is a document's extracted text plus reference metadata, used
// by a UI to render the source and highlight/anchor into it.
type DocumentSource struct {
	Document *domain.Document
	FileURL  string   // public link to the original file (if configured)
	Pages    []string // extracted text, one entry per page (index 0 = page 1)
}

// Source fetches a document's content from storage and extracts its text,
// split into pages, along with a public file link.
func (uc *DocumentUseCase) Source(ctx context.Context, id uuid.UUID) (*DocumentSource, error) {
	doc, err := uc.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("source: %w", err)
	}
	rc, err := uc.storage.Get(ctx, doc.StorageKey)
	if err != nil {
		return nil, fmt.Errorf("source: fetch content: %w", err)
	}
	defer rc.Close()
	content, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("source: read content: %w", err)
	}

	var pages []string
	if uc.extractor != nil {
		pages, err = uc.extractor.ExtractPages(ctx, content, doc.ContentType)
		if err != nil {
			return nil, fmt.Errorf("source: extract: %w", err)
		}
	} else {
		pages = []string{string(content)}
	}

	return &DocumentSource{
		Document: doc,
		FileURL:  fileURL(uc.publicBaseURL, doc.StorageKey),
		Pages:    pages,
	}, nil
}

// List returns paginated document metadata.
func (uc *DocumentUseCase) List(ctx context.Context, limit, offset int) ([]domain.Document, error) {
	limit, offset = normalizePage(limit, offset)
	return uc.repo.List(ctx, limit, offset)
}

// Delete removes a document's stored content and metadata.
func (uc *DocumentUseCase) Delete(ctx context.Context, id uuid.UUID) error {
	doc, err := uc.repo.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	if err := uc.storage.Delete(ctx, doc.StorageKey); err != nil {
		return fmt.Errorf("delete: storage: %w", err)
	}
	if err := uc.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("delete: metadata: %w", err)
	}
	return nil
}

func normalizePage(limit, offset int) (int, int) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

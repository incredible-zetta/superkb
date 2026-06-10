package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"superkb/internal/domain"
)

// DocumentRepository is a pgx-backed domain.DocumentRepository.
type DocumentRepository struct {
	pool *pgxpool.Pool
}

// NewDocumentRepository constructs a DocumentRepository over a pgx pool.
func NewDocumentRepository(pool *pgxpool.Pool) *DocumentRepository {
	return &DocumentRepository{pool: pool}
}

// Create inserts document metadata.
func (r *DocumentRepository) Create(ctx context.Context, d *domain.Document) error {
	const q = `
		INSERT INTO documents (id, title, filename, content_type, storage_key, size_bytes, metadata, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`
	_, err := r.pool.Exec(ctx, q,
		d.ID, d.Title, d.Filename, d.ContentType, d.StorageKey, d.SizeBytes, d.Metadata, d.CreatedAt, d.UpdatedAt)
	if err != nil {
		return fmt.Errorf("postgres: create document: %w", err)
	}
	return nil
}

// GetByID loads document metadata by ID.
func (r *DocumentRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Document, error) {
	const q = `
		SELECT id, title, filename, content_type, storage_key, size_bytes, metadata, created_at, updated_at
		FROM documents WHERE id = $1`
	var d domain.Document
	err := r.pool.QueryRow(ctx, q, id).Scan(
		&d.ID, &d.Title, &d.Filename, &d.ContentType, &d.StorageKey, &d.SizeBytes, &d.Metadata, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: get document: %w", err)
	}
	return &d, nil
}

// List returns paginated document metadata, newest first.
func (r *DocumentRepository) List(ctx context.Context, limit, offset int) ([]domain.Document, error) {
	const q = `
		SELECT id, title, filename, content_type, storage_key, size_bytes, metadata, created_at, updated_at
		FROM documents ORDER BY created_at DESC LIMIT $1 OFFSET $2`
	rows, err := r.pool.Query(ctx, q, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("postgres: list documents: %w", err)
	}
	defer rows.Close()

	var out []domain.Document
	for rows.Next() {
		var d domain.Document
		if err := rows.Scan(
			&d.ID, &d.Title, &d.Filename, &d.ContentType, &d.StorageKey, &d.SizeBytes, &d.Metadata, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, fmt.Errorf("postgres: scan document: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Delete removes document metadata by ID.
func (r *DocumentRepository) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM documents WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("postgres: delete document: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

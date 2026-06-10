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

// KnowledgeBaseRepository is a pgx-backed domain.KnowledgeBaseRepository.
type KnowledgeBaseRepository struct {
	pool *pgxpool.Pool
}

// NewKnowledgeBaseRepository constructs the repository.
func NewKnowledgeBaseRepository(pool *pgxpool.Pool) *KnowledgeBaseRepository {
	return &KnowledgeBaseRepository{pool: pool}
}

func (r *KnowledgeBaseRepository) Create(ctx context.Context, kb *domain.KnowledgeBase) error {
	const q = `
		INSERT INTO knowledge_bases (id, name, description, enabled, active_build_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`
	_, err := r.pool.Exec(ctx, q, kb.ID, kb.Name, kb.Description, kb.Enabled, kb.ActiveBuildID, kb.CreatedAt, kb.UpdatedAt)
	if err != nil {
		return fmt.Errorf("postgres: create knowledge base: %w", err)
	}
	return nil
}

func (r *KnowledgeBaseRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.KnowledgeBase, error) {
	const q = `
		SELECT id, name, description, enabled, active_build_id, created_at, updated_at
		FROM knowledge_bases WHERE id = $1`
	var kb domain.KnowledgeBase
	err := r.pool.QueryRow(ctx, q, id).Scan(
		&kb.ID, &kb.Name, &kb.Description, &kb.Enabled, &kb.ActiveBuildID, &kb.CreatedAt, &kb.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: get knowledge base: %w", err)
	}
	return &kb, nil
}

func (r *KnowledgeBaseRepository) List(ctx context.Context, limit, offset int) ([]domain.KnowledgeBase, error) {
	const q = `
		SELECT id, name, description, enabled, active_build_id, created_at, updated_at
		FROM knowledge_bases ORDER BY created_at DESC LIMIT $1 OFFSET $2`
	rows, err := r.pool.Query(ctx, q, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("postgres: list knowledge bases: %w", err)
	}
	defer rows.Close()

	var out []domain.KnowledgeBase
	for rows.Next() {
		var kb domain.KnowledgeBase
		if err := rows.Scan(
			&kb.ID, &kb.Name, &kb.Description, &kb.Enabled, &kb.ActiveBuildID, &kb.CreatedAt, &kb.UpdatedAt); err != nil {
			return nil, fmt.Errorf("postgres: scan knowledge base: %w", err)
		}
		out = append(out, kb)
	}
	return out, rows.Err()
}

func (r *KnowledgeBaseRepository) Update(ctx context.Context, kb *domain.KnowledgeBase) error {
	const q = `
		UPDATE knowledge_bases
		SET name = $2, description = $3, enabled = $4, active_build_id = $5, updated_at = $6
		WHERE id = $1`
	tag, err := r.pool.Exec(ctx, q, kb.ID, kb.Name, kb.Description, kb.Enabled, kb.ActiveBuildID, kb.UpdatedAt)
	if err != nil {
		return fmt.Errorf("postgres: update knowledge base: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *KnowledgeBaseRepository) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM knowledge_bases WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("postgres: delete knowledge base: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *KnowledgeBaseRepository) AddDocument(ctx context.Context, kbID, documentID uuid.UUID) error {
	const q = `
		INSERT INTO kb_documents (knowledge_base_id, document_id)
		VALUES ($1, $2) ON CONFLICT DO NOTHING`
	if _, err := r.pool.Exec(ctx, q, kbID, documentID); err != nil {
		return fmt.Errorf("postgres: add document to kb: %w", err)
	}
	return nil
}

func (r *KnowledgeBaseRepository) RemoveDocument(ctx context.Context, kbID, documentID uuid.UUID) error {
	const q = `DELETE FROM kb_documents WHERE knowledge_base_id = $1 AND document_id = $2`
	if _, err := r.pool.Exec(ctx, q, kbID, documentID); err != nil {
		return fmt.Errorf("postgres: remove document from kb: %w", err)
	}
	return nil
}

func (r *KnowledgeBaseRepository) ListDocuments(ctx context.Context, kbID uuid.UUID) ([]domain.Document, error) {
	const q = `
		SELECT d.id, d.title, d.filename, d.content_type, d.storage_key, d.size_bytes, d.metadata, d.created_at, d.updated_at
		FROM documents d
		JOIN kb_documents kd ON kd.document_id = d.id
		WHERE kd.knowledge_base_id = $1
		ORDER BY d.created_at`
	rows, err := r.pool.Query(ctx, q, kbID)
	if err != nil {
		return nil, fmt.Errorf("postgres: list kb documents: %w", err)
	}
	defer rows.Close()

	var out []domain.Document
	for rows.Next() {
		var d domain.Document
		if err := rows.Scan(
			&d.ID, &d.Title, &d.Filename, &d.ContentType, &d.StorageKey, &d.SizeBytes, &d.Metadata, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, fmt.Errorf("postgres: scan kb document: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (r *KnowledgeBaseRepository) CreateBuild(ctx context.Context, b *domain.Build) error {
	const q = `
		INSERT INTO kb_builds (id, knowledge_base_id, bank_id, status, document_count, error, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	_, err := r.pool.Exec(ctx, q,
		b.ID, b.KnowledgeBaseID, b.BankID, string(b.Status), b.DocumentCount, b.Error, b.CreatedAt, b.UpdatedAt)
	if err != nil {
		return fmt.Errorf("postgres: create build: %w", err)
	}
	return nil
}

func (r *KnowledgeBaseRepository) GetBuild(ctx context.Context, buildID uuid.UUID) (*domain.Build, error) {
	const q = `
		SELECT id, knowledge_base_id, bank_id, status, document_count, error, created_at, updated_at
		FROM kb_builds WHERE id = $1`
	var (
		b      domain.Build
		status string
	)
	err := r.pool.QueryRow(ctx, q, buildID).Scan(
		&b.ID, &b.KnowledgeBaseID, &b.BankID, &status, &b.DocumentCount, &b.Error, &b.CreatedAt, &b.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: get build: %w", err)
	}
	b.Status = domain.BuildStatus(status)
	return &b, nil
}

func (r *KnowledgeBaseRepository) UpdateBuild(ctx context.Context, b *domain.Build) error {
	const q = `
		UPDATE kb_builds
		SET status = $2, document_count = $3, error = $4, updated_at = $5
		WHERE id = $1`
	tag, err := r.pool.Exec(ctx, q, b.ID, string(b.Status), b.DocumentCount, b.Error, b.UpdatedAt)
	if err != nil {
		return fmt.Errorf("postgres: update build: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *KnowledgeBaseRepository) ListBuilds(ctx context.Context, kbID uuid.UUID) ([]domain.Build, error) {
	const q = `
		SELECT id, knowledge_base_id, bank_id, status, document_count, error, created_at, updated_at
		FROM kb_builds WHERE knowledge_base_id = $1 ORDER BY created_at DESC`
	rows, err := r.pool.Query(ctx, q, kbID)
	if err != nil {
		return nil, fmt.Errorf("postgres: list builds: %w", err)
	}
	defer rows.Close()

	var out []domain.Build
	for rows.Next() {
		var (
			b      domain.Build
			status string
		)
		if err := rows.Scan(
			&b.ID, &b.KnowledgeBaseID, &b.BankID, &status, &b.DocumentCount, &b.Error, &b.CreatedAt, &b.UpdatedAt); err != nil {
			return nil, fmt.Errorf("postgres: scan build: %w", err)
		}
		b.Status = domain.BuildStatus(status)
		out = append(out, b)
	}
	return out, rows.Err()
}

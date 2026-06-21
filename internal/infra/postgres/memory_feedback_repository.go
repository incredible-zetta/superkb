package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"superkb/internal/domain"
)

// MemoryFeedbackRepository stores human consensus votes for memory curation.
type MemoryFeedbackRepository struct {
	pool *pgxpool.Pool
}

func NewMemoryFeedbackRepository(pool *pgxpool.Pool) *MemoryFeedbackRepository {
	return &MemoryFeedbackRepository{pool: pool}
}

func (r *MemoryFeedbackRepository) AddMemoryFeedback(ctx context.Context, f domain.MemoryFeedback) (domain.MemoryConsensus, error) {
	const insert = `
		INSERT INTO memory_feedback (knowledge_base_id, memory_id, reviewer, vote, proposed_text)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (knowledge_base_id, memory_id, reviewer, proposed_text)
		DO UPDATE SET vote = EXCLUDED.vote, created_at = now()`
	if _, err := r.pool.Exec(ctx, insert, f.KnowledgeBaseID, f.MemoryID, f.Reviewer, f.Vote, f.ProposedText); err != nil {
		return domain.MemoryConsensus{}, fmt.Errorf("postgres: add memory feedback: %w", err)
	}
	return r.consensus(ctx, f.KnowledgeBaseID, f.MemoryID, f.ProposedText)
}

func (r *MemoryFeedbackRepository) MarkMemoryConsensusApplied(ctx context.Context, kbID uuid.UUID, memoryID, proposedText string) error {
	const q = `
		INSERT INTO memory_consensus_applied (knowledge_base_id, memory_id, proposed_text)
		VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING`
	if _, err := r.pool.Exec(ctx, q, kbID, memoryID, proposedText); err != nil {
		return fmt.Errorf("postgres: mark memory consensus applied: %w", err)
	}
	return nil
}

func (r *MemoryFeedbackRepository) consensus(ctx context.Context, kbID uuid.UUID, memoryID, proposedText string) (domain.MemoryConsensus, error) {
	const q = `
		SELECT
			COUNT(*) FILTER (WHERE vote = 'approve') AS approvals,
			COUNT(*) FILTER (WHERE vote = 'reject') AS rejections,
			EXISTS (
				SELECT 1 FROM memory_consensus_applied a
				WHERE a.knowledge_base_id = $1 AND a.memory_id = $2 AND a.proposed_text = $3
			) AS applied
		FROM memory_feedback
		WHERE knowledge_base_id = $1 AND memory_id = $2 AND proposed_text = $3`
	c := domain.MemoryConsensus{MemoryID: memoryID, ProposedText: proposedText, Status: "pending_consensus"}
	if err := r.pool.QueryRow(ctx, q, kbID, memoryID, proposedText).Scan(&c.Approvals, &c.Rejections, &c.Applied); err != nil {
		return domain.MemoryConsensus{}, fmt.Errorf("postgres: memory consensus: %w", err)
	}
	if c.Applied {
		c.Status = "applied"
	}
	return c, nil
}

var _ domain.MemoryFeedbackRepository = (*MemoryFeedbackRepository)(nil)

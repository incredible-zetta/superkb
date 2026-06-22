// Package app wires the SuperKB application's infrastructure and usecases so
// multiple entrypoints (HTTP API, MCP server) can share one construction path.
package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"superkb/internal/config"
	"superkb/internal/infra/extract"
	"superkb/internal/infra/hindsight"
	"superkb/internal/infra/ocr"
	"superkb/internal/infra/postgres"
	"superkb/internal/infra/s3store"
	"superkb/internal/usecase"
)

// App holds the constructed dependencies shared by all entrypoints. Callers own
// lifecycle: start the worker if they need async builds, and call Close when
// done.
type App struct {
	Config               *config.Config
	Pool                 *pgxpool.Pool
	Storage              *s3store.Storage
	DocumentUseCase      *usecase.DocumentUseCase
	KnowledgeBaseUseCase *usecase.KnowledgeBaseUseCase
	Queue                *usecase.ChannelBuildQueue
}

// New loads config and constructs storage, repositories, the RAG indexer, and
// the document/knowledge-base usecases. It runs database migrations. The build
// queue is created but no worker is started; callers that need async builds
// construct a worker over App.Queue and App.KnowledgeBaseUseCase.
func New(ctx context.Context) (*App, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	pool, err := postgres.NewPool(ctx, cfg.Postgres.DSN)
	if err != nil {
		return nil, err
	}
	if err := postgres.Migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}

	storage, err := s3store.New(ctx, cfg.S3)
	if err != nil {
		pool.Close()
		return nil, err
	}
	if err := storage.EnsureBucket(ctx); err != nil {
		slog.Warn("ensure bucket failed; continuing", "error", err)
	}

	indexer := hindsight.New(cfg.Hindsight)
	extractor := extract.NewPDFExtractor()
	docRepo := postgres.NewDocumentRepository(pool)
	kbRepo := postgres.NewKnowledgeBaseRepository(pool)
	feedbackRepo := postgres.NewMemoryFeedbackRepository(pool)

	queue := usecase.NewChannelBuildQueue(cfg.Worker.QueueSize)
	docUC := usecase.NewDocumentUseCase(docRepo, storage, extractor, cfg.S3.PublicBaseURL)
	kbUC := usecase.NewKnowledgeBaseUseCase(kbRepo, feedbackRepo, docRepo, storage, indexer, queue, extractor, cfg.S3.PublicBaseURL)
	if conv := ocr.NewVision(cfg.VisionOCR); conv != nil {
		kbUC.WithConverter(conv)
		slog.Info("vision OCR enabled", "model", cfg.VisionOCR.Model)
	}

	return &App{
		Config:               cfg,
		Pool:                 pool,
		Storage:              storage,
		DocumentUseCase:      docUC,
		KnowledgeBaseUseCase: kbUC,
		Queue:                queue,
	}, nil
}

// Close releases the database pool.
func (a *App) Close() {
	if a.Pool != nil {
		a.Pool.Close()
	}
}

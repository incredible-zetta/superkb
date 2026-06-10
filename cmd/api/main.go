package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"superkb/internal/config"
	deliveryhttp "superkb/internal/delivery/http"
	"superkb/internal/infra/hindsight"
	"superkb/internal/infra/postgres"
	"superkb/internal/infra/s3store"
	"superkb/internal/usecase"
)

func main() {
	if err := run(); err != nil {
		slog.Error("startup failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	ctx := context.Background()

	pool, err := postgres.NewPool(ctx, cfg.Postgres.DSN)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := postgres.Migrate(ctx, pool); err != nil {
		return err
	}

	storage, err := s3store.New(ctx, cfg.S3)
	if err != nil {
		return err
	}
	if err := storage.EnsureBucket(ctx); err != nil {
		slog.Warn("ensure bucket failed; continuing", "error", err)
	}

	indexer := hindsight.New(cfg.Hindsight)
	docRepo := postgres.NewDocumentRepository(pool)
	kbRepo := postgres.NewKnowledgeBaseRepository(pool)

	queue := usecase.NewChannelBuildQueue(cfg.Worker.QueueSize)
	docUC := usecase.NewDocumentUseCase(docRepo, storage)
	kbUC := usecase.NewKnowledgeBaseUseCase(kbRepo, docRepo, storage, indexer, queue)

	// Background build worker: drains the queue and runs the RAG indexing
	// pipeline off the request path.
	workerCtx, stopWorker := context.WithCancel(context.Background())
	defer stopWorker()
	worker := usecase.NewBuildWorker(queue, kbUC, cfg.Worker.Concurrency, slog.Default())
	worker.Start(workerCtx)

	router := deliveryhttp.NewRouter(
		deliveryhttp.NewDocumentHandler(docUC),
		deliveryhttp.NewKnowledgeBaseHandler(kbUC),
		deliveryhttp.BasicAuth(cfg.Auth.Enabled, cfg.Auth.Username, cfg.Auth.Password),
	)

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.HTTP.Port),
		Handler:      router,
		ReadTimeout:  time.Duration(cfg.HTTP.ReadTimeoutSec) * time.Second,
		WriteTimeout: time.Duration(cfg.HTTP.WriteTimeoutSec) * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("http server listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		return fmt.Errorf("server error: %w", err)
	case sig := <-stop:
		slog.Info("shutting down", "signal", sig.String())
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err := srv.Shutdown(shutdownCtx)
		// Stop accepting new builds, then wait for in-flight builds to finish.
		stopWorker()
		worker.Wait()
		return err
	}
}

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

	"superkb/internal/app"
	deliveryhttp "superkb/internal/delivery/http"
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

	ctx := context.Background()

	application, err := app.New(ctx)
	if err != nil {
		return err
	}
	defer application.Close()

	// Background build worker: drains the queue and runs the RAG indexing
	// pipeline off the request path.
	workerCtx, stopWorker := context.WithCancel(context.Background())
	defer stopWorker()
	worker := usecase.NewBuildWorker(application.Queue, application.KnowledgeBaseUseCase, application.Config.Worker.Concurrency, slog.Default())
	worker.Start(workerCtx)

	router := deliveryhttp.NewRouter(
		deliveryhttp.NewDocumentHandler(application.DocumentUseCase),
		deliveryhttp.NewKnowledgeBaseHandler(application.KnowledgeBaseUseCase),
		deliveryhttp.BasicAuth(application.Config.Auth.Enabled, application.Config.Auth.Username, application.Config.Auth.Password),
	)

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", application.Config.HTTP.Port),
		Handler:      router,
		ReadTimeout:  time.Duration(application.Config.HTTP.ReadTimeoutSec) * time.Second,
		WriteTimeout: time.Duration(application.Config.HTTP.WriteTimeoutSec) * time.Second,
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

package main

import (
	"context"
	"log/slog"
	"os"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"superkb/internal/app"
	mcpdelivery "superkb/internal/delivery/mcp"
	"superkb/internal/usecase"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	slog.SetDefault(logger)

	ctx := context.Background()
	application, err := app.New(ctx)
	if err != nil {
		slog.Error("startup failed", "error", err)
		os.Exit(1)
	}
	defer application.Close()

	// Background build worker so build_knowledge_base can enqueue and complete.
	workerCtx, stopWorker := context.WithCancel(context.Background())
	defer stopWorker()
	worker := usecase.NewBuildWorker(application.Queue, application.KnowledgeBaseUseCase, application.Config.Worker.Concurrency, slog.Default())
	worker.Start(workerCtx)

	server := mcpdelivery.NewServer(application.DocumentUseCase, application.KnowledgeBaseUseCase)
	err = server.Run(ctx, &mcpsdk.StdioTransport{})

	stopWorker()
	worker.Wait()
	if err != nil {
		slog.Error("mcp server exited", "error", err)
		os.Exit(1)
	}
}

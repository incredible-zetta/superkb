package main

import (
	"context"
	"log/slog"
	"os"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"superkb/internal/app"
	mcpdelivery "superkb/internal/delivery/mcp"
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

	server := mcpdelivery.NewServer(application.DocumentUseCase, application.KnowledgeBaseUseCase)
	if err := server.Run(ctx, &mcpsdk.StdioTransport{}); err != nil {
		slog.Error("mcp server exited", "error", err)
		os.Exit(1)
	}
}

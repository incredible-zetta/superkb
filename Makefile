.PHONY: help tidy build run test test-cover lint clean docker-up docker-down

BINARY := bin/api

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS=":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

tidy: ## Sync go.mod/go.sum
	go mod tidy

build: ## Build the API binary
	go build -o $(BINARY) ./cmd/api

run: ## Run the API
	go run ./cmd/api

test: ## Run all tests
	go test ./...

test-cover: ## Run tests with coverage
	go test -cover ./...

lint: ## Run go vet
	go vet ./...

clean: ## Remove build artifacts
	rm -rf bin

docker-up: ## Start postgres+minio+hindsight
	docker compose up -d

docker-down: ## Stop services
	docker compose down

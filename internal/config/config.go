package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds all application configuration loaded from environment variables.
type Config struct {
	HTTP      HTTPConfig
	Postgres  PostgresConfig
	S3        S3Config
	Hindsight HindsightConfig
	Auth      AuthConfig
	Worker    WorkerConfig
}

// HTTPConfig holds HTTP server settings.
type HTTPConfig struct {
	Port            int
	ReadTimeoutSec  int
	WriteTimeoutSec int
}

// PostgresConfig holds PostgreSQL / pgvector connection settings.
type PostgresConfig struct {
	DSN string
}

// S3Config holds S3-compatible object storage settings.
type S3Config struct {
	Endpoint        string
	Region          string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
	UsePathStyle    bool
	// PublicBaseURL is an optional public base URL (e.g. a CDN/public R2
	// domain) used to build browsable file links for search references.
	// The document storage key is appended to it.
	PublicBaseURL string
}

// HindsightConfig holds settings for the Hindsight RAG indexer service.
type HindsightConfig struct {
	BaseURL    string // e.g. http://localhost:8888
	APIKey     string // optional bearer token
	Profile    string // path segment, default "default"
	TimeoutSec int
}

// AuthConfig holds HTTP basic auth settings for service-to-service access.
type AuthConfig struct {
	Enabled  bool
	Username string
	Password string
}

// WorkerConfig tunes the background build worker.
type WorkerConfig struct {
	Concurrency int
	QueueSize   int
}

// Load reads configuration from environment variables, applying defaults.
func Load() (*Config, error) {
	cfg := &Config{
		HTTP: HTTPConfig{
			Port:            envInt("HTTP_PORT", 8080),
			ReadTimeoutSec:  envInt("HTTP_READ_TIMEOUT_SEC", 120),
			WriteTimeoutSec: envInt("HTTP_WRITE_TIMEOUT_SEC", 120),
		},
		Postgres: PostgresConfig{
			DSN: env("POSTGRES_DSN", "postgres://postgres:postgres@localhost:5432/superkb?sslmode=disable"),
		},
		S3: S3Config{
			Endpoint:        env("S3_ENDPOINT", ""),
			Region:          env("S3_REGION", "us-east-1"),
			Bucket:          env("S3_BUCKET", "superkb"),
			AccessKeyID:     env("S3_ACCESS_KEY_ID", ""),
			SecretAccessKey: env("S3_SECRET_ACCESS_KEY", ""),
			UsePathStyle:    envBool("S3_USE_PATH_STYLE", true),
			PublicBaseURL:   env("S3_PUBLIC_BASE_URL", ""),
		},
		Hindsight: HindsightConfig{
			BaseURL:    env("HINDSIGHT_BASE_URL", "http://localhost:8888"),
			APIKey:     env("HINDSIGHT_API_KEY", ""),
			Profile:    env("HINDSIGHT_PROFILE", "default"),
			TimeoutSec: envInt("HINDSIGHT_TIMEOUT_SEC", 120),
		},
		Auth: AuthConfig{
			Enabled:  envBool("AUTH_ENABLED", true),
			Username: env("AUTH_USERNAME", ""),
			Password: env("AUTH_PASSWORD", ""),
		},
		Worker: WorkerConfig{
			Concurrency: envInt("WORKER_CONCURRENCY", 2),
			QueueSize:   envInt("WORKER_QUEUE_SIZE", 64),
		},
	}

	if cfg.Hindsight.BaseURL == "" {
		return nil, fmt.Errorf("config: HINDSIGHT_BASE_URL is required")
	}
	if cfg.Auth.Enabled && (cfg.Auth.Username == "" || cfg.Auth.Password == "") {
		return nil, fmt.Errorf("config: AUTH_USERNAME and AUTH_PASSWORD are required when AUTH_ENABLED=true")
	}
	return cfg, nil
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envBool(key string, def bool) bool {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}

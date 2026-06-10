package hindsight

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"superkb/internal/config"
	"superkb/internal/domain"
)

// Indexer is a domain.RAGIndexer backed by a Hindsight server.
//
// Hindsight banks are isolated memory containers. Retaining a document
// chunks it, extracts facts, and vectorizes them into the bank; recall runs
// multi-strategy similarity search. Each build uses its own bank id, so
// rollback is just pointing search at a previous bank.
type Indexer struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
	profile    string
}

// New constructs an Indexer from Hindsight config.
func New(cfg config.HindsightConfig) *Indexer {
	profile := cfg.Profile
	if profile == "" {
		profile = "default"
	}
	return &Indexer{
		httpClient: &http.Client{Timeout: time.Duration(cfg.TimeoutSec) * time.Second},
		baseURL:    cfg.BaseURL,
		apiKey:     cfg.APIKey,
		profile:    profile,
	}
}

func (i *Indexer) bankPath(parts ...string) string {
	p := fmt.Sprintf("%s/v1/%s/banks", i.baseURL, i.profile)
	for _, part := range parts {
		p += "/" + part
	}
	return p
}

// CreateBank provisions a new isolated bank.
func (i *Indexer) CreateBank(ctx context.Context, bankID string) error {
	body := map[string]string{"bank_id": bankID}
	return i.do(ctx, http.MethodPost, i.bankPath(), body, nil)
}

// DeleteBank removes a bank and all its data.
func (i *Indexer) DeleteBank(ctx context.Context, bankID string) error {
	return i.do(ctx, http.MethodDelete, i.bankPath(bankID), nil, nil)
}

type retainItem struct {
	Content    string            `json:"content"`
	DocumentID string            `json:"document_id,omitempty"`
	Context    string            `json:"context,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	Timestamp  string            `json:"timestamp,omitempty"`
}

type retainRequest struct {
	Items []retainItem `json:"items"`
}

// Retain ingests documents into a bank. Each document is upserted by its
// DocumentID, so retaining the same build twice is idempotent.
func (i *Indexer) Retain(ctx context.Context, bankID string, docs []domain.RAGDocument) error {
	if len(docs) == 0 {
		return nil
	}
	items := make([]retainItem, len(docs))
	for n, d := range docs {
		items[n] = retainItem{
			Content:    string(d.Content),
			DocumentID: d.DocumentID,
			Context:    d.Title,
			Metadata:   d.Metadata,
			Timestamp:  "unset", // reference docs have no event time
		}
	}
	return i.do(ctx, http.MethodPost, i.bankPath(bankID, "retain"), retainRequest{Items: items}, nil)
}

type recallRequest struct {
	Query     string `json:"query"`
	MaxTokens int    `json:"max_tokens,omitempty"`
}

type recallResponse struct {
	Results []struct {
		ID    string  `json:"id"`
		Text  string  `json:"text"`
		Score float64 `json:"score"`
	} `json:"results"`
}

// Recall runs a similarity search against a bank.
func (i *Indexer) Recall(ctx context.Context, bankID, query string, topK int) ([]domain.SearchResult, error) {
	if topK <= 0 {
		topK = 5
	}
	req := recallRequest{Query: query, MaxTokens: topK * 256}
	var resp recallResponse
	if err := i.do(ctx, http.MethodPost, i.bankPath(bankID, "recall"), req, &resp); err != nil {
		return nil, err
	}
	out := make([]domain.SearchResult, 0, len(resp.Results))
	for n, r := range resp.Results {
		if n >= topK {
			break
		}
		out = append(out, domain.SearchResult{
			DocumentID: r.ID,
			Content:    r.Text,
			Score:      r.Score,
		})
	}
	return out, nil
}

// do executes a JSON request against Hindsight, decoding into out if non-nil.
func (i *Indexer) do(ctx context.Context, method, url string, payload, out any) error {
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("hindsight: marshal request: %w", err)
		}
		body = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return fmt.Errorf("hindsight: new request: %w", err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if i.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+i.apiKey)
	}

	resp, err := i.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("hindsight: %s %s: %w", method, url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("hindsight: %s %s failed (status %d): %s", method, url, resp.StatusCode, string(msg))
	}

	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("hindsight: decode response: %w", err)
		}
	}
	return nil
}

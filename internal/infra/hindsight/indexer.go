package hindsight

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
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

// CreateBank provisions a new isolated bank. Hindsight banks are created via
// PUT on the bank resource; the body may carry optional config.
func (i *Indexer) CreateBank(ctx context.Context, bankID string) error {
	return i.do(ctx, http.MethodPut, i.bankPath(bankID), map[string]any{}, nil)
}

// DeleteBank removes a bank and all its data.
func (i *Indexer) DeleteBank(ctx context.Context, bankID string) error {
	return i.do(ctx, http.MethodDelete, i.bankPath(bankID), nil, nil)
}

type retainResponse struct {
	OperationID  string   `json:"operation_id"`
	OperationIDs []string `json:"operation_ids"`
}

type operationResponse struct {
	Status       string `json:"status"`
	ErrorMessage string `json:"error_message"`
}

type operationsListResponse struct {
	Operations []operationResponse `json:"operations"`
}

// fileMetadata is one entry in the file-retain request's files_metadata array,
// positionally matched to the uploaded files.
type fileMetadata struct {
	DocumentID string            `json:"document_id,omitempty"`
	Context    string            `json:"context,omitempty"`
	Timestamp  string            `json:"timestamp,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

type fileRetainRequest struct {
	FilesMetadata []fileMetadata `json:"files_metadata"`
}

// Retain ingests documents into a bank via Hindsight's file-retain endpoint,
// which converts each uploaded file (PDF, DOCX, images, text, ...) to text
// server-side before chunking, extracting, and vectorizing. This is required
// because raw documents are often binary and cannot be sent as JSON text.
//
// File retain is always asynchronous: it returns operation ids which we poll
// until completion, so a long-running conversion never blocks on a single HTTP
// request. Each document is upserted by its DocumentID for idempotent rebuilds.
func (i *Indexer) Retain(ctx context.Context, bankID string, docs []domain.RAGDocument) error {
	if len(docs) == 0 {
		return nil
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	meta := make([]fileMetadata, len(docs))
	for n, d := range docs {
		filename := d.Filename
		if filename == "" {
			filename = d.DocumentID
		}
		fw, err := mw.CreateFormFile("files", filename)
		if err != nil {
			return fmt.Errorf("hindsight: multipart file: %w", err)
		}
		if _, err := fw.Write(d.Content); err != nil {
			return fmt.Errorf("hindsight: write file: %w", err)
		}
		meta[n] = fileMetadata{
			DocumentID: d.DocumentID,
			Context:    d.Title,
			Timestamp:  "unset", // reference docs have no event time
			Metadata:   d.Metadata,
		}
	}
	reqJSON, err := json.Marshal(fileRetainRequest{FilesMetadata: meta})
	if err != nil {
		return fmt.Errorf("hindsight: marshal file request: %w", err)
	}
	if err := mw.WriteField("request", string(reqJSON)); err != nil {
		return fmt.Errorf("hindsight: multipart request field: %w", err)
	}
	if err := mw.Close(); err != nil {
		return fmt.Errorf("hindsight: close multipart: %w", err)
	}

	resp, err := i.doMultipart(ctx, i.bankPath(bankID, "files", "retain"), mw.FormDataContentType(), &body)
	if err != nil {
		return err
	}

	opIDs := resp.OperationIDs
	if len(opIDs) == 0 && resp.OperationID != "" {
		opIDs = []string{resp.OperationID}
	}
	for _, opID := range opIDs {
		if err := i.waitForOperation(ctx, bankID, opID); err != nil {
			return err
		}
	}

	// File retain spawns child fact-extraction operations that continue after
	// the parent op reports completed. Wait until the bank has no in-flight
	// operations so the build is only marked ready once extraction has landed.
	return i.waitForBankIdle(ctx, bankID)
}

// waitForBankIdle polls the bank's operations until none are pending or
// processing. Bounded by ctx (the build worker controls the overall deadline).
func (i *Indexer) waitForBankIdle(ctx context.Context, bankID string) error {
	const pollInterval = 3 * time.Second
	// Require a couple of consecutive idle reads to avoid a race where child
	// operations have not yet been enqueued right after the parent completes.
	idleStreak := 0
	for {
		var resp operationsListResponse
		if err := i.do(ctx, http.MethodGet, i.bankPath(bankID, "operations")+"?limit=100", nil, &resp); err != nil {
			return fmt.Errorf("poll bank operations: %w", err)
		}
		inflight := 0
		for _, op := range resp.Operations {
			switch op.Status {
			case "pending", "processing":
				inflight++
			case "failed", "cancelled":
				if op.ErrorMessage != "" {
					return fmt.Errorf("bank operation %s: %s", op.Status, op.ErrorMessage)
				}
				return fmt.Errorf("bank operation %s", op.Status)
			}
		}
		if inflight == 0 {
			idleStreak++
			if idleStreak >= 2 {
				return nil
			}
		} else {
			idleStreak = 0
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

// waitForOperation polls a retain operation until it completes or fails.
// Bounded by ctx; the caller (build worker) controls the overall deadline.
func (i *Indexer) waitForOperation(ctx context.Context, bankID, opID string) error {
	const pollInterval = 3 * time.Second
	for {
		var op operationResponse
		if err := i.do(ctx, http.MethodGet, i.bankPath(bankID, "operations", opID), nil, &op); err != nil {
			return fmt.Errorf("poll operation %s: %w", opID, err)
		}
		switch op.Status {
		case "completed":
			return nil
		case "failed", "cancelled":
			if op.ErrorMessage != "" {
				return fmt.Errorf("retain operation %s %s: %s", opID, op.Status, op.ErrorMessage)
			}
			return fmt.Errorf("retain operation %s %s", opID, op.Status)
		case "not_found":
			return fmt.Errorf("retain operation %s not found", opID)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

type recallRequest struct {
	Query     string `json:"query"`
	MaxTokens int    `json:"max_tokens,omitempty"`
}

type recallResponse struct {
	Results []struct {
		ID         string `json:"id"`
		Text       string `json:"text"`
		DocumentID string `json:"document_id"`
	} `json:"results"`
}

// Recall runs a similarity search against a bank (POST .../memories/recall).
// Hindsight fuses semantic, keyword, graph, and temporal retrieval and does
// not return a raw similarity score, so results are returned in rank order
// with Score left zero.
func (i *Indexer) Recall(ctx context.Context, bankID, query string, topK int) ([]domain.SearchResult, error) {
	if topK <= 0 {
		topK = 5
	}
	req := recallRequest{Query: query, MaxTokens: topK * 256}
	var resp recallResponse
	if err := i.do(ctx, http.MethodPost, i.bankPath(bankID, "memories", "recall"), req, &resp); err != nil {
		return nil, err
	}
	out := make([]domain.SearchResult, 0, len(resp.Results))
	for n, r := range resp.Results {
		if n >= topK {
			break
		}
		docID := r.DocumentID
		if docID == "" {
			docID = r.ID
		}
		out = append(out, domain.SearchResult{
			DocumentID: docID,
			Content:    r.Text,
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

// doMultipart POSTs a multipart/form-data body and decodes the retain
// response. Used by the file-retain endpoint.
func (i *Indexer) doMultipart(ctx context.Context, url, contentType string, body io.Reader) (*retainResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, fmt.Errorf("hindsight: new request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	if i.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+i.apiKey)
	}

	resp, err := i.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hindsight: POST %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("hindsight: POST %s failed (status %d): %s", url, resp.StatusCode, string(msg))
	}

	var out retainResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("hindsight: decode response: %w", err)
	}
	return &out, nil
}

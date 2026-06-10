package hindsight

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"superkb/internal/config"
	"superkb/internal/domain"
)

func TestIndexer_CreateBank(t *testing.T) {
	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	idx := New(config.HindsightConfig{BaseURL: srv.URL, Profile: "default"})
	if err := idx.CreateBank(context.Background(), "kb-1"); err != nil {
		t.Fatalf("CreateBank: %v", err)
	}
	if gotMethod != http.MethodPut || gotPath != "/v1/default/banks/kb-1" {
		t.Fatalf("expected PUT /v1/default/banks/kb-1, got %s %s", gotMethod, gotPath)
	}
}

func TestIndexer_Retain(t *testing.T) {
	var gotFiles int
	var gotRequest string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/default/banks/kb-1/files/retain":
			if err := r.ParseMultipartForm(10 << 20); err != nil {
				t.Errorf("parse multipart: %v", err)
			}
			gotFiles = len(r.MultipartForm.File["files"])
			gotRequest = r.FormValue("request")
			_ = json.NewEncoder(w).Encode(retainResponse{OperationIDs: []string{"op-1", "op-2"}})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/default/banks/kb-1/operations/op-1":
			_ = json.NewEncoder(w).Encode(operationResponse{Status: "completed"})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/default/banks/kb-1/operations/op-2":
			_ = json.NewEncoder(w).Encode(operationResponse{Status: "completed"})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/default/banks/kb-1/operations":
			_ = json.NewEncoder(w).Encode(operationsListResponse{}) // idle: no in-flight ops
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	idx := New(config.HindsightConfig{BaseURL: srv.URL})
	docs := []domain.RAGDocument{
		{DocumentID: "d1", Title: "Doc 1", Filename: "d1.pdf", Content: []byte("hello")},
		{DocumentID: "d2", Title: "Doc 2", Filename: "d2.pdf", Content: []byte("world")},
	}
	if err := idx.Retain(context.Background(), "kb-1", docs); err != nil {
		t.Fatalf("Retain: %v", err)
	}
	if gotFiles != 2 {
		t.Fatalf("expected 2 uploaded files, got %d", gotFiles)
	}
	if !strings.Contains(gotRequest, "\"document_id\":\"d1\"") {
		t.Errorf("request metadata missing document_id d1: %s", gotRequest)
	}
	if !strings.Contains(gotRequest, "\"timestamp\":\"unset\"") {
		t.Errorf("request metadata missing timestamp unset: %s", gotRequest)
	}
}

func TestIndexer_RetainOperationFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_ = json.NewEncoder(w).Encode(retainResponse{OperationIDs: []string{"op-x"}})
			return
		}
		_ = json.NewEncoder(w).Encode(operationResponse{Status: "failed", ErrorMessage: "rate limit"})
	}))
	defer srv.Close()

	idx := New(config.HindsightConfig{BaseURL: srv.URL})
	err := idx.Retain(context.Background(), "kb-1", []domain.RAGDocument{{DocumentID: "d1", Filename: "d1.pdf", Content: []byte("x")}})
	if err == nil {
		t.Fatal("expected error on failed operation")
	}
}

func TestIndexer_RetainEmptyNoop(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
	}))
	defer srv.Close()

	idx := New(config.HindsightConfig{BaseURL: srv.URL})
	if err := idx.Retain(context.Background(), "kb-1", nil); err != nil {
		t.Fatalf("Retain empty: %v", err)
	}
	if called {
		t.Error("expected no HTTP call for empty docs")
	}
}

func TestIndexer_Recall(t *testing.T) {
	var reqBody recallRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/default/banks/kb-1/memories/recall" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&reqBody)
		_ = json.NewEncoder(w).Encode(recallResponse{
			Results: []recallResult{
				{ID: "m1", Text: "alpha fact", DocumentID: "d1", Context: "Doc A", Entities: []string{"Alpha"}, ChunkID: "c1"},
				{ID: "m2", Text: "beta fact", DocumentID: "d2", ChunkID: "c2"},
			},
			Chunks: map[string]recallChunk{
				"c1": {ID: "c1", Text: "alpha source chunk text", ChunkIndex: 3},
				"c2": {ID: "c2", Text: "beta source chunk text", ChunkIndex: 7},
			},
		})
	}))
	defer srv.Close()

	idx := New(config.HindsightConfig{BaseURL: srv.URL})
	results, err := idx.Recall(context.Background(), "kb-1", "alpha", 5)
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if reqBody.Include == nil || reqBody.Include.Chunks == nil {
		t.Fatal("expected recall request to include chunks")
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	r0 := results[0]
	if r0.Content != "alpha fact" || r0.DocumentID != "d1" {
		t.Errorf("unexpected first result: %+v", r0)
	}
	if r0.MemoryID != "m1" || r0.Context != "Doc A" || r0.ChunkID != "c1" {
		t.Errorf("missing citation fields: %+v", r0)
	}
	if r0.ChunkText != "alpha source chunk text" || r0.ChunkIndex != 3 {
		t.Errorf("chunk not mapped: text=%q index=%d", r0.ChunkText, r0.ChunkIndex)
	}
	if len(r0.Entities) != 1 || r0.Entities[0] != "Alpha" {
		t.Errorf("entities not mapped: %+v", r0.Entities)
	}
}

func TestIndexer_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer srv.Close()

	idx := New(config.HindsightConfig{BaseURL: srv.URL})
	if err := idx.CreateBank(context.Background(), "kb-1"); err == nil {
		t.Fatal("expected error on 500, got nil")
	}
}

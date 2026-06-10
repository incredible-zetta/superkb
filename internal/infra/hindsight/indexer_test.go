package hindsight

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	if gotMethod != http.MethodPost || gotPath != "/v1/default/banks" {
		t.Fatalf("expected POST /v1/default/banks, got %s %s", gotMethod, gotPath)
	}
}

func TestIndexer_Retain(t *testing.T) {
	var req retainRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/default/banks/kb-1/retain" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	idx := New(config.HindsightConfig{BaseURL: srv.URL})
	docs := []domain.RAGDocument{
		{DocumentID: "d1", Title: "Doc 1", Content: []byte("hello")},
		{DocumentID: "d2", Title: "Doc 2", Content: []byte("world")},
	}
	if err := idx.Retain(context.Background(), "kb-1", docs); err != nil {
		t.Fatalf("Retain: %v", err)
	}
	if len(req.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(req.Items))
	}
	if req.Items[0].DocumentID != "d1" || req.Items[0].Content != "hello" {
		t.Errorf("unexpected first item: %+v", req.Items[0])
	}
	if req.Items[0].Timestamp != "unset" {
		t.Errorf("expected timestamp unset, got %q", req.Items[0].Timestamp)
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/default/banks/kb-1/recall" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(recallResponse{
			Results: []struct {
				ID    string  `json:"id"`
				Text  string  `json:"text"`
				Score float64 `json:"score"`
			}{
				{ID: "m1", Text: "alpha fact", Score: 0.9},
				{ID: "m2", Text: "beta fact", Score: 0.7},
			},
		})
	}))
	defer srv.Close()

	idx := New(config.HindsightConfig{BaseURL: srv.URL})
	results, err := idx.Recall(context.Background(), "kb-1", "alpha", 5)
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Content != "alpha fact" || results[0].Score != 0.9 {
		t.Errorf("unexpected first result: %+v", results[0])
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

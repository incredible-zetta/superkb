package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"superkb/internal/domain"
	"superkb/internal/usecase"
)

type fakeDocService struct {
	uploadFn func(ctx context.Context, in usecase.UploadInput) (*domain.Document, error)
	getFn    func(ctx context.Context, id uuid.UUID) (*domain.Document, error)
	listFn   func(ctx context.Context, limit, offset int) ([]domain.Document, error)
	deleteFn func(ctx context.Context, id uuid.UUID) error
	sourceFn func(ctx context.Context, id uuid.UUID) (*usecase.DocumentSource, error)
}

func (f *fakeDocService) Upload(ctx context.Context, in usecase.UploadInput) (*domain.Document, error) {
	return f.uploadFn(ctx, in)
}
func (f *fakeDocService) Get(ctx context.Context, id uuid.UUID) (*domain.Document, error) {
	return f.getFn(ctx, id)
}
func (f *fakeDocService) List(ctx context.Context, limit, offset int) ([]domain.Document, error) {
	return f.listFn(ctx, limit, offset)
}
func (f *fakeDocService) Delete(ctx context.Context, id uuid.UUID) error { return f.deleteFn(ctx, id) }
func (f *fakeDocService) Source(ctx context.Context, id uuid.UUID) (*usecase.DocumentSource, error) {
	if f.sourceFn == nil {
		return nil, domain.ErrNotFound
	}
	return f.sourceFn(ctx, id)
}

func newDocServer(svc DocumentService) *httptest.Server {
	return httptest.NewServer(NewRouter(NewDocumentHandler(svc), NewKnowledgeBaseHandler(&fakeKBService{}), nil))
}

func TestHealthz(t *testing.T) {
	srv := newDocServer(&fakeDocService{})
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestUploadEndpoint(t *testing.T) {
	now := time.Now().UTC()
	id := uuid.New()
	svc := &fakeDocService{
		uploadFn: func(_ context.Context, in usecase.UploadInput) (*domain.Document, error) {
			if in.Title != "Hello" {
				t.Errorf("expected title Hello, got %q", in.Title)
			}
			return &domain.Document{ID: id, Title: in.Title, StorageKey: "documents/x", CreatedAt: now, UpdatedAt: now}, nil
		},
	}
	srv := newDocServer(svc)
	defer srv.Close()

	body, _ := json.Marshal(uploadRequest{Title: "Hello", Content: "world"})
	resp, err := http.Post(srv.URL+"/api/v1/documents", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	var got documentResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != id {
		t.Errorf("expected id %s, got %s", id, got.ID)
	}
}

func TestUploadValidationError(t *testing.T) {
	svc := &fakeDocService{
		uploadFn: func(_ context.Context, _ usecase.UploadInput) (*domain.Document, error) {
			return nil, domain.ErrInvalidInput
		},
	}
	srv := newDocServer(svc)
	defer srv.Close()

	body, _ := json.Marshal(uploadRequest{})
	resp, err := http.Post(srv.URL+"/api/v1/documents", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestDocGetNotFound(t *testing.T) {
	svc := &fakeDocService{
		getFn: func(_ context.Context, _ uuid.UUID) (*domain.Document, error) {
			return nil, domain.ErrNotFound
		},
	}
	srv := newDocServer(svc)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/documents/" + uuid.New().String())
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

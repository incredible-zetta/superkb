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

type fakeKBService struct {
	createFn   func(ctx context.Context, name, description string) (*domain.KnowledgeBase, error)
	getFn      func(ctx context.Context, id uuid.UUID) (*domain.KnowledgeBase, error)
	listFn     func(ctx context.Context, limit, offset int) ([]domain.KnowledgeBase, error)
	deleteFn   func(ctx context.Context, id uuid.UUID) error
	addFn      func(ctx context.Context, kbID, documentID uuid.UUID) error
	removeFn   func(ctx context.Context, kbID, documentID uuid.UUID) error
	buildFn    func(ctx context.Context, kbID uuid.UUID) (*domain.Build, error)
	enableFn   func(ctx context.Context, kbID, buildID uuid.UUID) (*domain.KnowledgeBase, error)
	disableFn  func(ctx context.Context, kbID uuid.UUID) (*domain.KnowledgeBase, error)
	listBldFn  func(ctx context.Context, kbID uuid.UUID) ([]domain.Build, error)
	searchFn   func(ctx context.Context, kbID uuid.UUID, query string, topK int) ([]domain.SearchResult, error)
}

func (f *fakeKBService) Create(ctx context.Context, n, d string) (*domain.KnowledgeBase, error) {
	return f.createFn(ctx, n, d)
}
func (f *fakeKBService) Get(ctx context.Context, id uuid.UUID) (*domain.KnowledgeBase, error) {
	return f.getFn(ctx, id)
}
func (f *fakeKBService) List(ctx context.Context, l, o int) ([]domain.KnowledgeBase, error) {
	return f.listFn(ctx, l, o)
}
func (f *fakeKBService) Delete(ctx context.Context, id uuid.UUID) error { return f.deleteFn(ctx, id) }
func (f *fakeKBService) AddDocument(ctx context.Context, k, d uuid.UUID) error {
	return f.addFn(ctx, k, d)
}
func (f *fakeKBService) RemoveDocument(ctx context.Context, k, d uuid.UUID) error {
	return f.removeFn(ctx, k, d)
}
func (f *fakeKBService) Build(ctx context.Context, k uuid.UUID) (*domain.Build, error) {
	return f.buildFn(ctx, k)
}
func (f *fakeKBService) Enable(ctx context.Context, k, b uuid.UUID) (*domain.KnowledgeBase, error) {
	return f.enableFn(ctx, k, b)
}
func (f *fakeKBService) Disable(ctx context.Context, k uuid.UUID) (*domain.KnowledgeBase, error) {
	return f.disableFn(ctx, k)
}
func (f *fakeKBService) ListBuilds(ctx context.Context, k uuid.UUID) ([]domain.Build, error) {
	return f.listBldFn(ctx, k)
}
func (f *fakeKBService) Search(ctx context.Context, k uuid.UUID, q string, opts usecase.SearchOptions) ([]domain.SearchResult, error) {
	return f.searchFn(ctx, k, q, opts.TopK)
}

func newKBServer(svc KnowledgeBaseService) *httptest.Server {
	return httptest.NewServer(NewRouter(NewDocumentHandler(&fakeDocService{}), NewKnowledgeBaseHandler(svc), nil))
}

func TestCreateKBEndpoint(t *testing.T) {
	now := time.Now().UTC()
	id := uuid.New()
	svc := &fakeKBService{
		createFn: func(_ context.Context, name, _ string) (*domain.KnowledgeBase, error) {
			return &domain.KnowledgeBase{ID: id, Name: name, CreatedAt: now, UpdatedAt: now}, nil
		},
	}
	srv := newKBServer(svc)
	defer srv.Close()

	body, _ := json.Marshal(createKBRequest{Name: "Handbook"})
	resp, err := http.Post(srv.URL+"/api/v1/knowledge-bases", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	var got kbResponse
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if got.ID != id || got.Searchable {
		t.Errorf("unexpected kb response: %+v", got)
	}
}

func TestBuildEndpoint(t *testing.T) {
	kbID := uuid.New()
	buildID := uuid.New()
	now := time.Now().UTC()
	svc := &fakeKBService{
		buildFn: func(_ context.Context, k uuid.UUID) (*domain.Build, error) {
			return &domain.Build{ID: buildID, KnowledgeBaseID: k, BankID: "kb-build-x", Status: domain.BuildReady, DocumentCount: 3, CreatedAt: now, UpdatedAt: now}, nil
		},
	}
	srv := newKBServer(svc)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/v1/knowledge-bases/"+kbID.String()+"/builds", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	var got buildResponse
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if got.Status != "ready" || got.DocumentCount != 3 {
		t.Errorf("unexpected build response: %+v", got)
	}
}

func TestBuildFailureReturns202(t *testing.T) {
	kbID := uuid.New()
	now := time.Now().UTC()
	svc := &fakeKBService{
		buildFn: func(_ context.Context, k uuid.UUID) (*domain.Build, error) {
			return &domain.Build{ID: uuid.New(), KnowledgeBaseID: k, Status: domain.BuildFailed, Error: "boom", CreatedAt: now, UpdatedAt: now},
				domain.ErrConflict
		},
	}
	srv := newKBServer(svc)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/v1/knowledge-bases/"+kbID.String()+"/builds", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", resp.StatusCode)
	}
	var got buildResponse
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if got.Status != "failed" || got.Error != "boom" {
		t.Errorf("unexpected build response: %+v", got)
	}
}

func TestEnableEndpoint(t *testing.T) {
	kbID := uuid.New()
	buildID := uuid.New()
	now := time.Now().UTC()
	var gotBuild uuid.UUID
	svc := &fakeKBService{
		enableFn: func(_ context.Context, k, b uuid.UUID) (*domain.KnowledgeBase, error) {
			gotBuild = b
			return &domain.KnowledgeBase{ID: k, Name: "KB", Enabled: true, ActiveBuildID: &b, CreatedAt: now, UpdatedAt: now}, nil
		},
	}
	srv := newKBServer(svc)
	defer srv.Close()

	body, _ := json.Marshal(enableRequest{BuildID: buildID.String()})
	resp, err := http.Post(srv.URL+"/api/v1/knowledge-bases/"+kbID.String()+"/enable", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if gotBuild != buildID {
		t.Errorf("expected build %s, got %s", buildID, gotBuild)
	}
	var got kbResponse
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if !got.Searchable {
		t.Error("expected enabled KB with active build to be searchable")
	}
}

func TestSearchEndpoint(t *testing.T) {
	kbID := uuid.New()
	svc := &fakeKBService{
		searchFn: func(_ context.Context, k uuid.UUID, q string, _ int) ([]domain.SearchResult, error) {
			if q != "alpha" {
				t.Errorf("expected query alpha, got %q", q)
			}
			return []domain.SearchResult{{
				MemoryID: "m1", DocumentID: "d1", Content: "alpha fact", Score: 0.9,
				Context: "Doc A", Entities: []string{"Alpha"},
				ChunkID: "c1", ChunkIndex: 3, ChunkText: "alpha source chunk text",
			}}, nil
		},
	}
	srv := newKBServer(svc)
	defer srv.Close()

	body, _ := json.Marshal(searchRequest{Query: "alpha", TopK: 5})
	resp, err := http.Post(srv.URL+"/api/v1/knowledge-bases/"+kbID.String()+"/search", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var out struct {
		Results []searchHit `json:"results"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if len(out.Results) != 1 || out.Results[0].Content != "alpha fact" {
		t.Fatalf("unexpected results: %+v", out.Results)
	}
	h := out.Results[0]
	if h.DocumentID != "d1" || h.ChunkID != "c1" || h.ChunkIndex != 3 {
		t.Errorf("missing citation fields: %+v", h)
	}
	if h.ChunkText != "alpha source chunk text" {
		t.Errorf("expected chunk_text for highlight, got %q", h.ChunkText)
	}
	if h.Context != "Doc A" || len(h.Entities) != 1 {
		t.Errorf("missing context/entities: %+v", h)
	}
}

func TestSearchDisabledKBConflict(t *testing.T) {
	kbID := uuid.New()
	svc := &fakeKBService{
		searchFn: func(_ context.Context, _ uuid.UUID, _ string, _ int) ([]domain.SearchResult, error) {
			return nil, domain.ErrConflict
		},
	}
	srv := newKBServer(svc)
	defer srv.Close()

	body, _ := json.Marshal(searchRequest{Query: "x"})
	resp, err := http.Post(srv.URL+"/api/v1/knowledge-bases/"+kbID.String()+"/search", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409, got %d", resp.StatusCode)
	}
}

func TestAddDocumentEndpoint(t *testing.T) {
	kbID, docID := uuid.New(), uuid.New()
	var gotKB, gotDoc uuid.UUID
	svc := &fakeKBService{
		addFn: func(_ context.Context, k, d uuid.UUID) error {
			gotKB, gotDoc = k, d
			return nil
		},
	}
	srv := newKBServer(svc)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/v1/knowledge-bases/"+kbID.String()+"/documents/"+docID.String(), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}
	if gotKB != kbID || gotDoc != docID {
		t.Errorf("expected (%s,%s), got (%s,%s)", kbID, docID, gotKB, gotDoc)
	}
}

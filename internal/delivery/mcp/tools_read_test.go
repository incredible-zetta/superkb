package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"superkb/internal/domain"
	"superkb/internal/usecase"
)

// --- fakes -----------------------------------------------------------------

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
	return f.sourceFn(ctx, id)
}

type fakeKBService struct {
	createFn       func(ctx context.Context, name, description string) (*domain.KnowledgeBase, error)
	getFn          func(ctx context.Context, id uuid.UUID) (*domain.KnowledgeBase, error)
	listFn         func(ctx context.Context, limit, offset int) ([]domain.KnowledgeBase, error)
	deleteFn       func(ctx context.Context, id uuid.UUID) error
	addFn          func(ctx context.Context, kbID, documentID uuid.UUID) error
	removeFn       func(ctx context.Context, kbID, documentID uuid.UUID) error
	buildFn        func(ctx context.Context, kbID uuid.UUID) (*domain.Build, error)
	processBuildFn func(ctx context.Context, buildID uuid.UUID) error
	enableFn       func(ctx context.Context, kbID, buildID uuid.UUID) (*domain.KnowledgeBase, error)
	disableFn      func(ctx context.Context, kbID uuid.UUID) (*domain.KnowledgeBase, error)
	listBuildsFn   func(ctx context.Context, kbID uuid.UUID) ([]domain.Build, error)
	searchFn       func(ctx context.Context, kbID uuid.UUID, query string, opts usecase.SearchOptions) ([]domain.SearchResult, error)
	retainExpFn    func(ctx context.Context, kbID uuid.UUID, in usecase.RetainMemoryInput) (*domain.Memory, error)
	curateFn       func(ctx context.Context, kbID uuid.UUID, memoryID string, in usecase.CurateMemoryInput) (*domain.Memory, error)
	feedbackFn     func(ctx context.Context, kbID uuid.UUID, memoryID string, in usecase.MemoryFeedbackInput) (*usecase.MemoryConsensusResult, error)
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
func (f *fakeKBService) ProcessBuild(ctx context.Context, b uuid.UUID) error {
	return f.processBuildFn(ctx, b)
}
func (f *fakeKBService) Enable(ctx context.Context, k, b uuid.UUID) (*domain.KnowledgeBase, error) {
	return f.enableFn(ctx, k, b)
}
func (f *fakeKBService) Disable(ctx context.Context, k uuid.UUID) (*domain.KnowledgeBase, error) {
	return f.disableFn(ctx, k)
}
func (f *fakeKBService) ListBuilds(ctx context.Context, k uuid.UUID) ([]domain.Build, error) {
	return f.listBuildsFn(ctx, k)
}
func (f *fakeKBService) Search(ctx context.Context, k uuid.UUID, q string, opts usecase.SearchOptions) ([]domain.SearchResult, error) {
	return f.searchFn(ctx, k, q, opts)
}
func (f *fakeKBService) RetainExperience(ctx context.Context, k uuid.UUID, in usecase.RetainMemoryInput) (*domain.Memory, error) {
	return f.retainExpFn(ctx, k, in)
}
func (f *fakeKBService) CurateMemory(ctx context.Context, k uuid.UUID, m string, in usecase.CurateMemoryInput) (*domain.Memory, error) {
	return f.curateFn(ctx, k, m, in)
}
func (f *fakeKBService) SubmitMemoryFeedback(ctx context.Context, k uuid.UUID, m string, in usecase.MemoryFeedbackInput) (*usecase.MemoryConsensusResult, error) {
	return f.feedbackFn(ctx, k, m, in)
}

// --- harness ---------------------------------------------------------------

// connect starts the server over an in-memory transport and returns a connected
// client session.
func connect(t *testing.T, docs DocumentService, kbs KnowledgeBaseService) *mcpsdk.ClientSession {
	t.Helper()
	server := NewServer(docs, kbs)
	clientT, serverT := mcpsdk.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := server.Connect(ctx, serverT, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "0"}, nil)
	session, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func callText(t *testing.T, s *mcpsdk.ClientSession, name string, args map[string]any) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := s.CallTool(ctx, &mcpsdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	if res.IsError {
		t.Fatalf("tool %s returned error: %+v", name, res.Content)
	}
	if len(res.Content) == 0 {
		t.Fatalf("tool %s returned no content", name)
	}
	tc, ok := res.Content[0].(*mcpsdk.TextContent)
	if !ok {
		t.Fatalf("tool %s returned non-text content", name)
	}
	return tc.Text
}

// --- tests -----------------------------------------------------------------

func TestServerRegistersReadTools(t *testing.T) {
	s := connect(t, &fakeDocService{}, &fakeKBService{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := s.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	got := map[string]bool{}
	for _, tool := range res.Tools {
		got[tool.Name] = true
	}
	for _, want := range []string{
		"list_documents", "get_document", "get_document_source",
		"list_knowledge_bases", "get_knowledge_base", "list_builds", "search_knowledge_base",
	} {
		if !got[want] {
			t.Errorf("missing tool %q", want)
		}
	}
}

func TestListKnowledgeBasesTool(t *testing.T) {
	id := uuid.New()
	now := time.Now().UTC()
	kbs := &fakeKBService{
		listFn: func(_ context.Context, _, _ int) ([]domain.KnowledgeBase, error) {
			return []domain.KnowledgeBase{{ID: id, Name: "KB", Enabled: true, CreatedAt: now, UpdatedAt: now}}, nil
		},
	}
	s := connect(t, &fakeDocService{}, kbs)
	out := callText(t, s, "list_knowledge_bases", map[string]any{"limit": 10})
	var got []domain.KnowledgeBase
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, out)
	}
	if len(got) != 1 || got[0].ID != id {
		t.Fatalf("unexpected result: %s", out)
	}
}

func TestSearchKnowledgeBaseTool(t *testing.T) {
	kbID := uuid.New()
	var gotQuery string
	var gotOpts usecase.SearchOptions
	kbs := &fakeKBService{
		searchFn: func(_ context.Context, k uuid.UUID, q string, opts usecase.SearchOptions) ([]domain.SearchResult, error) {
			if k != kbID {
				t.Fatalf("wrong kb id: %s", k)
			}
			gotQuery = q
			gotOpts = opts
			return []domain.SearchResult{{MemoryID: "m1", Content: "alpha fact"}}, nil
		},
	}
	s := connect(t, &fakeDocService{}, kbs)
	out := callText(t, s, "search_knowledge_base", map[string]any{
		"kb_id": kbID.String(), "query": "alpha", "top_k": 5, "include_references": true,
	})
	if gotQuery != "alpha" || gotOpts.TopK != 5 || !gotOpts.IncludeReferences {
		t.Fatalf("unexpected search args: q=%q opts=%+v", gotQuery, gotOpts)
	}
	var got []domain.SearchResult
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, out)
	}
	if len(got) != 1 || got[0].Content != "alpha fact" {
		t.Fatalf("unexpected result: %s", out)
	}
}

package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"superkb/internal/domain"
)

func newKBUC() (*KnowledgeBaseUseCase, *fakeKBRepo, *fakeDocRepo, *fakeStorage, *fakeIndexer) {
	docs := newFakeDocRepo()
	storage := newFakeStorage()
	kbRepo := newFakeKBRepo(docs)
	indexer := newFakeIndexer()
	queue := &syncQueue{}
	uc := NewKnowledgeBaseUseCase(kbRepo, docs, storage, indexer, queue, nil, "")
	queue.processor = uc // sync queue processes builds inline via the usecase
	return uc, kbRepo, docs, storage, indexer
}

// uploadDoc seeds a document via its own usecase so storage + repo agree.
func uploadDoc(t *testing.T, docs *fakeDocRepo, storage *fakeStorage, title, body string) *domain.Document {
	t.Helper()
	doc, err := NewDocumentUseCase(docs, storage, nil, "").Upload(context.Background(), UploadInput{Title: title, Content: []byte(body)})
	if err != nil {
		t.Fatalf("seed upload failed: %v", err)
	}
	return doc
}

func TestCreate_DisabledByDefault(t *testing.T) {
	uc, _, _, _, _ := newKBUC()
	kb, err := uc.Create(context.Background(), "Handbook", "company docs")
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}
	if kb.Enabled {
		t.Error("expected new KB to be disabled")
	}
	if kb.IsSearchable() {
		t.Error("expected new KB to be not searchable")
	}
}

func TestBuild_RetainsAllDocumentsIntoNewBank(t *testing.T) {
	uc, kbRepo, docs, storage, indexer := newKBUC()
	kb, _ := uc.Create(context.Background(), "KB", "")
	d1 := uploadDoc(t, docs, storage, "A", "alpha content")
	d2 := uploadDoc(t, docs, storage, "B", "beta content")
	mustAdd(t, uc, kb.ID, d1.ID)
	mustAdd(t, uc, kb.ID, d2.ID)

	build, err := uc.Build(context.Background(), kb.ID)
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}
	// Build returns a pending build; the worker (here, the sync queue) processes
	// it. Reload to observe the terminal state.
	stored, _ := kbRepo.GetBuild(context.Background(), build.ID)
	if stored.Status != domain.BuildReady {
		t.Fatalf("expected ready, got %s", stored.Status)
	}
	if stored.DocumentCount != 2 {
		t.Errorf("expected 2 docs, got %d", stored.DocumentCount)
	}
	if got := len(indexer.banks[build.BankID]); got != 2 {
		t.Errorf("expected 2 retained docs in bank, got %d", got)
	}
}

func TestBuild_ReturnsPendingThenEnqueues(t *testing.T) {
	uc, _, docs, storage, _ := newKBUC()
	kb, _ := uc.Create(context.Background(), "KB", "")
	d := uploadDoc(t, docs, storage, "A", "x")
	mustAdd(t, uc, kb.ID, d.ID)

	build, err := uc.Build(context.Background(), kb.ID)
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}
	if build.Status != domain.BuildPending {
		t.Errorf("expected returned build to be pending, got %s", build.Status)
	}
	if build.BankID == "" {
		t.Error("expected bank id assigned at enqueue time")
	}
}

func TestBuild_DoesNotAutoEnable(t *testing.T) {
	uc, _, docs, storage, _ := newKBUC()
	kb, _ := uc.Create(context.Background(), "KB", "")
	d := uploadDoc(t, docs, storage, "A", "x")
	mustAdd(t, uc, kb.ID, d.ID)

	if _, err := uc.Build(context.Background(), kb.ID); err != nil {
		t.Fatalf("Build error: %v", err)
	}
	reloaded, _ := uc.Get(context.Background(), kb.ID)
	if reloaded.IsSearchable() {
		t.Error("build must not auto-enable search")
	}
}

func TestBuild_EmptyKBRejected(t *testing.T) {
	uc, _, _, _, _ := newKBUC()
	kb, _ := uc.Create(context.Background(), "KB", "")
	_, err := uc.Build(context.Background(), kb.ID)
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput, got %v", err)
	}
}

func TestBuild_RetainFailureMarksFailed(t *testing.T) {
	uc, kbRepo, docs, storage, indexer := newKBUC()
	indexer.retainErr = errors.New("hindsight down")
	kb, _ := uc.Create(context.Background(), "KB", "")
	d := uploadDoc(t, docs, storage, "A", "x")
	mustAdd(t, uc, kb.ID, d.ID)

	build, err := uc.Build(context.Background(), kb.ID)
	if err == nil {
		t.Fatal("expected build error")
	}
	stored, _ := kbRepo.GetBuild(context.Background(), build.ID)
	if stored.Status != domain.BuildFailed {
		t.Errorf("expected failed status, got %s", stored.Status)
	}
	if stored.Error == "" {
		t.Error("expected error message recorded")
	}
}

func TestSearch_RequiresEnabledKB(t *testing.T) {
	uc, _, docs, storage, _ := newKBUC()
	kb, _ := uc.Create(context.Background(), "KB", "")
	d := uploadDoc(t, docs, storage, "A", "alpha")
	mustAdd(t, uc, kb.ID, d.ID)
	if _, err := uc.Build(context.Background(), kb.ID); err != nil {
		t.Fatalf("Build error: %v", err)
	}

	// Not enabled yet.
	_, err := uc.Search(context.Background(), kb.ID, "alpha", SearchOptions{TopK: 5})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expected ErrConflict before enable, got %v", err)
	}
}

func TestEnableThenSearch(t *testing.T) {
	uc, _, docs, storage, _ := newKBUC()
	kb, _ := uc.Create(context.Background(), "KB", "")
	d := uploadDoc(t, docs, storage, "A", "alpha content")
	mustAdd(t, uc, kb.ID, d.ID)
	build, _ := uc.Build(context.Background(), kb.ID)

	if _, err := uc.Enable(context.Background(), kb.ID, build.ID); err != nil {
		t.Fatalf("Enable error: %v", err)
	}
	results, err := uc.Search(context.Background(), kb.ID, "alpha", SearchOptions{TopK: 5})
	if err != nil {
		t.Fatalf("Search error: %v", err)
	}
	if len(results) != 1 || results[0].Content != "alpha content" {
		t.Fatalf("unexpected results: %+v", results)
	}
}

func TestEnable_RejectsBuildFromOtherKB(t *testing.T) {
	uc, _, docs, storage, _ := newKBUC()
	kb1, _ := uc.Create(context.Background(), "KB1", "")
	kb2, _ := uc.Create(context.Background(), "KB2", "")
	d := uploadDoc(t, docs, storage, "A", "x")
	mustAdd(t, uc, kb1.ID, d.ID)
	build, _ := uc.Build(context.Background(), kb1.ID)

	_, err := uc.Enable(context.Background(), kb2.ID, build.ID)
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput, got %v", err)
	}
}

// TestRollback verifies switching the active build is instant and changes
// which bank serves search, without re-processing.
func TestRollback(t *testing.T) {
	uc, _, docs, storage, indexer := newKBUC()
	kb, _ := uc.Create(context.Background(), "KB", "")

	// Build v1 with one doc.
	d1 := uploadDoc(t, docs, storage, "A", "alpha only")
	mustAdd(t, uc, kb.ID, d1.ID)
	buildV1, _ := uc.Build(context.Background(), kb.ID)
	if _, err := uc.Enable(context.Background(), kb.ID, buildV1.ID); err != nil {
		t.Fatalf("enable v1: %v", err)
	}

	// Build v2 with an extra doc; enable it.
	d2 := uploadDoc(t, docs, storage, "B", "beta added")
	mustAdd(t, uc, kb.ID, d2.ID)
	buildV2, _ := uc.Build(context.Background(), kb.ID)
	if _, err := uc.Enable(context.Background(), kb.ID, buildV2.ID); err != nil {
		t.Fatalf("enable v2: %v", err)
	}

	// v2 active: search returns 2 docs.
	res2, _ := uc.Search(context.Background(), kb.ID, "anything", SearchOptions{TopK: 10})
	if len(res2) != 2 {
		t.Fatalf("v2 expected 2 results, got %d", len(res2))
	}

	// Roll back to v1 (no rebuild) — instant.
	if _, err := uc.Enable(context.Background(), kb.ID, buildV1.ID); err != nil {
		t.Fatalf("rollback to v1: %v", err)
	}
	res1, _ := uc.Search(context.Background(), kb.ID, "anything", SearchOptions{TopK: 10})
	if len(res1) != 1 {
		t.Fatalf("v1 expected 1 result after rollback, got %d", len(res1))
	}

	// Both banks still exist (no reprocessing).
	if len(indexer.banks[buildV1.BankID]) != 1 || len(indexer.banks[buildV2.BankID]) != 2 {
		t.Error("both build banks should persist intact for instant rollback")
	}
}

func mustAdd(t *testing.T, uc *KnowledgeBaseUseCase, kbID, docID uuid.UUID) {
	t.Helper()
	if err := uc.AddDocument(context.Background(), kbID, docID); err != nil {
		t.Fatalf("AddDocument failed: %v", err)
	}
}

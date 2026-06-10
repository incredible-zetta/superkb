package usecase

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"

	"superkb/internal/domain"
)

// KnowledgeBaseUseCase manages knowledge bases: grouping documents, building
// immutable RAG snapshots (builds), enabling search, and instant rollback by
// switching the active build.
type KnowledgeBaseUseCase struct {
	repo          domain.KnowledgeBaseRepository
	docs          domain.DocumentRepository
	storage       domain.ObjectStorage
	indexer       domain.RAGIndexer
	queue         domain.BuildQueue
	extractor     domain.TextExtractor
	publicBaseURL string
	now           func() time.Time
	newBank       func(buildID uuid.UUID) string
}

// NewKnowledgeBaseUseCase wires a KnowledgeBaseUseCase. extractor and
// publicBaseURL power search references (file links + page numbers); both may
// be zero-valued to disable that enrichment.
func NewKnowledgeBaseUseCase(
	repo domain.KnowledgeBaseRepository,
	docs domain.DocumentRepository,
	storage domain.ObjectStorage,
	indexer domain.RAGIndexer,
	queue domain.BuildQueue,
	extractor domain.TextExtractor,
	publicBaseURL string,
) *KnowledgeBaseUseCase {
	return &KnowledgeBaseUseCase{
		repo:          repo,
		docs:          docs,
		storage:       storage,
		indexer:       indexer,
		queue:         queue,
		extractor:     extractor,
		publicBaseURL: publicBaseURL,
		now:           time.Now,
		newBank:       func(buildID uuid.UUID) string { return "kb-build-" + buildID.String() },
	}
}

// Create makes a new (disabled, empty) knowledge base.
func (uc *KnowledgeBaseUseCase) Create(ctx context.Context, name, description string) (*domain.KnowledgeBase, error) {
	if name == "" {
		return nil, fmt.Errorf("create kb: %w: name required", domain.ErrInvalidInput)
	}
	now := uc.now().UTC()
	kb := &domain.KnowledgeBase{
		ID:          uuid.New(),
		Name:        name,
		Description: description,
		Enabled:     false,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := uc.repo.Create(ctx, kb); err != nil {
		return nil, fmt.Errorf("create kb: %w", err)
	}
	return kb, nil
}

// Get returns a knowledge base by ID.
func (uc *KnowledgeBaseUseCase) Get(ctx context.Context, id uuid.UUID) (*domain.KnowledgeBase, error) {
	kb, err := uc.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get kb: %w", err)
	}
	return kb, nil
}

// List returns paginated knowledge bases.
func (uc *KnowledgeBaseUseCase) List(ctx context.Context, limit, offset int) ([]domain.KnowledgeBase, error) {
	limit, offset = normalizePage(limit, offset)
	return uc.repo.List(ctx, limit, offset)
}

// Delete removes a knowledge base. Its build banks should be reclaimed by the
// caller via ListBuilds beforehand if cleanup is desired.
func (uc *KnowledgeBaseUseCase) Delete(ctx context.Context, id uuid.UUID) error {
	builds, err := uc.repo.ListBuilds(ctx, id)
	if err != nil {
		return fmt.Errorf("delete kb: list builds: %w", err)
	}
	for _, b := range builds {
		// Best-effort bank cleanup; ignore not-found style errors.
		_ = uc.indexer.DeleteBank(ctx, b.BankID)
	}
	if err := uc.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("delete kb: %w", err)
	}
	return nil
}

// AddDocument adds a document to a knowledge base's membership set.
func (uc *KnowledgeBaseUseCase) AddDocument(ctx context.Context, kbID, documentID uuid.UUID) error {
	if _, err := uc.repo.GetByID(ctx, kbID); err != nil {
		return fmt.Errorf("add doc: kb: %w", err)
	}
	if _, err := uc.docs.GetByID(ctx, documentID); err != nil {
		return fmt.Errorf("add doc: document: %w", err)
	}
	if err := uc.repo.AddDocument(ctx, kbID, documentID); err != nil {
		return fmt.Errorf("add doc: %w", err)
	}
	return nil
}

// RemoveDocument removes a document from a knowledge base's membership set.
func (uc *KnowledgeBaseUseCase) RemoveDocument(ctx context.Context, kbID, documentID uuid.UUID) error {
	if err := uc.repo.RemoveDocument(ctx, kbID, documentID); err != nil {
		return fmt.Errorf("remove doc: %w", err)
	}
	return nil
}

// Build creates a pending build snapshot of the knowledge base's current
// documents and enqueues it for background processing. It returns immediately
// with the pending build; the worker provisions the bank and retains the
// documents asynchronously. Building does NOT change which build serves search
// — call Enable once the build is ready. This keeps builds safe to prepare and
// switch between for instant rollback.
func (uc *KnowledgeBaseUseCase) Build(ctx context.Context, kbID uuid.UUID) (*domain.Build, error) {
	if _, err := uc.repo.GetByID(ctx, kbID); err != nil {
		return nil, fmt.Errorf("build: kb: %w", err)
	}
	members, err := uc.repo.ListDocuments(ctx, kbID)
	if err != nil {
		return nil, fmt.Errorf("build: list documents: %w", err)
	}
	if len(members) == 0 {
		return nil, fmt.Errorf("build: %w: knowledge base has no documents", domain.ErrInvalidInput)
	}

	now := uc.now().UTC()
	build := &domain.Build{
		ID:              uuid.New(),
		KnowledgeBaseID: kbID,
		Status:          domain.BuildPending,
		DocumentCount:   len(members),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	build.BankID = uc.newBank(build.ID)
	if err := uc.repo.CreateBuild(ctx, build); err != nil {
		return nil, fmt.Errorf("build: persist build: %w", err)
	}

	if err := uc.queue.Enqueue(ctx, build.ID); err != nil {
		// Could not schedule: mark failed so the state is not stuck pending.
		build.Status = domain.BuildFailed
		build.Error = fmt.Sprintf("enqueue: %v", err)
		build.UpdatedAt = uc.now().UTC()
		_ = uc.repo.UpdateBuild(ctx, build)
		return build, fmt.Errorf("build: enqueue: %w", err)
	}
	return build, nil
}

// ProcessBuild runs the indexing pipeline for a previously-enqueued build:
// provision the bank, retain every member document, and mark the build ready.
// Called by the background worker. Safe to call only on pending builds.
func (uc *KnowledgeBaseUseCase) ProcessBuild(ctx context.Context, buildID uuid.UUID) error {
	build, err := uc.repo.GetBuild(ctx, buildID)
	if err != nil {
		return fmt.Errorf("process build: load: %w", err)
	}
	if build.Status != domain.BuildPending {
		// Already processed, failed, or in flight — nothing to do.
		return nil
	}
	members, err := uc.repo.ListDocuments(ctx, build.KnowledgeBaseID)
	if err != nil {
		return uc.failBuild(ctx, build, fmt.Errorf("list documents: %w", err))
	}

	if err := uc.runBuild(ctx, build, members); err != nil {
		return uc.failBuild(ctx, build, err)
	}

	build.Status = domain.BuildReady
	build.UpdatedAt = uc.now().UTC()
	if err := uc.repo.UpdateBuild(ctx, build); err != nil {
		return fmt.Errorf("process build: finalize: %w", err)
	}
	return nil
}

// failBuild records a failed build and returns the underlying error.
func (uc *KnowledgeBaseUseCase) failBuild(ctx context.Context, build *domain.Build, cause error) error {
	build.Status = domain.BuildFailed
	build.Error = cause.Error()
	build.UpdatedAt = uc.now().UTC()
	_ = uc.repo.UpdateBuild(ctx, build)
	return cause
}

// runBuild provisions the bank and retains all member documents.
func (uc *KnowledgeBaseUseCase) runBuild(ctx context.Context, build *domain.Build, members []domain.Document) error {
	build.Status = domain.BuildProcessing
	build.UpdatedAt = uc.now().UTC()
	if err := uc.repo.UpdateBuild(ctx, build); err != nil {
		return fmt.Errorf("mark processing: %w", err)
	}

	if err := uc.indexer.CreateBank(ctx, build.BankID); err != nil {
		return fmt.Errorf("create bank: %w", err)
	}

	ragDocs := make([]domain.RAGDocument, 0, len(members))
	for i := range members {
		doc := members[i]
		content, err := uc.readContent(ctx, doc.StorageKey)
		if err != nil {
			return fmt.Errorf("read %s: %w", doc.ID, err)
		}
		filename := doc.Filename
		if filename == "" {
			filename = doc.Title
		}
		ragDocs = append(ragDocs, domain.RAGDocument{
			DocumentID: doc.ID.String(),
			Title:      doc.Title,
			Filename:   filename,
			Content:    content,
			Metadata:   doc.Metadata,
		})
	}
	if err := uc.indexer.Retain(ctx, build.BankID, ragDocs); err != nil {
		return fmt.Errorf("retain documents: %w", err)
	}
	return nil
}

func (uc *KnowledgeBaseUseCase) readContent(ctx context.Context, key string) ([]byte, error) {
	rc, err := uc.storage.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// Enable activates a ready build for search and enables the knowledge base.
// Switching to a previously-built ready build is an instant rollback — no
// re-processing occurs.
func (uc *KnowledgeBaseUseCase) Enable(ctx context.Context, kbID, buildID uuid.UUID) (*domain.KnowledgeBase, error) {
	kb, err := uc.repo.GetByID(ctx, kbID)
	if err != nil {
		return nil, fmt.Errorf("enable: kb: %w", err)
	}
	build, err := uc.repo.GetBuild(ctx, buildID)
	if err != nil {
		return nil, fmt.Errorf("enable: build: %w", err)
	}
	if build.KnowledgeBaseID != kbID {
		return nil, fmt.Errorf("enable: %w: build does not belong to knowledge base", domain.ErrInvalidInput)
	}
	if build.Status != domain.BuildReady {
		return nil, fmt.Errorf("enable: %w: build is not ready (status %s)", domain.ErrConflict, build.Status)
	}

	kb.Enabled = true
	kb.ActiveBuildID = &build.ID
	kb.UpdatedAt = uc.now().UTC()
	if err := uc.repo.Update(ctx, kb); err != nil {
		return nil, fmt.Errorf("enable: persist: %w", err)
	}
	return kb, nil
}

// Disable turns off search for a knowledge base without dropping its builds.
func (uc *KnowledgeBaseUseCase) Disable(ctx context.Context, kbID uuid.UUID) (*domain.KnowledgeBase, error) {
	kb, err := uc.repo.GetByID(ctx, kbID)
	if err != nil {
		return nil, fmt.Errorf("disable: kb: %w", err)
	}
	kb.Enabled = false
	kb.UpdatedAt = uc.now().UTC()
	if err := uc.repo.Update(ctx, kb); err != nil {
		return nil, fmt.Errorf("disable: persist: %w", err)
	}
	return kb, nil
}

// ListBuilds returns all builds for a knowledge base, for rollback selection.
func (uc *KnowledgeBaseUseCase) ListBuilds(ctx context.Context, kbID uuid.UUID) ([]domain.Build, error) {
	return uc.repo.ListBuilds(ctx, kbID)
}

// SearchOptions tunes a knowledge base search.
type SearchOptions struct {
	TopK              int
	IncludeReferences bool // resolve file links + page numbers per result
}

// Search runs a similarity query against a knowledge base's active build. When
// opts.IncludeReferences is set, each result is enriched with a public file
// link, filename, and the page number its source chunk was found on.
func (uc *KnowledgeBaseUseCase) Search(ctx context.Context, kbID uuid.UUID, query string, opts SearchOptions) ([]domain.SearchResult, error) {
	if query == "" {
		return nil, fmt.Errorf("search: %w: query required", domain.ErrInvalidInput)
	}
	kb, err := uc.repo.GetByID(ctx, kbID)
	if err != nil {
		return nil, fmt.Errorf("search: kb: %w", err)
	}
	if !kb.IsSearchable() {
		return nil, fmt.Errorf("search: %w: knowledge base is not enabled or has no active build", domain.ErrConflict)
	}
	build, err := uc.repo.GetBuild(ctx, *kb.ActiveBuildID)
	if err != nil {
		return nil, fmt.Errorf("search: active build: %w", err)
	}
	results, err := uc.indexer.Recall(ctx, build.BankID, query, opts.TopK)
	if err != nil {
		return nil, fmt.Errorf("search: recall: %w", err)
	}
	if opts.IncludeReferences {
		uc.addReferences(ctx, results)
	}
	return results, nil
}

// addReferences enriches results in place with file URL, filename, and page.
// It resolves each distinct document once and caches its extracted pages so a
// document is fetched and parsed at most once per search.
func (uc *KnowledgeBaseUseCase) addReferences(ctx context.Context, results []domain.SearchResult) {
	type cacheEntry struct {
		doc   *domain.Document
		pages []string
	}
	cache := map[string]*cacheEntry{}

	for i := range results {
		docID := results[i].DocumentID
		if docID == "" {
			continue
		}
		entry, ok := cache[docID]
		if !ok {
			entry = &cacheEntry{}
			if id, err := uuid.Parse(docID); err == nil {
				if doc, err := uc.docs.GetByID(ctx, id); err == nil {
					entry.doc = doc
					entry.pages = uc.extractPages(ctx, doc)
				}
			}
			cache[docID] = entry
		}
		if entry.doc != nil {
			enrichReference(&results[i], entry.doc, entry.pages, uc.publicBaseURL)
		}
	}
}

// extractPages reads a document from storage and extracts per-page text.
// Returns nil on any failure (page lookup then degrades to 0).
func (uc *KnowledgeBaseUseCase) extractPages(ctx context.Context, doc *domain.Document) []string {
	if uc.extractor == nil {
		return nil
	}
	content, err := uc.readContent(ctx, doc.StorageKey)
	if err != nil {
		return nil
	}
	pages, err := uc.extractor.ExtractPages(ctx, content, doc.ContentType)
	if err != nil {
		return nil
	}
	return pages
}

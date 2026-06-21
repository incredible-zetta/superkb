package usecase

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"

	"superkb/internal/domain"
)

// KnowledgeBaseUseCase manages knowledge bases: grouping documents, building
// immutable RAG snapshots (builds), enabling search, and instant rollback by
// switching the active build.
type KnowledgeBaseUseCase struct {
	repo          domain.KnowledgeBaseRepository
	feedback      domain.MemoryFeedbackRepository
	docs          domain.DocumentRepository
	storage       domain.ObjectStorage
	indexer       domain.RAGIndexer
	queue         domain.BuildQueue
	extractor     domain.TextExtractor
	converter     domain.DocumentConverter
	publicBaseURL string
	now           func() time.Time
	newBank       func(buildID uuid.UUID) string
}

// NewKnowledgeBaseUseCase wires a KnowledgeBaseUseCase. extractor and
// publicBaseURL power search references (file links + page numbers); both may
// be zero-valued to disable that enrichment.
func NewKnowledgeBaseUseCase(
	repo domain.KnowledgeBaseRepository,
	feedback domain.MemoryFeedbackRepository,
	docs domain.DocumentRepository,
	storage domain.ObjectStorage,
	indexer domain.RAGIndexer,
	queue domain.BuildQueue,
	extractor domain.TextExtractor,
	publicBaseURL string,
) *KnowledgeBaseUseCase {
	return &KnowledgeBaseUseCase{
		repo:          repo,
		feedback:      feedback,
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

// WithConverter sets an optional document converter (OCR) used to extract text
// from scanned PDFs/images before retain. When nil, builds send raw bytes to
// the indexer unchanged. Returns the receiver for chaining.
func (uc *KnowledgeBaseUseCase) WithConverter(c domain.DocumentConverter) *KnowledgeBaseUseCase {
	uc.converter = c
	return uc
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
		rd := domain.RAGDocument{
			DocumentID: doc.ID.String(),
			Title:      doc.Title,
			Filename:   filename,
			Content:    content,
			Metadata:   doc.Metadata,
		}
		// Optional OCR: extract text from scanned PDFs/images up front so
		// Hindsight indexes real text instead of failing markitdown OCR. On
		// success we send the extracted text (as a .md filename) instead of raw
		// bytes; ok=false means the converter skipped this type, so raw bytes
		// are retained unchanged.
		if uc.converter != nil {
			text, ok, cerr := uc.converter.Convert(ctx, content, doc.ContentType, filename)
			if cerr != nil {
				return fmt.Errorf("ocr %s: %w", doc.ID, cerr)
			}
			if ok {
				rd.Content = []byte(text)
				rd.Filename = ocrTextFilename(filename)
			}
		}
		ragDocs = append(ragDocs, rd)
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

// ocrTextFilename converts a source filename to a .md name so the indexer
// treats OCR output as text/markdown rather than re-parsing it as a binary.
func ocrTextFilename(filename string) string {
	if i := strings.LastIndex(filename, "."); i > 0 {
		filename = filename[:i]
	}
	if filename == "" {
		filename = "document"
	}
	return filename + ".md"
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

type RetainMemoryInput struct {
	Content  string
	Context  string
	Tags     []string
	Metadata map[string]string
}

type CurateMemoryInput struct {
	Text  string
	State string
}

type MemoryFeedbackInput struct {
	Reviewer     string
	Vote         string
	ProposedText string
}

type MemoryConsensusResult struct {
	MemoryID     string `json:"memory_id"`
	ProposedText string `json:"proposed_text,omitempty"`
	Approvals    int    `json:"approvals"`
	Rejections   int    `json:"rejections"`
	Status       string `json:"status"`
	Applied      bool   `json:"applied"`
}

// SearchOptions tunes a knowledge base search.
type SearchOptions struct {
	TopK              int
	IncludeReferences bool // resolve file links + page numbers per result
}

func (uc *KnowledgeBaseUseCase) activeBankID(ctx context.Context, kbID uuid.UUID) (string, error) {
	kb, err := uc.repo.GetByID(ctx, kbID)
	if err != nil {
		return "", fmt.Errorf("kb: %w", err)
	}
	if !kb.IsSearchable() {
		return "", fmt.Errorf("%w: knowledge base is not enabled or has no active build", domain.ErrConflict)
	}
	build, err := uc.repo.GetBuild(ctx, *kb.ActiveBuildID)
	if err != nil {
		return "", fmt.Errorf("active build: %w", err)
	}
	return build.BankID, nil
}

func (uc *KnowledgeBaseUseCase) RetainExperience(ctx context.Context, kbID uuid.UUID, in RetainMemoryInput) (*domain.Memory, error) {
	if strings.TrimSpace(in.Content) == "" {
		return nil, fmt.Errorf("retain experience: %w: content required", domain.ErrInvalidInput)
	}
	bankID, err := uc.activeBankID(ctx, kbID)
	if err != nil {
		return nil, fmt.Errorf("retain experience: %w", err)
	}
	mems, err := uc.indexer.RetainMemories(ctx, bankID, []domain.Memory{{
		Content:  in.Content,
		Type:     domain.MemoryTypeExperience,
		Context:  in.Context,
		Tags:     in.Tags,
		Metadata: in.Metadata,
	}})
	if err != nil {
		return nil, fmt.Errorf("retain experience: %w", err)
	}
	if len(mems) == 0 {
		return nil, fmt.Errorf("retain experience: no memory returned")
	}
	return &mems[0], nil
}

func (uc *KnowledgeBaseUseCase) CurateMemory(ctx context.Context, kbID uuid.UUID, memoryID string, in CurateMemoryInput) (*domain.Memory, error) {
	if strings.TrimSpace(memoryID) == "" {
		return nil, fmt.Errorf("curate memory: %w: memory_id required", domain.ErrInvalidInput)
	}
	bankID, err := uc.activeBankID(ctx, kbID)
	if err != nil {
		return nil, fmt.Errorf("curate memory: %w", err)
	}
	state := normalizeCurationState(in.State)
	mem, err := uc.indexer.CurateMemory(ctx, bankID, memoryID, domain.MemoryCuration{Text: in.Text, State: state})
	if err != nil {
		return nil, fmt.Errorf("curate memory: %w", err)
	}
	return mem, nil
}

func normalizeCurationState(state string) string {
	if state == "active" {
		return "valid"
	}
	return state
}

func (uc *KnowledgeBaseUseCase) SubmitMemoryFeedback(ctx context.Context, kbID uuid.UUID, memoryID string, in MemoryFeedbackInput) (*MemoryConsensusResult, error) {
	if uc.feedback == nil {
		return nil, fmt.Errorf("memory feedback: %w: feedback repository unavailable", domain.ErrInvalidInput)
	}
	if strings.TrimSpace(memoryID) == "" || strings.TrimSpace(in.Reviewer) == "" || strings.TrimSpace(in.Vote) == "" {
		return nil, fmt.Errorf("memory feedback: %w: memory_id, reviewer, and vote required", domain.ErrInvalidInput)
	}
	if in.Vote != "approve" && in.Vote != "reject" {
		return nil, fmt.Errorf("memory feedback: %w: vote must be approve or reject", domain.ErrInvalidInput)
	}
	consensus, err := uc.feedback.AddMemoryFeedback(ctx, domain.MemoryFeedback{
		KnowledgeBaseID: kbID,
		MemoryID:        memoryID,
		Reviewer:        in.Reviewer,
		Vote:            in.Vote,
		ProposedText:    in.ProposedText,
	})
	if err != nil {
		return nil, fmt.Errorf("memory feedback: %w", err)
	}
	if in.Vote == "approve" && consensus.Approvals >= 2 && consensus.ProposedText != "" && !consensus.Applied {
		if _, err := uc.CurateMemory(ctx, kbID, memoryID, CurateMemoryInput{Text: consensus.ProposedText, State: "active"}); err != nil {
			return nil, fmt.Errorf("memory feedback: apply consensus: %w", err)
		}
		if err := uc.feedback.MarkMemoryConsensusApplied(ctx, kbID, memoryID, consensus.ProposedText); err != nil {
			return nil, fmt.Errorf("memory feedback: mark applied: %w", err)
		}
		consensus.Status = "applied"
		consensus.Applied = true
	}
	return &MemoryConsensusResult{
		MemoryID:     consensus.MemoryID,
		ProposedText: consensus.ProposedText,
		Approvals:    consensus.Approvals,
		Rejections:   consensus.Rejections,
		Status:       consensus.Status,
		Applied:      consensus.Applied,
	}, nil
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

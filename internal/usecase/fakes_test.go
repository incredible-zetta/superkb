package usecase

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/google/uuid"

	"superkb/internal/domain"
)

// --- fake document repo ----------------------------------------------------

type fakeDocRepo struct {
	mu   sync.Mutex
	docs map[uuid.UUID]*domain.Document
}

func newFakeDocRepo() *fakeDocRepo { return &fakeDocRepo{docs: map[uuid.UUID]*domain.Document{}} }

func (r *fakeDocRepo) Create(_ context.Context, d *domain.Document) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *d
	r.docs[d.ID] = &cp
	return nil
}

func (r *fakeDocRepo) GetByID(_ context.Context, id uuid.UUID) (*domain.Document, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.docs[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *d
	return &cp, nil
}

func (r *fakeDocRepo) List(_ context.Context, limit, offset int) ([]domain.Document, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var all []domain.Document
	for _, d := range r.docs {
		all = append(all, *d)
	}
	if offset >= len(all) {
		return nil, nil
	}
	end := offset + limit
	if end > len(all) {
		end = len(all)
	}
	return all[offset:end], nil
}

func (r *fakeDocRepo) Delete(_ context.Context, id uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.docs[id]; !ok {
		return domain.ErrNotFound
	}
	delete(r.docs, id)
	return nil
}

// --- fake object storage ---------------------------------------------------

type fakeStorage struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func newFakeStorage() *fakeStorage { return &fakeStorage{objects: map[string][]byte{}} }

func (s *fakeStorage) Put(_ context.Context, key string, body io.Reader, _ int64, _ string) error {
	b, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = b
	return nil
}

func (s *fakeStorage) Get(_ context.Context, key string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.objects[key]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (s *fakeStorage) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, key)
	return nil
}

// --- fake RAG indexer ------------------------------------------------------

type retainedMemoriesCall struct {
	bankID   string
	memories []domain.Memory
}

type fakeIndexer struct {
	mu               sync.Mutex
	banks            map[string][]domain.RAGDocument
	retainedMemories []retainedMemoriesCall
	curatedBankID    string
	curatedMemoryID  string
	curatedText      string
	recallFn         func(bankID, query string, topK int) []domain.SearchResult
	createErr        error
	retainErr        error
}

func newFakeIndexer() *fakeIndexer { return &fakeIndexer{banks: map[string][]domain.RAGDocument{}} }

func (i *fakeIndexer) CreateBank(_ context.Context, bankID string) error {
	if i.createErr != nil {
		return i.createErr
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.banks[bankID] = nil
	return nil
}

func (i *fakeIndexer) Retain(_ context.Context, bankID string, docs []domain.RAGDocument) error {
	if i.retainErr != nil {
		return i.retainErr
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.banks[bankID] = append(i.banks[bankID], docs...)
	return nil
}

func (i *fakeIndexer) Recall(_ context.Context, bankID, query string, topK int) ([]domain.SearchResult, error) {
	if i.recallFn != nil {
		return i.recallFn(bankID, query, topK), nil
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	var out []domain.SearchResult
	for _, d := range i.banks[bankID] {
		out = append(out, domain.SearchResult{DocumentID: d.DocumentID, Content: string(d.Content), Score: 1.0})
	}
	return out, nil
}

func (i *fakeIndexer) RetainMemories(_ context.Context, bankID string, memories []domain.Memory) ([]domain.Memory, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	out := append([]domain.Memory(nil), memories...)
	for n := range out {
		if out[n].ID == "" {
			out[n].ID = fmt.Sprintf("mem-%d", n+1)
		}
	}
	i.retainedMemories = append(i.retainedMemories, retainedMemoriesCall{bankID: bankID, memories: out})
	return out, nil
}

func (i *fakeIndexer) CurateMemory(_ context.Context, bankID, memoryID string, curation domain.MemoryCuration) (*domain.Memory, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.curatedBankID = bankID
	i.curatedMemoryID = memoryID
	i.curatedText = curation.Text
	return &domain.Memory{ID: memoryID, Content: curation.Text}, nil
}

func (i *fakeIndexer) DeleteBank(_ context.Context, bankID string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	delete(i.banks, bankID)
	return nil
}

// --- fake knowledge base repo ----------------------------------------------

type fakeKBRepo struct {
	mu      sync.Mutex
	kbs     map[uuid.UUID]*domain.KnowledgeBase
	members map[uuid.UUID]map[uuid.UUID]bool
	builds  map[uuid.UUID]*domain.Build
	docs    *fakeDocRepo
}

func newFakeKBRepo(docs *fakeDocRepo) *fakeKBRepo {
	return &fakeKBRepo{
		kbs:     map[uuid.UUID]*domain.KnowledgeBase{},
		members: map[uuid.UUID]map[uuid.UUID]bool{},
		builds:  map[uuid.UUID]*domain.Build{},
		docs:    docs,
	}
}

func (r *fakeKBRepo) Create(_ context.Context, kb *domain.KnowledgeBase) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *kb
	r.kbs[kb.ID] = &cp
	return nil
}

func (r *fakeKBRepo) GetByID(_ context.Context, id uuid.UUID) (*domain.KnowledgeBase, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	kb, ok := r.kbs[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *kb
	return &cp, nil
}

func (r *fakeKBRepo) List(_ context.Context, limit, offset int) ([]domain.KnowledgeBase, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var all []domain.KnowledgeBase
	for _, kb := range r.kbs {
		all = append(all, *kb)
	}
	if offset >= len(all) {
		return nil, nil
	}
	end := offset + limit
	if end > len(all) {
		end = len(all)
	}
	return all[offset:end], nil
}

func (r *fakeKBRepo) Update(_ context.Context, kb *domain.KnowledgeBase) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.kbs[kb.ID]; !ok {
		return domain.ErrNotFound
	}
	cp := *kb
	r.kbs[kb.ID] = &cp
	return nil
}

func (r *fakeKBRepo) Delete(_ context.Context, id uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.kbs[id]; !ok {
		return domain.ErrNotFound
	}
	delete(r.kbs, id)
	return nil
}

func (r *fakeKBRepo) AddDocument(_ context.Context, kbID, documentID uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.members[kbID] == nil {
		r.members[kbID] = map[uuid.UUID]bool{}
	}
	r.members[kbID][documentID] = true
	return nil
}

func (r *fakeKBRepo) RemoveDocument(_ context.Context, kbID, documentID uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.members[kbID], documentID)
	return nil
}

func (r *fakeKBRepo) ListDocuments(_ context.Context, kbID uuid.UUID) ([]domain.Document, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []domain.Document
	for docID := range r.members[kbID] {
		if d, err := r.docs.GetByID(context.Background(), docID); err == nil {
			out = append(out, *d)
		}
	}
	return out, nil
}

func (r *fakeKBRepo) CreateBuild(_ context.Context, b *domain.Build) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *b
	r.builds[b.ID] = &cp
	return nil
}

func (r *fakeKBRepo) GetBuild(_ context.Context, buildID uuid.UUID) (*domain.Build, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, ok := r.builds[buildID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *b
	return &cp, nil
}

func (r *fakeKBRepo) UpdateBuild(_ context.Context, b *domain.Build) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *b
	r.builds[b.ID] = &cp
	return nil
}

func (r *fakeKBRepo) ListBuilds(_ context.Context, kbID uuid.UUID) ([]domain.Build, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []domain.Build
	for _, b := range r.builds {
		if b.KnowledgeBaseID == kbID {
			out = append(out, *b)
		}
	}
	return out, nil
}

// --- fake build queue ------------------------------------------------------

// syncQueue processes builds immediately on Enqueue via the given processor,
// so tests exercise the full pipeline without spinning up the worker.
type syncQueue struct {
	processor BuildProcessor
	enqueued  []uuid.UUID
}

func (q *syncQueue) Enqueue(_ context.Context, buildID uuid.UUID) error {
	q.enqueued = append(q.enqueued, buildID)
	return q.processor.ProcessBuild(context.Background(), buildID)
}

// --- fake memory feedback repo --------------------------------------------

type fakeFeedbackRepo struct {
	mu        sync.Mutex
	feedbacks []domain.MemoryFeedback
	applied   map[string]bool
}

func newFakeFeedbackRepo() *fakeFeedbackRepo {
	return &fakeFeedbackRepo{applied: map[string]bool{}}
}

func feedbackKey(kbID uuid.UUID, memoryID, text string) string {
	return kbID.String() + "|" + memoryID + "|" + text
}

func (r *fakeFeedbackRepo) AddMemoryFeedback(_ context.Context, f domain.MemoryFeedback) (domain.MemoryConsensus, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.feedbacks = append(r.feedbacks, f)
	c := domain.MemoryConsensus{MemoryID: f.MemoryID, ProposedText: f.ProposedText, Status: "pending_consensus"}
	seen := map[string]bool{}
	for _, item := range r.feedbacks {
		if item.KnowledgeBaseID != f.KnowledgeBaseID || item.MemoryID != f.MemoryID || item.ProposedText != f.ProposedText {
			continue
		}
		key := item.Reviewer + "|" + item.Vote
		if seen[key] {
			continue
		}
		seen[key] = true
		switch item.Vote {
		case "approve":
			c.Approvals++
		case "reject":
			c.Rejections++
		}
	}
	if r.applied[feedbackKey(f.KnowledgeBaseID, f.MemoryID, f.ProposedText)] {
		c.Status = "applied"
		c.Applied = true
	}
	return c, nil
}

func (r *fakeFeedbackRepo) MarkMemoryConsensusApplied(_ context.Context, kbID uuid.UUID, memoryID, proposedText string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.applied[feedbackKey(kbID, memoryID, proposedText)] = true
	return nil
}

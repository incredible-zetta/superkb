package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"superkb/internal/domain"
	"superkb/internal/usecase"
)

func TestServerRegistersWriteAndMemoryTools(t *testing.T) {
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
		"upload_document", "delete_document",
		"create_knowledge_base", "delete_knowledge_base",
		"add_document_to_knowledge_base", "remove_document_from_knowledge_base",
		"enable_knowledge_base_build", "disable_knowledge_base",
		"build_knowledge_base", "process_build",
		"retain_experience", "curate_memory", "submit_memory_feedback",
	} {
		if !got[want] {
			t.Errorf("missing tool %q", want)
		}
	}
}

func TestUploadDocumentTool(t *testing.T) {
	var gotIn usecase.UploadInput
	id := uuid.New()
	now := time.Now().UTC()
	docs := &fakeDocService{
		uploadFn: func(_ context.Context, in usecase.UploadInput) (*domain.Document, error) {
			gotIn = in
			return &domain.Document{ID: id, Title: in.Title, CreatedAt: now, UpdatedAt: now}, nil
		},
	}
	s := connect(t, docs, &fakeKBService{})
	out := callText(t, s, "upload_document", map[string]any{
		"title": "Notes", "content": "hello world", "metadata": map[string]any{"src": "mcp"},
	})
	if gotIn.Title != "Notes" || string(gotIn.Content) != "hello world" || gotIn.Metadata["src"] != "mcp" {
		t.Fatalf("unexpected upload input: %+v", gotIn)
	}
	var doc domain.Document
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("decode: %v (%s)", err, out)
	}
	if doc.ID != id {
		t.Fatalf("unexpected doc: %s", out)
	}
}

func TestBuildKnowledgeBaseTool(t *testing.T) {
	kbID := uuid.New()
	buildID := uuid.New()
	kbs := &fakeKBService{
		buildFn: func(_ context.Context, k uuid.UUID) (*domain.Build, error) {
			if k != kbID {
				t.Fatalf("wrong kb id: %s", k)
			}
			return &domain.Build{ID: buildID, KnowledgeBaseID: k, Status: domain.BuildPending, BankID: "bank-x"}, nil
		},
	}
	s := connect(t, &fakeDocService{}, kbs)
	out := callText(t, s, "build_knowledge_base", map[string]any{"kb_id": kbID.String()})
	var b domain.Build
	if err := json.Unmarshal([]byte(out), &b); err != nil {
		t.Fatalf("decode: %v (%s)", err, out)
	}
	if b.ID != buildID || b.Status != domain.BuildPending {
		t.Fatalf("unexpected build: %s", out)
	}
}

func TestSubmitMemoryFeedbackTool(t *testing.T) {
	kbID := uuid.New()
	var gotMemory string
	var gotIn usecase.MemoryFeedbackInput
	kbs := &fakeKBService{
		feedbackFn: func(_ context.Context, k uuid.UUID, memoryID string, in usecase.MemoryFeedbackInput) (*usecase.MemoryConsensusResult, error) {
			if k != kbID {
				t.Fatalf("wrong kb id: %s", k)
			}
			gotMemory = memoryID
			gotIn = in
			return &usecase.MemoryConsensusResult{MemoryID: memoryID, ProposedText: in.ProposedText, Approvals: 2, Status: "applied", Applied: true}, nil
		},
	}
	s := connect(t, &fakeDocService{}, kbs)
	out := callText(t, s, "submit_memory_feedback", map[string]any{
		"kb_id": kbID.String(), "memory_id": "mem-1", "reviewer": "alice", "vote": "approve", "proposed_text": "Corrected",
	})
	if gotMemory != "mem-1" || gotIn.Reviewer != "alice" || gotIn.Vote != "approve" || gotIn.ProposedText != "Corrected" {
		t.Fatalf("unexpected feedback input: %s %+v", gotMemory, gotIn)
	}
	var res usecase.MemoryConsensusResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("decode: %v (%s)", err, out)
	}
	if res.Status != "applied" || !res.Applied || res.Approvals != 2 {
		t.Fatalf("unexpected consensus result: %s", out)
	}
}

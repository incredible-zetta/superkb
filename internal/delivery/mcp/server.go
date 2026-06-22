// Package mcp exposes SuperKB usecases as Model Context Protocol tools so MCP
// clients (agents) can drive documents, knowledge bases, builds, search, and
// memory feedback. Handlers are thin adapters over the usecase layer; business
// rules stay in internal/usecase.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"superkb/internal/domain"
	"superkb/internal/usecase"
)

// DocumentService is the MCP view of the document usecase.
type DocumentService interface {
	Upload(ctx context.Context, in usecase.UploadInput) (*domain.Document, error)
	Get(ctx context.Context, id uuid.UUID) (*domain.Document, error)
	List(ctx context.Context, limit, offset int) ([]domain.Document, error)
	Delete(ctx context.Context, id uuid.UUID) error
	Source(ctx context.Context, id uuid.UUID) (*usecase.DocumentSource, error)
}

// KnowledgeBaseService is the MCP view of the knowledge base usecase.
type KnowledgeBaseService interface {
	Create(ctx context.Context, name, description string) (*domain.KnowledgeBase, error)
	Get(ctx context.Context, id uuid.UUID) (*domain.KnowledgeBase, error)
	List(ctx context.Context, limit, offset int) ([]domain.KnowledgeBase, error)
	Delete(ctx context.Context, id uuid.UUID) error
	AddDocument(ctx context.Context, kbID, documentID uuid.UUID) error
	RemoveDocument(ctx context.Context, kbID, documentID uuid.UUID) error
	Build(ctx context.Context, kbID uuid.UUID) (*domain.Build, error)
	ProcessBuild(ctx context.Context, buildID uuid.UUID) error
	Enable(ctx context.Context, kbID, buildID uuid.UUID) (*domain.KnowledgeBase, error)
	Disable(ctx context.Context, kbID uuid.UUID) (*domain.KnowledgeBase, error)
	ListBuilds(ctx context.Context, kbID uuid.UUID) ([]domain.Build, error)
	Search(ctx context.Context, kbID uuid.UUID, query string, opts usecase.SearchOptions) ([]domain.SearchResult, error)
	RetainExperience(ctx context.Context, kbID uuid.UUID, in usecase.RetainMemoryInput) (*domain.Memory, error)
	CurateMemory(ctx context.Context, kbID uuid.UUID, memoryID string, in usecase.CurateMemoryInput) (*domain.Memory, error)
	SubmitMemoryFeedback(ctx context.Context, kbID uuid.UUID, memoryID string, in usecase.MemoryFeedbackInput) (*usecase.MemoryConsensusResult, error)
}

// handler is the bound dependency set used by tool implementations.
type handler struct {
	docs DocumentService
	kbs  KnowledgeBaseService
}

// NewServer builds an MCP server with all SuperKB tools registered.
func NewServer(docs DocumentService, kbs KnowledgeBaseService) *mcpsdk.Server {
	h := &handler{docs: docs, kbs: kbs}
	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "superkb", Version: "0.1.0"}, nil)
	h.registerReadTools(server)
	return server
}

// jsonResult marshals v to indented JSON as a tool text result.
func jsonResult(v any) (*mcpsdk.CallToolResult, any, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, nil, fmt.Errorf("marshal result: %w", err)
	}
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(b)}},
	}, nil, nil
}

// --- read tool argument types ---------------------------------------------

type listArgs struct {
	Limit  int `json:"limit,omitempty"`
	Offset int `json:"offset,omitempty"`
}

type documentIDArgs struct {
	ID string `json:"id"`
}

type kbIDArgs struct {
	KBID string `json:"kb_id"`
}

type searchArgs struct {
	KBID              string `json:"kb_id"`
	Query             string `json:"query"`
	TopK              int    `json:"top_k,omitempty"`
	IncludeReferences bool   `json:"include_references,omitempty"`
}

func (h *handler) registerReadTools(server *mcpsdk.Server) {
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "list_documents",
		Description: "List stored documents (metadata only).",
	}, h.listDocuments)

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "get_document",
		Description: "Get a document's metadata by id.",
	}, h.getDocument)

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "get_document_source",
		Description: "Get a document's extracted per-page text and public file link.",
	}, h.getDocumentSource)

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "list_knowledge_bases",
		Description: "List knowledge bases with enabled/searchable state.",
	}, h.listKnowledgeBases)

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "get_knowledge_base",
		Description: "Get a knowledge base by id.",
	}, h.getKnowledgeBase)

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "list_builds",
		Description: "List a knowledge base's builds (for rollback selection).",
	}, h.listBuilds)

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "search_knowledge_base",
		Description: "Search a knowledge base's active build. Set include_references for file links and page numbers.",
	}, h.searchKnowledgeBase)
}

func (h *handler) listDocuments(ctx context.Context, _ *mcpsdk.CallToolRequest, args listArgs) (*mcpsdk.CallToolResult, any, error) {
	docs, err := h.docs.List(ctx, args.Limit, args.Offset)
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(docs)
}

func (h *handler) getDocument(ctx context.Context, _ *mcpsdk.CallToolRequest, args documentIDArgs) (*mcpsdk.CallToolResult, any, error) {
	id, err := uuid.Parse(args.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid id: %w", err)
	}
	doc, err := h.docs.Get(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(doc)
}

func (h *handler) getDocumentSource(ctx context.Context, _ *mcpsdk.CallToolRequest, args documentIDArgs) (*mcpsdk.CallToolResult, any, error) {
	id, err := uuid.Parse(args.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid id: %w", err)
	}
	src, err := h.docs.Source(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(src)
}

func (h *handler) listKnowledgeBases(ctx context.Context, _ *mcpsdk.CallToolRequest, args listArgs) (*mcpsdk.CallToolResult, any, error) {
	kbs, err := h.kbs.List(ctx, args.Limit, args.Offset)
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(kbs)
}

func (h *handler) getKnowledgeBase(ctx context.Context, _ *mcpsdk.CallToolRequest, args kbIDArgs) (*mcpsdk.CallToolResult, any, error) {
	id, err := uuid.Parse(args.KBID)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid kb_id: %w", err)
	}
	kb, err := h.kbs.Get(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(kb)
}

func (h *handler) listBuilds(ctx context.Context, _ *mcpsdk.CallToolRequest, args kbIDArgs) (*mcpsdk.CallToolResult, any, error) {
	id, err := uuid.Parse(args.KBID)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid kb_id: %w", err)
	}
	builds, err := h.kbs.ListBuilds(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(builds)
}

func (h *handler) searchKnowledgeBase(ctx context.Context, _ *mcpsdk.CallToolRequest, args searchArgs) (*mcpsdk.CallToolResult, any, error) {
	id, err := uuid.Parse(args.KBID)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid kb_id: %w", err)
	}
	results, err := h.kbs.Search(ctx, id, args.Query, usecase.SearchOptions{TopK: args.TopK, IncludeReferences: args.IncludeReferences})
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(results)
}

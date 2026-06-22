package mcp

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"superkb/internal/usecase"
)

// --- write tool argument types ---------------------------------------------

type uploadArgs struct {
	Title       string            `json:"title"`
	Content     string            `json:"content"`
	Filename    string            `json:"filename,omitempty"`
	ContentType string            `json:"content_type,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

type createKBArgs struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type kbDocArgs struct {
	KBID       string `json:"kb_id"`
	DocumentID string `json:"document_id"`
}

type enableArgs struct {
	KBID    string `json:"kb_id"`
	BuildID string `json:"build_id"`
}

type buildIDArgs struct {
	BuildID string `json:"build_id"`
}

func (h *handler) registerWriteTools(server *mcpsdk.Server) {
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "upload_document",
		Description: "Upload a text document (title + content). Stored raw; not vectorized until built into a KB.",
	}, h.uploadDocument)

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "delete_document",
		Description: "Delete a document and its stored content by id.",
	}, h.deleteDocument)

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "create_knowledge_base",
		Description: "Create a new (disabled, empty) knowledge base.",
	}, h.createKnowledgeBase)

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "delete_knowledge_base",
		Description: "Delete a knowledge base and reclaim its build banks.",
	}, h.deleteKnowledgeBase)

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "add_document_to_knowledge_base",
		Description: "Add a document to a knowledge base's membership set.",
	}, h.addDocument)

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "remove_document_from_knowledge_base",
		Description: "Remove a document from a knowledge base's membership set.",
	}, h.removeDocument)

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "enable_knowledge_base_build",
		Description: "Point a knowledge base's search at a ready build (instant rollback).",
	}, h.enableBuild)

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "disable_knowledge_base",
		Description: "Turn off search for a knowledge base without dropping builds.",
	}, h.disableKnowledgeBase)

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "build_knowledge_base",
		Description: "Start an async build snapshot of a knowledge base's current documents. Poll list_builds until ready, then enable.",
	}, h.buildKnowledgeBase)

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "process_build",
		Description: "Admin/debug: synchronously process a pending build by id (normally the background worker does this).",
	}, h.processBuild)
}

func (h *handler) uploadDocument(ctx context.Context, _ *mcpsdk.CallToolRequest, args uploadArgs) (*mcpsdk.CallToolResult, any, error) {
	doc, err := h.docs.Upload(ctx, usecase.UploadInput{
		Title:       args.Title,
		Filename:    args.Filename,
		ContentType: args.ContentType,
		Content:     []byte(args.Content),
		Metadata:    args.Metadata,
	})
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(doc)
}

func (h *handler) deleteDocument(ctx context.Context, _ *mcpsdk.CallToolRequest, args documentIDArgs) (*mcpsdk.CallToolResult, any, error) {
	id, err := uuid.Parse(args.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid id: %w", err)
	}
	if err := h.docs.Delete(ctx, id); err != nil {
		return nil, nil, err
	}
	return jsonResult(map[string]any{"deleted": true, "id": args.ID})
}

func (h *handler) createKnowledgeBase(ctx context.Context, _ *mcpsdk.CallToolRequest, args createKBArgs) (*mcpsdk.CallToolResult, any, error) {
	kb, err := h.kbs.Create(ctx, args.Name, args.Description)
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(kb)
}

func (h *handler) deleteKnowledgeBase(ctx context.Context, _ *mcpsdk.CallToolRequest, args kbIDArgs) (*mcpsdk.CallToolResult, any, error) {
	id, err := uuid.Parse(args.KBID)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid kb_id: %w", err)
	}
	if err := h.kbs.Delete(ctx, id); err != nil {
		return nil, nil, err
	}
	return jsonResult(map[string]any{"deleted": true, "kb_id": args.KBID})
}

func (h *handler) addDocument(ctx context.Context, _ *mcpsdk.CallToolRequest, args kbDocArgs) (*mcpsdk.CallToolResult, any, error) {
	kbID, err := uuid.Parse(args.KBID)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid kb_id: %w", err)
	}
	docID, err := uuid.Parse(args.DocumentID)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid document_id: %w", err)
	}
	if err := h.kbs.AddDocument(ctx, kbID, docID); err != nil {
		return nil, nil, err
	}
	return jsonResult(map[string]any{"added": true, "kb_id": args.KBID, "document_id": args.DocumentID})
}

func (h *handler) removeDocument(ctx context.Context, _ *mcpsdk.CallToolRequest, args kbDocArgs) (*mcpsdk.CallToolResult, any, error) {
	kbID, err := uuid.Parse(args.KBID)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid kb_id: %w", err)
	}
	docID, err := uuid.Parse(args.DocumentID)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid document_id: %w", err)
	}
	if err := h.kbs.RemoveDocument(ctx, kbID, docID); err != nil {
		return nil, nil, err
	}
	return jsonResult(map[string]any{"removed": true, "kb_id": args.KBID, "document_id": args.DocumentID})
}

func (h *handler) enableBuild(ctx context.Context, _ *mcpsdk.CallToolRequest, args enableArgs) (*mcpsdk.CallToolResult, any, error) {
	kbID, err := uuid.Parse(args.KBID)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid kb_id: %w", err)
	}
	buildID, err := uuid.Parse(args.BuildID)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid build_id: %w", err)
	}
	kb, err := h.kbs.Enable(ctx, kbID, buildID)
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(kb)
}

func (h *handler) disableKnowledgeBase(ctx context.Context, _ *mcpsdk.CallToolRequest, args kbIDArgs) (*mcpsdk.CallToolResult, any, error) {
	id, err := uuid.Parse(args.KBID)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid kb_id: %w", err)
	}
	kb, err := h.kbs.Disable(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(kb)
}

func (h *handler) buildKnowledgeBase(ctx context.Context, _ *mcpsdk.CallToolRequest, args kbIDArgs) (*mcpsdk.CallToolResult, any, error) {
	id, err := uuid.Parse(args.KBID)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid kb_id: %w", err)
	}
	build, err := h.kbs.Build(ctx, id)
	if err != nil {
		if build != nil {
			return jsonResult(build)
		}
		return nil, nil, err
	}
	return jsonResult(build)
}

func (h *handler) processBuild(ctx context.Context, _ *mcpsdk.CallToolRequest, args buildIDArgs) (*mcpsdk.CallToolResult, any, error) {
	id, err := uuid.Parse(args.BuildID)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid build_id: %w", err)
	}
	if err := h.kbs.ProcessBuild(ctx, id); err != nil {
		return nil, nil, err
	}
	return jsonResult(map[string]any{"processed": true, "build_id": args.BuildID})
}

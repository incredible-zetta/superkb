package mcp

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"superkb/internal/usecase"
)

// --- memory tool argument types --------------------------------------------

type retainExperienceArgs struct {
	KBID     string            `json:"kb_id"`
	Content  string            `json:"content"`
	Context  string            `json:"context,omitempty"`
	Tags     []string          `json:"tags,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type curateMemoryArgs struct {
	KBID     string `json:"kb_id"`
	MemoryID string `json:"memory_id"`
	Text     string `json:"text,omitempty"`
	State    string `json:"state,omitempty"`
}

type memoryFeedbackArgs struct {
	KBID         string `json:"kb_id"`
	MemoryID     string `json:"memory_id"`
	Reviewer     string `json:"reviewer"`
	Vote         string `json:"vote"`
	ProposedText string `json:"proposed_text,omitempty"`
}

func (h *handler) registerMemoryTools(server *mcpsdk.Server) {
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "retain_experience",
		Description: "Retain an experience memory (e.g. human feedback, agent action) into the KB's active build bank.",
	}, h.retainExperience)

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "curate_memory",
		Description: "Curate a memory unit. state: 'active'/'valid' to set text/revert, 'invalidated' to soft-retire. Provide text to correct the fact.",
	}, h.curateMemory)

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "submit_memory_feedback",
		Description: "Vote on a memory correction. Two approvals with the same proposed_text apply the correction (no LLM). vote: 'approve' or 'reject'.",
	}, h.submitMemoryFeedback)
}

func (h *handler) retainExperience(ctx context.Context, _ *mcpsdk.CallToolRequest, args retainExperienceArgs) (*mcpsdk.CallToolResult, any, error) {
	kbID, err := uuid.Parse(args.KBID)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid kb_id: %w", err)
	}
	mem, err := h.kbs.RetainExperience(ctx, kbID, usecase.RetainMemoryInput{
		Content:  args.Content,
		Context:  args.Context,
		Tags:     args.Tags,
		Metadata: args.Metadata,
	})
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(mem)
}

func (h *handler) curateMemory(ctx context.Context, _ *mcpsdk.CallToolRequest, args curateMemoryArgs) (*mcpsdk.CallToolResult, any, error) {
	kbID, err := uuid.Parse(args.KBID)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid kb_id: %w", err)
	}
	mem, err := h.kbs.CurateMemory(ctx, kbID, args.MemoryID, usecase.CurateMemoryInput{Text: args.Text, State: args.State})
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(mem)
}

func (h *handler) submitMemoryFeedback(ctx context.Context, _ *mcpsdk.CallToolRequest, args memoryFeedbackArgs) (*mcpsdk.CallToolResult, any, error) {
	kbID, err := uuid.Parse(args.KBID)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid kb_id: %w", err)
	}
	out, err := h.kbs.SubmitMemoryFeedback(ctx, kbID, args.MemoryID, usecase.MemoryFeedbackInput{
		Reviewer:     args.Reviewer,
		Vote:         args.Vote,
		ProposedText: args.ProposedText,
	})
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(out)
}

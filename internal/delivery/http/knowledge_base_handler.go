package http

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"superkb/internal/domain"
	"superkb/internal/usecase"
)

// KnowledgeBaseHandler serves knowledge base HTTP endpoints.
type KnowledgeBaseHandler struct {
	svc KnowledgeBaseService
}

// NewKnowledgeBaseHandler constructs a KnowledgeBaseHandler.
func NewKnowledgeBaseHandler(svc KnowledgeBaseService) *KnowledgeBaseHandler {
	return &KnowledgeBaseHandler{svc: svc}
}

// Routes mounts knowledge base routes on a chi router.
func (h *KnowledgeBaseHandler) Routes(r chi.Router) {
	r.Post("/knowledge-bases", h.create)
	r.Get("/knowledge-bases", h.list)
	r.Get("/knowledge-bases/{id}", h.get)
	r.Delete("/knowledge-bases/{id}", h.delete)

	r.Put("/knowledge-bases/{id}/documents/{docID}", h.addDocument)
	r.Delete("/knowledge-bases/{id}/documents/{docID}", h.removeDocument)

	r.Post("/knowledge-bases/{id}/builds", h.build)
	r.Get("/knowledge-bases/{id}/builds", h.listBuilds)

	r.Post("/knowledge-bases/{id}/enable", h.enable)
	r.Post("/knowledge-bases/{id}/disable", h.disable)

	r.Post("/knowledge-bases/{id}/search", h.search)
	r.Post("/knowledge-bases/{id}/experiences", h.retainExperience)
	r.Patch("/knowledge-bases/{id}/memories/{memoryID}", h.curateMemory)
	r.Post("/knowledge-bases/{id}/memories/{memoryID}/feedback", h.submitMemoryFeedback)
}

type createKBRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type kbResponse struct {
	ID            uuid.UUID `json:"id"`
	Name          string    `json:"name"`
	Description   string    `json:"description,omitempty"`
	Enabled       bool      `json:"enabled"`
	ActiveBuildID *string   `json:"active_build_id,omitempty"`
	Searchable    bool      `json:"searchable"`
	CreatedAt     string    `json:"created_at"`
	UpdatedAt     string    `json:"updated_at"`
}

func toKBResponse(kb *domain.KnowledgeBase) kbResponse {
	var active *string
	if kb.ActiveBuildID != nil {
		s := kb.ActiveBuildID.String()
		active = &s
	}
	return kbResponse{
		ID:            kb.ID,
		Name:          kb.Name,
		Description:   kb.Description,
		Enabled:       kb.Enabled,
		ActiveBuildID: active,
		Searchable:    kb.IsSearchable(),
		CreatedAt:     kb.CreatedAt.Format(timeFormat),
		UpdatedAt:     kb.UpdatedAt.Format(timeFormat),
	}
}

type buildResponse struct {
	ID              uuid.UUID `json:"id"`
	KnowledgeBaseID uuid.UUID `json:"knowledge_base_id"`
	BankID          string    `json:"bank_id"`
	Status          string    `json:"status"`
	DocumentCount   int       `json:"document_count"`
	Error           string    `json:"error,omitempty"`
	CreatedAt       string    `json:"created_at"`
	UpdatedAt       string    `json:"updated_at"`
}

func toBuildResponse(b *domain.Build) buildResponse {
	return buildResponse{
		ID:              b.ID,
		KnowledgeBaseID: b.KnowledgeBaseID,
		BankID:          b.BankID,
		Status:          string(b.Status),
		DocumentCount:   b.DocumentCount,
		Error:           b.Error,
		CreatedAt:       b.CreatedAt.Format(timeFormat),
		UpdatedAt:       b.UpdatedAt.Format(timeFormat),
	}
}

func (h *KnowledgeBaseHandler) create(w http.ResponseWriter, r *http.Request) {
	var req createKBRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	kb, err := h.svc.Create(r.Context(), req.Name, req.Description)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toKBResponse(kb))
}

func (h *KnowledgeBaseHandler) list(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	kbs, err := h.svc.List(r.Context(), limit, offset)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	out := make([]kbResponse, len(kbs))
	for i := range kbs {
		out[i] = toKBResponse(&kbs[i])
	}
	writeJSON(w, http.StatusOK, map[string]any{"knowledge_bases": out})
}

func (h *KnowledgeBaseHandler) get(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r, "id")
	if !ok {
		return
	}
	kb, err := h.svc.Get(r.Context(), id)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toKBResponse(kb))
}

func (h *KnowledgeBaseHandler) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r, "id")
	if !ok {
		return
	}
	if err := h.svc.Delete(r.Context(), id); err != nil {
		writeDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *KnowledgeBaseHandler) addDocument(w http.ResponseWriter, r *http.Request) {
	kbID, ok := parseID(w, r, "id")
	if !ok {
		return
	}
	docID, ok := parseID(w, r, "docID")
	if !ok {
		return
	}
	if err := h.svc.AddDocument(r.Context(), kbID, docID); err != nil {
		writeDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *KnowledgeBaseHandler) removeDocument(w http.ResponseWriter, r *http.Request) {
	kbID, ok := parseID(w, r, "id")
	if !ok {
		return
	}
	docID, ok := parseID(w, r, "docID")
	if !ok {
		return
	}
	if err := h.svc.RemoveDocument(r.Context(), kbID, docID); err != nil {
		writeDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *KnowledgeBaseHandler) build(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r, "id")
	if !ok {
		return
	}
	b, err := h.svc.Build(r.Context(), id)
	if err != nil {
		// A failed build still returns the build record with its error.
		if b != nil {
			writeJSON(w, http.StatusAccepted, toBuildResponse(b))
			return
		}
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toBuildResponse(b))
}

func (h *KnowledgeBaseHandler) listBuilds(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r, "id")
	if !ok {
		return
	}
	builds, err := h.svc.ListBuilds(r.Context(), id)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	out := make([]buildResponse, len(builds))
	for i := range builds {
		out[i] = toBuildResponse(&builds[i])
	}
	writeJSON(w, http.StatusOK, map[string]any{"builds": out})
}

type enableRequest struct {
	BuildID string `json:"build_id"`
}

func (h *KnowledgeBaseHandler) enable(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r, "id")
	if !ok {
		return
	}
	var req enableRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	buildID, err := uuid.Parse(req.BuildID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid build_id")
		return
	}
	kb, err := h.svc.Enable(r.Context(), id, buildID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toKBResponse(kb))
}

func (h *KnowledgeBaseHandler) disable(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r, "id")
	if !ok {
		return
	}
	kb, err := h.svc.Disable(r.Context(), id)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toKBResponse(kb))
}

type searchRequest struct {
	Query             string `json:"query"`
	TopK              int    `json:"top_k"`
	IncludeReferences bool   `json:"include_references"`
}

type memoryResponse struct {
	ID       string            `json:"id"`
	Content  string            `json:"content"`
	Type     string            `json:"type"`
	Context  string            `json:"context,omitempty"`
	Tags     []string          `json:"tags,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

func toMemoryResponse(m *domain.Memory) memoryResponse {
	return memoryResponse{ID: m.ID, Content: m.Content, Type: string(m.Type), Context: m.Context, Tags: m.Tags, Metadata: m.Metadata}
}

type retainExperienceRequest struct {
	Content  string            `json:"content"`
	Context  string            `json:"context"`
	Tags     []string          `json:"tags"`
	Metadata map[string]string `json:"metadata"`
}

type curateMemoryRequestBody struct {
	Text  string `json:"text"`
	State string `json:"state"`
}

type memoryFeedbackRequest struct {
	Reviewer     string `json:"reviewer"`
	Vote         string `json:"vote"`
	ProposedText string `json:"proposed_text"`
}

type searchHit struct {
	MemoryID   string   `json:"memory_id,omitempty"`
	DocumentID string   `json:"document_id,omitempty"`
	Content    string   `json:"content"`
	Score      float64  `json:"score"`
	Context    string   `json:"context,omitempty"`
	Entities   []string `json:"entities,omitempty"`
	ChunkID    string   `json:"chunk_id,omitempty"`
	ChunkIndex int      `json:"chunk_index,omitempty"`
	// ChunkText is the source snippet a UI can highlight / anchor to within
	// the original document.
	ChunkText string `json:"chunk_text,omitempty"`
	// Reference fields (populated when include_references is true).
	FileURL  string `json:"file_url,omitempty"`
	Filename string `json:"filename,omitempty"`
	Page     int    `json:"page,omitempty"`
}

func (h *KnowledgeBaseHandler) retainExperience(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r, "id")
	if !ok {
		return
	}
	var req retainExperienceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	mem, err := h.svc.RetainExperience(r.Context(), id, usecase.RetainMemoryInput{
		Content:  req.Content,
		Context:  req.Context,
		Tags:     req.Tags,
		Metadata: req.Metadata,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toMemoryResponse(mem))
}

func (h *KnowledgeBaseHandler) curateMemory(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r, "id")
	if !ok {
		return
	}
	memoryID := chi.URLParam(r, "memoryID")
	if memoryID == "" {
		writeError(w, http.StatusBadRequest, "invalid memoryID")
		return
	}
	var req curateMemoryRequestBody
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	mem, err := h.svc.CurateMemory(r.Context(), id, memoryID, usecase.CurateMemoryInput{Text: req.Text, State: req.State})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toMemoryResponse(mem))
}

func (h *KnowledgeBaseHandler) submitMemoryFeedback(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r, "id")
	if !ok {
		return
	}
	memoryID := chi.URLParam(r, "memoryID")
	if memoryID == "" {
		writeError(w, http.StatusBadRequest, "invalid memoryID")
		return
	}
	var req memoryFeedbackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	out, err := h.svc.SubmitMemoryFeedback(r.Context(), id, memoryID, usecase.MemoryFeedbackInput{
		Reviewer:     req.Reviewer,
		Vote:         req.Vote,
		ProposedText: req.ProposedText,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *KnowledgeBaseHandler) search(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r, "id")
	if !ok {
		return
	}
	var req searchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	results, err := h.svc.Search(r.Context(), id, req.Query, usecase.SearchOptions{
		TopK:              req.TopK,
		IncludeReferences: req.IncludeReferences,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	hits := make([]searchHit, len(results))
	for i, res := range results {
		hits[i] = searchHit{
			MemoryID:   res.MemoryID,
			DocumentID: res.DocumentID,
			Content:    res.Content,
			Score:      res.Score,
			Context:    res.Context,
			Entities:   res.Entities,
			ChunkID:    res.ChunkID,
			ChunkIndex: res.ChunkIndex,
			ChunkText:  res.ChunkText,
			FileURL:    res.FileURL,
			Filename:   res.Filename,
			Page:       res.Page,
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": hits})
}

// parseID parses a UUID URL param, writing a 400 on failure.
func parseID(w http.ResponseWriter, r *http.Request, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, name))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid "+name)
		return uuid.Nil, false
	}
	return id, true
}

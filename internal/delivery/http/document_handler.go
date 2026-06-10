package http

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"superkb/internal/domain"
	"superkb/internal/usecase"
)

// maxUploadBytes caps a single multipart upload (100 MB).
const maxUploadBytes = 100 << 20

// DocumentHandler serves raw document HTTP endpoints.
type DocumentHandler struct {
	svc DocumentService
}

// NewDocumentHandler constructs a DocumentHandler.
func NewDocumentHandler(svc DocumentService) *DocumentHandler {
	return &DocumentHandler{svc: svc}
}

// Routes mounts document routes on a chi router.
func (h *DocumentHandler) Routes(r chi.Router) {
	r.Post("/documents", h.upload)
	r.Get("/documents", h.list)
	r.Get("/documents/{id}", h.get)
	r.Get("/documents/{id}/source", h.source)
	r.Delete("/documents/{id}", h.delete)
}

type uploadRequest struct {
	Title       string            `json:"title"`
	Filename    string            `json:"filename"`
	ContentType string            `json:"content_type"`
	Content     string            `json:"content"`
	Metadata    map[string]string `json:"metadata"`
}

type documentResponse struct {
	ID          uuid.UUID         `json:"id"`
	Title       string            `json:"title"`
	Filename    string            `json:"filename,omitempty"`
	ContentType string            `json:"content_type"`
	StorageKey  string            `json:"storage_key"`
	SizeBytes   int64             `json:"size_bytes"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	CreatedAt   string            `json:"created_at"`
	UpdatedAt   string            `json:"updated_at"`
}

func toDocumentResponse(d *domain.Document) documentResponse {
	return documentResponse{
		ID:          d.ID,
		Title:       d.Title,
		Filename:    d.Filename,
		ContentType: d.ContentType,
		StorageKey:  d.StorageKey,
		SizeBytes:   d.SizeBytes,
		Metadata:    d.Metadata,
		CreatedAt:   d.CreatedAt.Format(timeFormat),
		UpdatedAt:   d.UpdatedAt.Format(timeFormat),
	}
}

func (h *DocumentHandler) upload(w http.ResponseWriter, r *http.Request) {
	ct := r.Header.Get("Content-Type")
	mediaType, _, _ := mime.ParseMediaType(ct)

	var in usecase.UploadInput
	var err error
	if mediaType == "multipart/form-data" {
		in, err = parseMultipartUpload(r)
	} else {
		in, err = parseJSONUpload(r)
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	doc, err := h.svc.Upload(r.Context(), in)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toDocumentResponse(doc))
}

// parseJSONUpload reads a JSON upload body (base64-free plain text content).
func parseJSONUpload(r *http.Request) (usecase.UploadInput, error) {
	var req uploadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return usecase.UploadInput{}, fmt.Errorf("invalid JSON body")
	}
	return usecase.UploadInput{
		Title:       req.Title,
		Filename:    req.Filename,
		ContentType: req.ContentType,
		Content:     []byte(req.Content),
		Metadata:    req.Metadata,
	}, nil
}

// parseMultipartUpload reads a file part plus optional title/metadata fields.
// Form fields: "file" (required), "title", "metadata" (JSON object string).
func parseMultipartUpload(r *http.Request) (usecase.UploadInput, error) {
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		return usecase.UploadInput{}, fmt.Errorf("invalid multipart form: %v", err)
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		return usecase.UploadInput{}, fmt.Errorf("missing file part")
	}
	defer file.Close()

	content, err := io.ReadAll(io.LimitReader(file, maxUploadBytes))
	if err != nil {
		return usecase.UploadInput{}, fmt.Errorf("read file: %v", err)
	}

	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		title = header.Filename
	}
	contentType := header.Header.Get("Content-Type")

	var metadata map[string]string
	if raw := r.FormValue("metadata"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
			return usecase.UploadInput{}, fmt.Errorf("invalid metadata JSON: %v", err)
		}
	}

	return usecase.UploadInput{
		Title:       title,
		Filename:    header.Filename,
		ContentType: contentType,
		Content:     content,
		Metadata:    metadata,
	}, nil
}

func (h *DocumentHandler) list(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	docs, err := h.svc.List(r.Context(), limit, offset)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	out := make([]documentResponse, len(docs))
	for i := range docs {
		out[i] = toDocumentResponse(&docs[i])
	}
	writeJSON(w, http.StatusOK, map[string]any{"documents": out})
}

func (h *DocumentHandler) get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid document id")
		return
	}
	doc, err := h.svc.Get(r.Context(), id)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toDocumentResponse(doc))
}

func (h *DocumentHandler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid document id")
		return
	}
	if err := h.svc.Delete(r.Context(), id); err != nil {
		writeDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type documentSourceResponse struct {
	ID       uuid.UUID `json:"id"`
	Title    string    `json:"title"`
	Filename string    `json:"filename,omitempty"`
	FileURL  string    `json:"file_url,omitempty"`
	PageCount int      `json:"page_count"`
	Pages    []string  `json:"pages"`
}

// source returns the document's extracted text split into pages, plus a public
// file link. A UI uses this to render the source and highlight a chunk; the
// page index aligns with the `page` field returned by search references.
func (h *DocumentHandler) source(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid document id")
		return
	}
	src, err := h.svc.Source(r.Context(), id)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, documentSourceResponse{
		ID:        src.Document.ID,
		Title:     src.Document.Title,
		Filename:  src.Document.Filename,
		FileURL:   src.FileURL,
		PageCount: len(src.Pages),
		Pages:     src.Pages,
	})
}

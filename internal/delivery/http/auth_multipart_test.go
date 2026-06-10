package http

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"superkb/internal/domain"
	"superkb/internal/usecase"
)

func newAuthedDocServer(svc DocumentService, user, pass string) *httptest.Server {
	auth := BasicAuth(true, user, pass)
	return httptest.NewServer(NewRouter(NewDocumentHandler(svc), NewKnowledgeBaseHandler(&fakeKBService{}), auth))
}

func okDocService() *fakeDocService {
	now := time.Now().UTC()
	return &fakeDocService{
		uploadFn: func(_ context.Context, in usecase.UploadInput) (*domain.Document, error) {
			return &domain.Document{
				ID: uuid.New(), Title: in.Title, Filename: in.Filename,
				ContentType: in.ContentType, StorageKey: "documents/x",
				SizeBytes: int64(len(in.Content)), CreatedAt: now, UpdatedAt: now,
			}, nil
		},
		listFn: func(_ context.Context, _, _ int) ([]domain.Document, error) { return nil, nil },
	}
}

func TestBasicAuth_HealthzOpen(t *testing.T) {
	srv := newAuthedDocServer(okDocService(), "svc", "secret")
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz should be open, got %d", resp.StatusCode)
	}
}

func TestBasicAuth_MissingCredentials401(t *testing.T) {
	srv := newAuthedDocServer(okDocService(), "svc", "secret")
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/documents")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
	if resp.Header.Get("WWW-Authenticate") == "" {
		t.Error("expected WWW-Authenticate header")
	}
}

func TestBasicAuth_WrongCredentials401(t *testing.T) {
	srv := newAuthedDocServer(okDocService(), "svc", "secret")
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/documents", nil)
	req.SetBasicAuth("svc", "wrong")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestBasicAuth_CorrectCredentials200(t *testing.T) {
	srv := newAuthedDocServer(okDocService(), "svc", "secret")
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/documents", nil)
	req.SetBasicAuth("svc", "secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestMultipartUpload(t *testing.T) {
	var gotIn usecase.UploadInput
	svc := &fakeDocService{
		uploadFn: func(_ context.Context, in usecase.UploadInput) (*domain.Document, error) {
			gotIn = in
			now := time.Now().UTC()
			return &domain.Document{ID: uuid.New(), Title: in.Title, Filename: in.Filename, CreatedAt: now, UpdatedAt: now}, nil
		},
	}
	srv := newDocServer(svc)
	defer srv.Close()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "report.pdf")
	_, _ = fw.Write([]byte("%PDF-1.4 fake content"))
	_ = mw.WriteField("title", "Quarterly Report")
	_ = mw.WriteField("metadata", `{"source":"finance"}`)
	_ = mw.Close()

	resp, err := http.Post(srv.URL+"/api/v1/documents", mw.FormDataContentType(), &body)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	if gotIn.Title != "Quarterly Report" {
		t.Errorf("expected title from form field, got %q", gotIn.Title)
	}
	if gotIn.Filename != "report.pdf" {
		t.Errorf("expected filename report.pdf, got %q", gotIn.Filename)
	}
	if string(gotIn.Content) != "%PDF-1.4 fake content" {
		t.Errorf("unexpected content: %q", gotIn.Content)
	}
	if gotIn.Metadata["source"] != "finance" {
		t.Errorf("expected metadata source=finance, got %+v", gotIn.Metadata)
	}
}

func TestMultipartUpload_TitleDefaultsToFilename(t *testing.T) {
	var gotIn usecase.UploadInput
	svc := &fakeDocService{
		uploadFn: func(_ context.Context, in usecase.UploadInput) (*domain.Document, error) {
			gotIn = in
			now := time.Now().UTC()
			return &domain.Document{ID: uuid.New(), Title: in.Title, CreatedAt: now, UpdatedAt: now}, nil
		},
	}
	srv := newDocServer(svc)
	defer srv.Close()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "notes.txt")
	_, _ = fw.Write([]byte("hello"))
	_ = mw.Close()

	resp, err := http.Post(srv.URL+"/api/v1/documents", mw.FormDataContentType(), &body)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	if gotIn.Title != "notes.txt" {
		t.Errorf("expected title to default to filename, got %q", gotIn.Title)
	}
}

func TestMultipartUpload_MissingFile400(t *testing.T) {
	srv := newDocServer(okDocService())
	defer srv.Close()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("title", "no file here")
	_ = mw.Close()

	resp, err := http.Post(srv.URL+"/api/v1/documents", mw.FormDataContentType(), &body)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

// JSON upload still works alongside multipart.
func TestJSONUploadStillWorks(t *testing.T) {
	srv := newDocServer(okDocService())
	defer srv.Close()

	body, _ := json.Marshal(uploadRequest{Title: "Plain", Content: "text"})
	resp, err := http.Post(srv.URL+"/api/v1/documents", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
}

package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"superkb/internal/domain"
)

func newDocUC() (*DocumentUseCase, *fakeDocRepo, *fakeStorage) {
	repo := newFakeDocRepo()
	storage := newFakeStorage()
	return NewDocumentUseCase(repo, storage, nil, ""), repo, storage
}

func TestUpload_StoresContentAndMetadata(t *testing.T) {
	uc, repo, storage := newDocUC()

	doc, err := uc.Upload(context.Background(), UploadInput{
		Title:    "Notes",
		Filename: "notes.txt",
		Content:  []byte("hello world"),
	})
	if err != nil {
		t.Fatalf("Upload error: %v", err)
	}
	if doc.ID == uuid.Nil {
		t.Fatal("expected non-nil document ID")
	}
	if len(storage.objects) != 1 {
		t.Fatalf("expected 1 stored object, got %d", len(storage.objects))
	}
	if _, err := repo.GetByID(context.Background(), doc.ID); err != nil {
		t.Fatalf("metadata not persisted: %v", err)
	}
}

func TestUpload_RejectsEmptyTitle(t *testing.T) {
	uc, _, _ := newDocUC()
	_, err := uc.Upload(context.Background(), UploadInput{Content: []byte("x")})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput, got %v", err)
	}
}

func TestUpload_RejectsEmptyContent(t *testing.T) {
	uc, _, _ := newDocUC()
	_, err := uc.Upload(context.Background(), UploadInput{Title: "t"})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput, got %v", err)
	}
}

func TestDelete_RemovesContentAndMetadata(t *testing.T) {
	uc, repo, storage := newDocUC()
	doc, err := uc.Upload(context.Background(), UploadInput{Title: "t", Content: []byte("data")})
	if err != nil {
		t.Fatalf("setup upload failed: %v", err)
	}
	if err := uc.Delete(context.Background(), doc.ID); err != nil {
		t.Fatalf("Delete error: %v", err)
	}
	if len(storage.objects) != 0 {
		t.Errorf("expected storage emptied, got %d", len(storage.objects))
	}
	if _, err := repo.GetByID(context.Background(), doc.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

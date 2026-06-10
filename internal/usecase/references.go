package usecase

import (
	"strings"

	"superkb/internal/domain"
)

// fileURL builds a public, browsable link to a document's stored object by
// appending the storage key to the configured public base URL. Returns empty
// when no public base URL is configured.
func fileURL(publicBaseURL, storageKey string) string {
	if publicBaseURL == "" || storageKey == "" {
		return ""
	}
	return strings.TrimRight(publicBaseURL, "/") + "/" + strings.TrimLeft(storageKey, "/")
}

// findPage returns the 1-based page number whose text contains the chunk text,
// or 0 if not found. Matching is whitespace-insensitive on a normalized prefix
// of the chunk so layout differences between extractors do not defeat it.
func findPage(pages []string, chunkText string) int {
	needle := normalizeForMatch(chunkText)
	if needle == "" {
		return 0
	}
	// Use a prefix of the chunk for a robust contains check.
	if len(needle) > 80 {
		needle = needle[:80]
	}
	for i, p := range pages {
		if strings.Contains(normalizeForMatch(p), needle) {
			return i + 1
		}
	}
	return 0
}

// normalizeForMatch lowercases and collapses all whitespace runs to single
// spaces so text matching is robust across extractors.
func normalizeForMatch(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// enrichReference fills FileURL/Filename/Page on a search result from the
// resolved document and its extracted pages. pages may be nil (page stays 0).
func enrichReference(r *domain.SearchResult, doc *domain.Document, pages []string, publicBaseURL string) {
	r.FileURL = fileURL(publicBaseURL, doc.StorageKey)
	r.Filename = doc.Filename
	if r.Filename == "" {
		r.Filename = doc.Title
	}
	if len(pages) > 0 && r.ChunkText != "" {
		r.Page = findPage(pages, r.ChunkText)
	}
}

package extract

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// PDFExtractor extracts per-page text using the `pdftotext` CLI (poppler).
// For non-PDF content it returns the raw text as a single page.
type PDFExtractor struct {
	// binary is the pdftotext executable name/path (default "pdftotext").
	binary string
}

// NewPDFExtractor constructs a PDFExtractor.
func NewPDFExtractor() *PDFExtractor {
	return &PDFExtractor{binary: "pdftotext"}
}

// ExtractPages returns the document text split into pages. PDFs are split on
// the form-feed (\f) page delimiter emitted by pdftotext; other content types
// are returned as a single page of UTF-8 text.
func (e *PDFExtractor) ExtractPages(ctx context.Context, content []byte, contentType string) ([]string, error) {
	if !isPDF(contentType, content) {
		return []string{string(content)}, nil
	}

	// pdftotext - - : read PDF from stdin, write layout text to stdout.
	cmd := exec.CommandContext(ctx, e.binary, "-q", "-", "-")
	cmd.Stdin = bytes.NewReader(content)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("extract: pdftotext: %w: %s", err, strings.TrimSpace(errBuf.String()))
	}

	// pdftotext separates pages with a form-feed character.
	pages := strings.Split(out.String(), "\f")
	// Drop a trailing empty page that pdftotext often appends.
	if n := len(pages); n > 0 && strings.TrimSpace(pages[n-1]) == "" {
		pages = pages[:n-1]
	}
	if len(pages) == 0 {
		return []string{""}, nil
	}
	return pages, nil
}

func isPDF(contentType string, content []byte) bool {
	if strings.Contains(strings.ToLower(contentType), "pdf") {
		return true
	}
	return len(content) >= 5 && string(content[:5]) == "%PDF-"
}

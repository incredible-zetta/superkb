// Package ocr provides document-to-text converters used to OCR scanned PDFs
// and images before they are retained into the RAG index.
package ocr

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"superkb/internal/config"
	"superkb/internal/domain"
)

// defaultPrompt instructs the vision model to transcribe a page faithfully as
// markdown without commentary.
const defaultPrompt = "Extract ALL text from this document image as clean Markdown. " +
	"Preserve headings, tables, lists, and reading order. " +
	"Transcribe verbatim — do not summarize, translate, or add commentary. " +
	"Output only the extracted content."

// Vision is a domain.DocumentConverter backed by a vision LLM (e.g.
// MiniMax-VL-01) over an OpenAI-compatible chat-completions endpoint, typically
// 9router. It OCRs scanned PDFs and images that Hindsight's markitdown parser
// cannot (no tesseract in the image).
//
// PDFs are rasterized to PNG pages with pdftoppm (poppler) and sent one image
// per chat call; images are sent directly. Conversion runs in superkb before
// retain, so Hindsight receives already-extracted text.
type Vision struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
	model      string
	prompt     string
	maxPages   int
	pdftoppm   string
}

// NewVision constructs a vision-LLM OCR converter. Returns nil when no API key
// or model is configured, so callers can treat a nil converter as "OCR
// disabled".
func NewVision(cfg config.VisionOCRConfig) *Vision {
	if cfg.APIKey == "" || cfg.Model == "" {
		return nil
	}
	prompt := cfg.Prompt
	if prompt == "" {
		prompt = defaultPrompt
	}
	timeout := cfg.TimeoutSec
	if timeout <= 0 {
		timeout = 180
	}
	return &Vision{
		httpClient: &http.Client{Timeout: time.Duration(timeout) * time.Second},
		baseURL:    strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:     cfg.APIKey,
		model:      cfg.Model,
		prompt:     prompt,
		maxPages:   cfg.MaxPages,
		pdftoppm:   "pdftoppm",
	}
}

// Convert OCRs the document via the vision LLM and returns the concatenated
// per-page markdown. Returns ok=false for content types it does not handle
// (e.g. plain text), so the caller falls back to retaining raw bytes.
func (v *Vision) Convert(ctx context.Context, content []byte, contentType, filename string) (string, bool, error) {
	kind, supported := documentKind(contentType, filename)
	if !supported {
		return "", false, nil
	}

	var pages [][]byte // PNG bytes per page
	switch kind {
	case kindImage:
		pages = [][]byte{content}
	default: // kindPDF
		imgs, err := v.rasterizePDF(ctx, content)
		if err != nil {
			return "", false, err
		}
		pages = imgs
	}

	var sb strings.Builder
	for i, png := range pages {
		text, err := v.ocrImage(ctx, png)
		if err != nil {
			return "", false, fmt.Errorf("ocr page %d/%d: %w", i+1, len(pages), err)
		}
		if i > 0 {
			// Form-feed keeps page boundaries so downstream page resolution can
			// still split on \f like pdftotext output.
			sb.WriteString("\f")
		}
		sb.WriteString(text)
	}

	out := sb.String()
	if strings.TrimSpace(out) == "" {
		return "", false, fmt.Errorf("ocr: empty extraction for %q", filename)
	}
	return out, true, nil
}

// rasterizePDF converts a PDF to one PNG per page using pdftoppm. pdftoppm does
// not reliably stream PNG to stdout, so pages are written to a temp dir and
// read back. Bounded by maxPages when > 0.
func (v *Vision) rasterizePDF(ctx context.Context, pdf []byte) ([][]byte, error) {
	pageCount, err := v.pdfPageCount(ctx, pdf)
	if err != nil {
		return nil, err
	}
	if v.maxPages > 0 && pageCount > v.maxPages {
		pageCount = v.maxPages
	}

	tmpDir, err := os.MkdirTemp("", "superkb-ocr-")
	if err != nil {
		return nil, fmt.Errorf("ocr: temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	pdfPath := filepath.Join(tmpDir, "in.pdf")
	if err := os.WriteFile(pdfPath, pdf, 0o600); err != nil {
		return nil, fmt.Errorf("ocr: write temp pdf: %w", err)
	}

	imgs := make([][]byte, 0, pageCount)
	for p := 1; p <= pageCount; p++ {
		prefix := filepath.Join(tmpDir, fmt.Sprintf("page-%d", p))
		cmd := exec.CommandContext(ctx, v.pdftoppm, "-png", "-r", "150",
			"-f", fmt.Sprintf("%d", p), "-l", fmt.Sprintf("%d", p), "-singlefile", pdfPath, prefix)
		var errBuf bytes.Buffer
		cmd.Stderr = &errBuf
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("ocr: pdftoppm page %d: %w: %s", p, err, strings.TrimSpace(errBuf.String()))
		}
		png, err := os.ReadFile(prefix + ".png")
		if err != nil {
			return nil, fmt.Errorf("ocr: read rasterized page %d: %w", p, err)
		}
		imgs = append(imgs, png)
	}
	return imgs, nil
}

// pdfPageCount returns the number of pages in a PDF using pdfinfo.
func (v *Vision) pdfPageCount(ctx context.Context, pdf []byte) (int, error) {
	cmd := exec.CommandContext(ctx, "pdfinfo", "-")
	cmd.Stdin = bytes.NewReader(pdf)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("ocr: pdfinfo: %w: %s", err, strings.TrimSpace(errBuf.String()))
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(line, "Pages:") {
			var n int
			if _, err := fmt.Sscanf(strings.TrimSpace(strings.TrimPrefix(line, "Pages:")), "%d", &n); err == nil && n > 0 {
				return n, nil
			}
		}
	}
	return 0, fmt.Errorf("ocr: could not determine PDF page count")
}

// chat-completions request/response (OpenAI-compatible, multimodal content).
type chatContent struct {
	Type     string        `json:"type"`
	Text     string        `json:"text,omitempty"`
	ImageURL *chatImageURL `json:"image_url,omitempty"`
}

type chatImageURL struct {
	URL string `json:"url"`
}

type chatMessage struct {
	Role    string        `json:"role"`
	Content []chatContent `json:"content"`
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// ocrImage sends one PNG to the vision model and returns the transcribed text.
func (v *Vision) ocrImage(ctx context.Context, png []byte) (string, error) {
	dataURI := "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
	reqBody := chatRequest{
		Model:  v.model,
		Stream: false, // we decode a single JSON response, not an SSE stream
		Messages: []chatMessage{{
			Role: "user",
			Content: []chatContent{
				{Type: "text", Text: v.prompt},
				{Type: "image_url", ImageURL: &chatImageURL{URL: dataURI}},
			},
		}},
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal chat request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, v.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+v.apiKey)

	resp, err := v.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("POST /chat/completions: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("/chat/completions failed (status %d): %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}

	var out chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("vision model returned no choices")
	}
	return out.Choices[0].Message.Content, nil
}

type docKind int

const (
	kindUnsupported docKind = iota
	kindPDF
	kindImage
)

// documentKind decides whether vision OCR should handle the document, based on
// content type and filename extension. Plain text and office formats are left
// to the indexer's own conversion.
func documentKind(contentType, filename string) (docKind, bool) {
	ct := strings.ToLower(contentType)
	name := strings.ToLower(filename)

	if strings.Contains(ct, "pdf") || strings.HasSuffix(name, ".pdf") {
		return kindPDF, true
	}
	for _, ext := range []string{".png", ".jpg", ".jpeg", ".gif", ".bmp", ".tiff", ".tif", ".webp"} {
		if strings.HasSuffix(name, ext) {
			return kindImage, true
		}
	}
	if strings.HasPrefix(ct, "image/") {
		return kindImage, true
	}
	return kindUnsupported, false
}

var _ domain.DocumentConverter = (*Vision)(nil)

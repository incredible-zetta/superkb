package ocr

import (
	"testing"

	"superkb/internal/config"
)

func TestNewVision_DisabledWithoutConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  config.VisionOCRConfig
		want bool // want nil
	}{
		{"no key no model", config.VisionOCRConfig{}, true},
		{"key only", config.VisionOCRConfig{APIKey: "k"}, true},
		{"model only", config.VisionOCRConfig{Model: "m"}, true},
		{"key and model", config.VisionOCRConfig{APIKey: "k", Model: "m"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := NewVision(tc.cfg)
			if (got == nil) != tc.want {
				t.Fatalf("NewVision nil=%v, want nil=%v", got == nil, tc.want)
			}
		})
	}
}

func TestNewVision_Defaults(t *testing.T) {
	v := NewVision(config.VisionOCRConfig{APIKey: "k", Model: "minimax/MiniMax-VL-01"})
	if v == nil {
		t.Fatal("expected non-nil converter")
	}
	if v.prompt != defaultPrompt {
		t.Errorf("prompt = %q, want default", v.prompt)
	}
	if v.httpClient.Timeout == 0 {
		t.Error("expected non-zero timeout default")
	}
}

func TestDocumentKind(t *testing.T) {
	cases := []struct {
		ct, name string
		wantKind docKind
		wantSupp bool
	}{
		{"application/pdf", "a.pdf", kindPDF, true},
		{"", "report.PDF", kindPDF, true},
		{"image/png", "scan.png", kindImage, true},
		{"", "photo.jpeg", kindImage, true},
		{"image/webp", "x", kindImage, true},
		{"text/plain", "notes.txt", kindUnsupported, false},
		{"application/vnd.openxmlformats", "doc.docx", kindUnsupported, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k, supp := documentKind(tc.ct, tc.name)
			if k != tc.wantKind || supp != tc.wantSupp {
				t.Errorf("documentKind(%q,%q) = (%d,%v), want (%d,%v)", tc.ct, tc.name, k, supp, tc.wantKind, tc.wantSupp)
			}
		})
	}
}

func TestConvert_SkipsUnsupported(t *testing.T) {
	v := NewVision(config.VisionOCRConfig{APIKey: "k", Model: "m"})
	text, ok, err := v.Convert(nil, []byte("plain text"), "text/plain", "notes.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected ok=false for unsupported type")
	}
	if text != "" {
		t.Errorf("expected empty text, got %q", text)
	}
}

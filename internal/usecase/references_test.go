package usecase

import "testing"

func TestFileURL(t *testing.T) {
	cases := []struct {
		base, key, want string
	}{
		{"https://cdn.example.com", "documents/abc", "https://cdn.example.com/documents/abc"},
		{"https://cdn.example.com/", "/documents/abc", "https://cdn.example.com/documents/abc"},
		{"", "documents/abc", ""},
		{"https://cdn.example.com", "", ""},
	}
	for _, c := range cases {
		if got := fileURL(c.base, c.key); got != c.want {
			t.Errorf("fileURL(%q,%q)=%q want %q", c.base, c.key, got, c.want)
		}
	}
}

func TestFindPage(t *testing.T) {
	pages := []string{
		"Page one intro text about apples.",
		"Page two discusses the VACATION   policy in detail.",
		"Page three closing.",
	}
	// whitespace-insensitive, case-insensitive match on page 2
	if p := findPage(pages, "vacation policy"); p != 2 {
		t.Errorf("expected page 2, got %d", p)
	}
	// not present
	if p := findPage(pages, "nonexistent phrase here"); p != 0 {
		t.Errorf("expected 0 for missing text, got %d", p)
	}
	// empty chunk
	if p := findPage(pages, ""); p != 0 {
		t.Errorf("expected 0 for empty chunk, got %d", p)
	}
}

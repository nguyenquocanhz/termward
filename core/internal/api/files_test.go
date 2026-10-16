package api

import (
	"net/url"
	"strings"
	"testing"
)

// Directories come first, then names are ordered case-insensitively.
func TestSortEntries(t *testing.T) {
	entries := []fileEntry{
		{Name: "readme.md"},
		{Name: "Zebra", IsDir: true},
		{Name: "apple.txt"},
		{Name: "docs", IsDir: true},
		{Name: "Build.sh"},
	}
	sortEntries(entries)
	got := make([]string, len(entries))
	for i, e := range entries {
		got[i] = e.Name
	}
	want := []string{"docs", "Zebra", "apple.txt", "Build.sh", "readme.md"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestContentDisposition(t *testing.T) {
	// ASCII name passes through unchanged.
	if got, want := contentDisposition("report.pdf"),
		`attachment; filename="report.pdf"; filename*=UTF-8''report.pdf`; got != want {
		t.Errorf("ascii: got %s want %s", got, want)
	}
	// Quotes and backslashes are sanitised in the fallback and escaped in filename*.
	if got, want := contentDisposition(`a"b\c.txt`),
		`attachment; filename="a_b_c.txt"; filename*=UTF-8''a%22b%5Cc.txt`; got != want {
		t.Errorf("sanitise: got %s want %s", got, want)
	}
	// Non-ASCII (e.g. Vietnamese): ASCII fallback stays ASCII; filename* is the RFC 5987 escape.
	name := "báo cáo.txt"
	got := contentDisposition(name)
	if !strings.Contains(got, "filename*=UTF-8''"+url.PathEscape(name)) {
		t.Errorf("missing/incorrect RFC 5987 part: %s", got)
	}
	ascii, ok := strings.CutPrefix(got, `attachment; filename="`)
	if !ok {
		t.Fatalf("missing ascii filename: %s", got)
	}
	ascii, _, _ = strings.Cut(ascii, `"`)
	for _, c := range ascii {
		if c > 126 || c < 32 {
			t.Errorf("fallback not printable ASCII: %q in %q", c, ascii)
		}
	}
}

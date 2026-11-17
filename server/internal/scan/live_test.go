package scan

import (
	"context"
	"os"
	"strings"
	"testing"
)

// SCAN_LIVE_URL_FILE=<file holding a share link> fetches a real scan. The link carries
// personal tokens, so it is read from a file and never logged.
func TestLiveFetch(t *testing.T) {
	path := os.Getenv("SCAN_LIVE_URL_FILE")
	if path == "" {
		t.Skip("SCAN_LIVE_URL_FILE not set")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	w, err := Fetch(ctx, strings.TrimSpace(string(b)), nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if d := debugDir(); d != "" {
		dir = d + "/live"
	}
	files, err := Download(ctx, w, dir, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	mb, err := os.ReadFile(files.Model)
	if err != nil {
		t.Fatal(err)
	}
	m, err := Decode(mb)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d observers, %d window labels, %d chunks, %d triangles, layout %v", len(w.Observers), len(w.WindowLabels), len(m.Chunks), m.Triangles(), files.Layout != "")
}

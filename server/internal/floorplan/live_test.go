package floorplan

// Reads a real plan with the configured model, run on demand:
//
//	FLOORPLAN_LIVE=plan.jpg FLOORPLAN_OUT=reading.json go test ./internal/floorplan -run Live -v
//
// The answer is written to FLOORPLAN_OUT, which is how testdata/*.reading.json were recorded.

import (
	"context"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"testing"
	"time"
)

func TestLiveRead(t *testing.T) {
	path := os.Getenv("FLOORPLAN_LIVE")
	if path == "" {
		t.Skip("FLOORPLAN_LIVE not set")
	}
	llm, err := ModelFromEnv()
	if err != nil || llm == nil {
		t.Skip("no model configured")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	img = crop(img, Drawing(img))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	start := time.Now()
	r, raw, err := Read(ctx, llm, img)
	if out := os.Getenv("FLOORPLAN_OUT"); out != "" && raw != "" {
		os.WriteFile(out, []byte(raw), 0o644)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s: %d rooms, %d walls, %d doors, %d windows", time.Since(start).Round(time.Second), len(r.Rooms), len(r.Walls), len(r.Doors), len(r.Windows))
}

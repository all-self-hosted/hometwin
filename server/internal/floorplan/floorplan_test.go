package floorplan

import (
	"context"
	"image"
	"image/color"
	"testing"

	"github.com/liliang-cn/agent-go/v3/pkg/domain"

	"hometwin/internal/home"
)

func TestParseReading(t *testing.T) {
	r, err := ParseReading("```json\n{\"rooms\": [{\"name\": \"卧室\", \"kind\": \"bedroom\", \"area_m2\": 10, \"poly_px\": [[0,0],[1,0],[1,1]]}]}\n```")
	if err != nil || len(r.Rooms) != 1 || r.Rooms[0].Area != 10 {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := ParseReading("我看不清"); err == nil {
		t.Fatal("not JSON should fail")
	}
}

// Drawing cuts a phone screenshot down to the plan: dark bars above and below a white panel.
func TestDrawing(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 300, 600))
	for y := range 600 {
		for x := range 300 {
			c := color.RGBA{30, 30, 30, 255}
			if y >= 150 && y < 450 {
				c = color.RGBA{255, 255, 255, 255}
				if x > 100 && x < 200 && y > 250 && y < 350 {
					c = color.RGBA{240, 200, 120, 255} // a room's fill
				}
			}
			img.Set(x, y, c)
		}
	}
	if r := Drawing(img); r != image.Rect(0, 150, 300, 450) {
		t.Fatalf("got %v", r)
	}
	white := image.NewRGBA(image.Rect(0, 0, 50, 50))
	for i := range white.Pix {
		white.Pix[i] = 255
	}
	if r := Drawing(white); r != white.Bounds() {
		t.Fatalf("a plain picture is cut to %v", r)
	}
}

// fakeVision answers every question about a crop the same way.
type fakeVision struct {
	domain.Generator
	asked []string
}

func (f *fakeVision) GenerateWithTools(_ context.Context, msgs []domain.Message, _ []domain.ToolDefinition, _ *domain.GenerationOptions) (*domain.GenerationResult, error) {
	for _, p := range msgs[0].Parts {
		if p.Type == domain.MessagePartTypeText {
			f.asked = append(f.asked, p.Text)
		}
	}
	return &domain.GenerationResult{Content: "门开在它南边的墙上，通向客厅。"}, nil
}

// Without a model nothing is imported, and the page is told why.
func TestNoModel(t *testing.T) {
	im, _ := NewImporter(t.TempDir(), nil, home.Sample, func(home.Home) error { return nil }, nil)
	if _, err := im.Start([]byte("x"), "image/png"); err != ErrNoModel {
		t.Fatalf("got %v", err)
	}
}

package floorplan

// Draws a draft over its picture, on demand:
//
//	FLOORPLAN_DEBUG=out.png go test ./internal/floorplan -run DebugRender
//
// It draws the made-up plan of fictional_test.go; FLOORPLAN_IMAGE and FLOORPLAN_READING draw
// another picture with its recorded reading.

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"testing"

	"hometwin/internal/home"
)

// TestDebugRender draws a draft over its picture into FLOORPLAN_DEBUG (a PNG path).
func TestDebugRender(t *testing.T) {
	out := os.Getenv("FLOORPLAN_DEBUG")
	if out == "" {
		t.Skip("FLOORPLAN_DEBUG not set")
	}
	rd, img := fictionalReading(), image.Image(fictionalImage())
	if path := os.Getenv("FLOORPLAN_IMAGE"); path != "" {
		// a picture from elsewhere with its recorded reading
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		img, _, err = image.Decode(f)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		img = crop(img, Drawing(img))
		raw, _ := os.ReadFile(os.Getenv("FLOORPLAN_READING"))
		if rd, err = ParseReading(string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	p, err := Build(rd, img)
	if err != nil {
		t.Fatal(err)
	}
	d := p.Draft("t", "t")
	b := img.Bounds()
	c := image.NewRGBA(b)
	draw.Draw(c, b, img, b.Min, draw.Src)
	px := func(v home.P) (float64, float64) {
		q := p.px(home.P{v[0] * 1000, v[1] * 1000})
		return q[0], q[1]
	}
	line := func(a, bb home.P, w float64, col color.RGBA) {
		x0, y0 := px(a)
		x1, y1 := px(bb)
		n := int(math.Hypot(x1-x0, y1-y0)) + 1
		for i := 0; i <= n; i++ {
			t := float64(i) / float64(n)
			x, y := x0+(x1-x0)*t, y0+(y1-y0)*t
			for dx := -w; dx <= w; dx++ {
				for dy := -w; dy <= w; dy++ {
					c.Set(int(x+dx), int(y+dy), col)
				}
			}
		}
	}
	for _, r := range d.Home.Rooms {
		for i := range r.Poly {
			line(r.Poly[i], r.Poly[(i+1)%len(r.Poly)], 0, color.RGBA{30, 90, 255, 255})
		}
	}
	for _, w := range d.Home.Walls {
		line(w.A, w.B, 1, color.RGBA{230, 30, 30, 255})
	}
	for _, dr := range d.Home.Doors {
		w, _ := d.Home.Wall(dr.Wall)
		line(w.At(dr.At-dr.Width/2), w.At(dr.At+dr.Width/2), 3, color.RGBA{0, 180, 80, 255})
	}
	for _, wn := range d.Home.Windows {
		w, _ := d.Home.Wall(wn.Wall)
		line(w.At(wn.At-wn.Width/2), w.At(wn.At+wn.Width/2), 2, color.RGBA{255, 150, 0, 255})
	}
	f, _ := os.Create(out)
	defer f.Close()
	png.Encode(f, c)
	for _, a := range d.Areas {
		t.Logf("%s 图上 %.1f 算出 %.2f (%+.1f%%)", a.Name, a.Printed, a.Computed, a.Diff*100)
	}
	for _, is := range d.Issues {
		t.Logf("issue %s", is.Text)
	}
}

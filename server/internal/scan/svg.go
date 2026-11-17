package scan

import (
	"bytes"
	"fmt"
	"math"

	"hometwin/internal/home"
)

// SVG draws a plan result from above, north up: rooms with their names and areas, walls,
// doors (red), windows (blue) and the floor levels (platform orange, steps yellow).
func SVG(r Result) []byte {
	h := r.Home
	x0, z0, x1, z1 := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	grow := func(p home.P) {
		x0, x1 = math.Min(x0, p[0]), math.Max(x1, p[0])
		z0, z1 = math.Min(z0, p[1]), math.Max(z1, p[1])
	}
	for _, w := range h.Walls {
		grow(w.A)
		grow(w.B)
	}
	for _, rm := range h.Rooms {
		for _, p := range rm.Poly {
			grow(p)
		}
	}
	const px = 60.0
	pad := 0.5
	X := func(x float64) float64 { return (x - x0 + pad) * px }
	Z := func(z float64) float64 { return (z - z0 + pad) * px }
	pts := func(p []home.P) string {
		var b bytes.Buffer
		for _, q := range p {
			fmt.Fprintf(&b, "%.1f,%.1f ", X(q[0]), Z(q[1]))
		}
		return b.String()
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%.0f" height="%.0f" font-family="sans-serif">`+"\n", (x1-x0+2*pad)*px, (z1-z0+2*pad)*px)
	fmt.Fprintf(&b, `<rect width="100%%" height="100%%" fill="#fff"/>`+"\n")
	fill := map[string]string{"living": "#f3ead8", "bedroom": "#e9d9bd", "kitchen": "#e4e9ee", "bath": "#dfeaf0", "balcony": "#e8f0e0", "entry": "#eee"}
	for _, rm := range h.Rooms {
		fmt.Fprintf(&b, `<polygon points="%s" fill="%s" stroke="#bbb" stroke-width="1"/>`+"\n", pts(rm.Poly), fill[rm.Kind])
	}
	for _, l := range r.Levels {
		c := "#f0a040"
		if l.Kind == "step" {
			c = "#f5d742"
		}
		fmt.Fprintf(&b, `<polygon points="%s" fill="%s" fill-opacity="0.7" stroke="%s"/>`+"\n", pts(l.Poly), c, c)
	}
	for _, w := range h.Walls {
		col := "#555"
		if w.Exterior {
			col = "#222"
		}
		fmt.Fprintf(&b, `<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="%s" stroke-width="%.1f"/>`+"\n", X(w.A[0]), Z(w.A[1]), X(w.B[0]), Z(w.B[1]), col, w.Thick*px)
	}
	span := func(wid string, at, width float64, col string) {
		w, ok := h.Wall(wid)
		if !ok {
			return
		}
		a, c := w.At(at-width/2), w.At(at+width/2)
		fmt.Fprintf(&b, `<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="%s" stroke-width="%.1f"/>`+"\n", X(a[0]), Z(a[1]), X(c[0]), Z(c[1]), col, w.Thick*px+2)
	}
	for _, d := range h.Doors {
		span(d.Wall, d.At, d.Width, "#e04040")
	}
	for _, wn := range h.Windows {
		span(wn.Wall, wn.At, wn.Width, "#4080e0")
	}
	for _, rm := range h.Rooms {
		cx, cz := centroid(rm.Poly)
		fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" font-size="15" text-anchor="middle">%s</text>`+"\n", X(cx), Z(cz), rm.Name)
		fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" font-size="12" text-anchor="middle" fill="#555">%.1f m²</text>`+"\n", X(cx), Z(cz)+16, home.Area(rm.Poly))
	}
	b.WriteString("</svg>\n")
	return b.Bytes()
}

func centroid(p []home.P) (float64, float64) {
	var a, cx, cz float64
	for i := range p {
		q, r := p[i], p[(i+1)%len(p)]
		c := q[0]*r[1] - r[0]*q[1]
		a += c
		cx += (q[0] + r[0]) * c
		cz += (q[1] + r[1]) * c
	}
	if a == 0 {
		return p[0][0], p[0][1]
	}
	return cx / (3 * a), cz / (3 * a)
}

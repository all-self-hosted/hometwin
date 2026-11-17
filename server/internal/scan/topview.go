package scan

import (
	"image"
	"image/color"
	"math"
	"sort"
)

// View is a picture of the scan from straight above, north up: pixel (0, 0) is at X0, Z0
// in the home's frame, Cell metres a pixel.
type View struct {
	Img    *image.RGBA
	X0, Z0 float64
	Cell   float64
}

// TopView paints the scan from above, cut at cut metres like the 3D view: every triangle
// lower than the cut in its texture's colour, higher ones over lower.
func TopView(m Mesh, f Frame, textures []image.Image, cut, cell float64) View {
	ts := worldTris(m, f)
	g := newGrid(ts, cell, 0.2)
	var keep []tri
	for _, t := range ts {
		if t.centre()[1] < cut && t.n[1] > 0.3 {
			keep = append(keep, t)
		}
	}
	sort.Slice(keep, func(i, j int) bool { return keep[i].centre()[1] < keep[j].centre()[1] })
	img := image.NewRGBA(image.Rect(0, 0, g.nx, g.nz))
	for k := range g.n() {
		img.Set(k%g.nx, k/g.nx, color.RGBA{246, 245, 242, 255})
	}
	for _, t := range keep {
		var im image.Image
		if t.chunk < len(textures) {
			im = textures[t.chunk]
		}
		a, b, c := t.p[0], t.p[1], t.p[2]
		den := (b[2]-c[2])*(a[0]-c[0]) + (c[0]-b[0])*(a[2]-c[2])
		eachCell(g, t, func(k int) {
			if im == nil || math.Abs(den) < 1e-12 {
				img.SetRGBA(k%g.nx, k/g.nx, color.RGBA{200, 200, 200, 255})
				return
			}
			// The texture at the cell's centre, by its barycentric place in the triangle.
			p := g.centre(k)
			l0 := ((b[2]-c[2])*(p[0]-c[0]) + (c[0]-b[0])*(p[1]-c[2])) / den
			l1 := ((c[2]-a[2])*(p[0]-c[0]) + (a[0]-c[0])*(p[1]-c[2])) / den
			l2 := 1 - l0 - l1
			u := l0*float64(t.uvs[0][0]) + l1*float64(t.uvs[1][0]) + l2*float64(t.uvs[2][0])
			v := l0*float64(t.uvs[0][1]) + l1*float64(t.uvs[1][1]) + l2*float64(t.uvs[2][1])
			bd := im.Bounds()
			x := bd.Min.X + int(math.Max(0, math.Min(0.999, u))*float64(bd.Dx()))
			y := bd.Min.Y + int(math.Max(0, math.Min(0.999, 1-v))*float64(bd.Dy()))
			r, gg, bb, _ := im.At(x, y).RGBA()
			img.SetRGBA(k%g.nx, k/g.nx, color.RGBA{uint8(r >> 8), uint8(gg >> 8), uint8(bb >> 8), 255})
		})
	}
	return View{Img: img, X0: g.x0, Z0: g.z0, Cell: cell}
}

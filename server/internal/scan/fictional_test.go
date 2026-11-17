package scan

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"sort"
	"testing"

	"hometwin/internal/home"
)

// A made-up flat, built here as a scan would see it, so the package is covered without the
// real home's files (testdata/private). In its own plan (metres, x east, y north, the floor
// at 0, inner faces of the walls):
//
//	y 7.5 ┌──────────────┬─────────┐
//	      │ 卧室 (wood)   │ 卫生间   │   window on the north wall of 卧室
//	 4.12 ├───┤door├─────┼─┤door├──┤   interior walls 0.12 m thick
//	 4.0  │                        │
//	 door │ 客厅 (tile)    ┌step┬platform 0.26 m (wood), step 0.13 m
//	      │               │    │   │
//	y 0   └──┤ window ├────┴────┴───┘   window on the south wall
//	     x 0                    3.0 3.3 5
//
// The front door is on the west wall of 客厅. The ceiling is 2.7 m everywhere. The mesh
// is only what a scanner inside sees: inner faces, jambs and heads of the doors, nothing
// behind the outer walls. It is then turned and moved off the axes, as scans are.
const (
	fakeCeiling           = 2.7
	fakeAngle             = 0.1 // radians the scan's walls are turned off its X axis
	fakeX, fakeY, fakeFlr = 3.7, -2.2, -1.4
)

// Inner areas of the fictional rooms, m².
var fakeAreas = map[string]float64{"客厅": 5 * 4, "卧室": 3 * 3.38, "卫生间": 1.88 * 3.38}

// The chunks, one per texture.
const (
	fakeWall = iota // walls and ceilings
	fakeTile
	fakeWood
	fakeChunks
)

type flatBuilder struct{ chunks [fakeChunks]Chunk }

// toScan turns a point of the fictional plan into the scan's frame.
func toScan(x, y, z float64) [3]float64 {
	c, s := math.Cos(fakeAngle), math.Sin(fakeAngle)
	return [3]float64{x*c - y*s + fakeX, x*s + y*c + fakeY, z + fakeFlr}
}

// panel adds the rectangle o + u·U + v·V (0 ≤ u ≤ lu, 0 ≤ v ≤ lv) less its holes (u0, u1,
// v0, v1), in quads of at most 0.25 m, wound so the faces look along want.
func (b *flatBuilder) panel(chunk int, o, U, V [3]float64, lu, lv float64, want [3]float64, holes ...[4]float64) {
	breaks := func(l float64, edges ...float64) []float64 {
		v := []float64{0, l}
		for x := 0.25; x < l; x += 0.25 {
			v = append(v, x)
		}
		for _, e := range edges {
			if e > 0 && e < l {
				v = append(v, e)
			}
		}
		sort.Float64s(v)
		out := v[:1]
		for _, x := range v[1:] {
			if x-out[len(out)-1] > 1e-6 {
				out = append(out, x)
			}
		}
		return out
	}
	var ue, ve []float64
	for _, h := range holes {
		ue, ve = append(ue, h[0], h[1]), append(ve, h[2], h[3])
	}
	us, vs := breaks(lu, ue...), breaks(lv, ve...)
	flip := dot(cross(U, V), want) < 0
	c := &b.chunks[chunk]
	at := func(u, v float64) uint32 {
		p := toScan(o[0]+u*U[0]+v*V[0], o[1]+u*U[1]+v*V[1], o[2]+u*U[2]+v*V[2])
		c.Pos = append(c.Pos, float32(p[0]), float32(p[1]), float32(p[2]))
		c.UV = append(c.UV, float32(0.1+0.8*u/lu), float32(0.1+0.8*v/lv))
		return uint32(len(c.Pos)/3 - 1)
	}
	for i := 0; i+1 < len(us); i++ {
		for j := 0; j+1 < len(vs); j++ {
			uc, vc := (us[i]+us[i+1])/2, (vs[j]+vs[j+1])/2
			cut := false
			for _, h := range holes {
				cut = cut || (uc > h[0] && uc < h[1] && vc > h[2] && vc < h[3])
			}
			if cut {
				continue
			}
			a, bb, cc, d := at(us[i], vs[j]), at(us[i+1], vs[j]), at(us[i+1], vs[j+1]), at(us[i], vs[j+1])
			if flip {
				c.Index = append(c.Index, a, d, cc, a, cc, bb)
			} else {
				c.Index = append(c.Index, a, bb, cc, a, cc, d)
			}
		}
	}
}

func dot(a, b [3]float64) float64 { return a[0]*b[0] + a[1]*b[1] + a[2]*b[2] }

var (
	ex, ey, ez = [3]float64{1, 0, 0}, [3]float64{0, 1, 0}, [3]float64{0, 0, 1}
	nx, ny, nz = [3]float64{-1, 0, 0}, [3]float64{0, -1, 0}, [3]float64{0, 0, -1}
)

// floor adds a horizontal rectangle at height z facing up (or down for a ceiling).
func (b *flatBuilder) floor(chunk int, x0, y0, x1, y1, z float64, up bool, holes ...[4]float64) {
	want := ez
	if !up {
		want = nz
	}
	for i := range holes {
		holes[i] = [4]float64{holes[i][0] - x0, holes[i][1] - x0, holes[i][2] - y0, holes[i][3] - y0}
	}
	b.panel(chunk, [3]float64{x0, y0, z}, ex, ey, x1-x0, y1-y0, want, holes...)
}

// wallX adds a wall face in the plane x, from y0 to y1 and z0 to z1, facing want; holes are
// (y0, y1, z0, z1).
func (b *flatBuilder) wallX(x, y0, y1, z0, z1 float64, want [3]float64, holes ...[4]float64) {
	for i := range holes {
		holes[i] = [4]float64{holes[i][0] - y0, holes[i][1] - y0, holes[i][2] - z0, holes[i][3] - z0}
	}
	b.panel(fakeWall, [3]float64{x, y0, z0}, ey, ez, y1-y0, z1-z0, want, holes...)
}

// wallY is wallX in the plane y; holes are (x0, x1, z0, z1).
func (b *flatBuilder) wallY(y, x0, x1, z0, z1 float64, want [3]float64, holes ...[4]float64) {
	for i := range holes {
		holes[i] = [4]float64{holes[i][0] - x0, holes[i][1] - x0, holes[i][2] - z0, holes[i][3] - z0}
	}
	b.panel(fakeWall, [3]float64{x0, y, z0}, ex, ez, x1-x0, z1-z0, want, holes...)
}

// fakeFlat is the fictional flat's scan.
func fakeFlat() Mesh {
	var b flatBuilder
	const H = fakeCeiling
	// Outer walls, inner faces only.
	b.wallY(0, 0, 5, 0, H, ey, [4]float64{1.0, 2.6, 0.9, 2.1})   // south, the living room's window
	b.wallY(7.5, 0, 5, 0, H, ny, [4]float64{0.9, 2.1, 0.9, 2.1}) // north, the bedroom's window
	b.wallX(0, 0, 7.5, 0, H, ex, [4]float64{2.6, 3.5, 0, 2.05})  // west, the front door
	b.wallX(5, 0, 7.5, 0, H, nx)                                 // east
	// The front door's head, 0.2 m deep into the outer wall. (Its jambs would stick out
	// past the outer walls' faces and move Orient's origin.)
	b.floor(fakeWall, -0.2, 2.6, 0, 3.5, 2.05, false)
	// The wall between 客厅 and the rooms north of it, with two doors.
	doorA, doorB := [4]float64{0.8, 1.7, 0, 2.0}, [4]float64{3.7, 4.45, 0, 2.0}
	b.wallY(4.0, 0, 5, 0, H, ny, doorA, doorB)
	b.wallY(4.12, 0, 3, 0, H, ey, doorA)
	b.wallY(4.12, 3.12, 5, 0, H, ey, doorB)
	for _, d := range [][4]float64{doorA, doorB} {
		b.wallX(d[0], 4.0, 4.12, 0, d[3], ex)
		b.wallX(d[1], 4.0, 4.12, 0, d[3], nx)
		b.floor(fakeWall, d[0], 4.0, d[1], 4.12, d[3], false)
		b.floor(fakeTile, d[0], 4.0, d[1], 4.12, 0, true) // the threshold
	}
	// Between 卧室 and 卫生间.
	b.wallX(3.0, 4.12, 7.5, 0, H, nx)
	b.wallX(3.12, 4.12, 7.5, 0, H, ex)
	// Floors and ceilings.
	plat, step := [4]float64{3.3, 5, 0, 1.8}, [4]float64{3.0, 3.3, 0.3, 1.5}
	b.floor(fakeTile, 0, 0, 5, 4, 0, true, plat, step)
	b.floor(fakeWood, 0, 4.12, 3, 7.5, 0, true)
	b.floor(fakeTile, 3.12, 4.12, 5, 7.5, 0, true)
	for _, r := range [][4]float64{{0, 0, 5, 4}, {0, 4.12, 3, 7.5}, {3.12, 4.12, 5, 7.5}} {
		b.floor(fakeWall, r[0], r[1], r[2], r[3], H, false)
	}
	// The platform in 客厅 and the step up to it, with their sides.
	b.floor(fakeWood, plat[0], plat[2], plat[1], plat[3], 0.26, true)
	b.wallX(plat[0], plat[2], plat[3], 0, 0.26, nx)
	b.wallY(plat[3], plat[0], plat[1], 0, 0.26, ey)
	b.floor(fakeWood, step[0], step[2], step[1], step[3], 0.13, true)
	b.wallX(step[0], step[2], step[3], 0, 0.13, nx)
	b.wallY(step[2], step[0], step[1], 0, 0.13, ny)
	b.wallY(step[3], step[0], step[1], 0, 0.13, ey)

	var m Mesh
	for i, c := range b.chunks {
		c.Name = fmt.Sprintf("chunk_%d", i)
		c.Texture = fmt.Sprintf("texture_%d.jpg", i)
		m.Chunks = append(m.Chunks, c)
	}
	return m
}

// fakeTextures are the chunks' textures as JPEG: white walls, pale grey tiles with grout,
// brown boards.
func fakeTextures(t testing.TB) [][]byte {
	t.Helper()
	paint := func(at func(x, y int) color.RGBA) []byte {
		im := image.NewRGBA(image.Rect(0, 0, 64, 64))
		for y := range 64 {
			for x := range 64 {
				im.SetRGBA(x, y, at(x, y))
			}
		}
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, im, &jpeg.Options{Quality: 90}); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	return [][]byte{
		paint(func(x, y int) color.RGBA { return color.RGBA{236, 234, 229, 255} }),
		paint(func(x, y int) color.RGBA {
			if x%16 == 0 || y%16 == 0 {
				return color.RGBA{176, 175, 172, 255}
			}
			return color.RGBA{216, 214, 208, 255}
		}),
		paint(func(x, y int) color.RGBA {
			if y%8 == 0 {
				return color.RGBA{120, 78, 44, 255}
			}
			return color.RGBA{156, 104, 62, 255}
		}),
	}
}

func fakeImages(t testing.TB) []image.Image {
	var out []image.Image
	for _, b := range fakeTextures(t) {
		im, err := jpeg.Decode(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, im)
	}
	return out
}

// fakeWork is a share page's description of the fictional scan: where its panoramas were
// taken and their rooms, and where its window labels are. Every address and code is made up.
func fakeWork() (Work, []LayoutRoom) {
	var w Work
	w.Code = "demo-0000-fictional"
	w.Layout = `{"bedroom_amount":1,"parlor_amount":1,"toilet_amount":1}`
	w.Model.FileURL = "https://cdn.example.invalid/demo/model/flat.at3d"
	for i := range fakeChunks {
		w.Model.Textures = append(w.Model.Textures, fmt.Sprintf("https://cdn.example.invalid/demo/materials/texture_%d.jpg", i))
	}
	var layout []LayoutRoom
	for i, p := range []struct {
		name string
		x, y float64
	}{{"客厅", 1.5, 2.0}, {"客厅", 2.5, 3.0}, {"卧室", 1.5, 5.8}, {"卫生间", 4.0, 5.8}} {
		s := toScan(p.x, p.y, 0)
		// The viewer's frame: x, z up, −y.
		w.Observers = append(w.Observers, Observer{Index: i, Position: [3]float64{s[0], s[2] + 1.5, -s[1]}, Standing: [3]float64{s[0], s[2], -s[1]}})
		layout = append(layout, LayoutRoom{Pano: i, Name: p.name})
	}
	w.Panorama.Count = len(w.Observers)
	// Window labels: boxes on the two windows, corners as (x, z up, y).
	for i, c := range [][2]float64{{1.8, 0}, {1.5, 7.5}} {
		var box [][3]float64
		for _, d := range [][2]float64{{-0.1, 1.4}, {0.1, 1.4}, {0.1, 1.6}, {-0.1, 1.6}} {
			s := toScan(c[0]+d[0], c[1], d[1])
			box = append(box, [3]float64{s[0], s[2], s[1]})
		}
		w.WindowLabels = append(w.WindowLabels, WindowLabel{Pano: i, Box: box})
	}
	return w, layout
}

func fakeLayoutJSON(t testing.TB) []byte {
	_, l := fakeWork()
	b, err := json.Marshal(l)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDecodeFictional(t *testing.T) {
	m := fakeFlat()
	got, err := Decode(encodeAt3d(m))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Chunks) != len(m.Chunks) {
		t.Fatalf("%d chunks, want %d", len(got.Chunks), len(m.Chunks))
	}
	for i, c := range got.Chunks {
		w := m.Chunks[i]
		if c.Name != w.Name || c.Texture != w.Texture || fmt.Sprint(c.Pos) != fmt.Sprint(w.Pos) || fmt.Sprint(c.UV) != fmt.Sprint(w.UV) || fmt.Sprint(c.Index) != fmt.Sprint(w.Index) {
			t.Errorf("chunk %d (%q) came back different", i, c.Name)
		}
	}
	if got.Vertices() != m.Vertices() || got.Triangles() != m.Triangles() {
		t.Errorf("%d vertices %d triangles, want %d %d", got.Vertices(), got.Triangles(), m.Vertices(), m.Triangles())
	}
}

func TestDecodeFictionalRejectsJunk(t *testing.T) {
	b := encodeAt3d(fakeFlat())
	flip := append([]byte{}, b...)
	flip[len(flip)/2] ^= 0x55
	for _, bad := range [][]byte{nil, b[:40], b[:len(b)-200], append(append([]byte{}, b[:len(b)/2]...), make([]byte, 64)...)} {
		if _, err := Decode(bad); err == nil {
			t.Errorf("Decode of %d bytes: no error", len(bad))
		}
	}
	c := append([]byte{}, b...)
	c[0] = 2
	if _, err := Decode(c); err == nil {
		t.Error("version 2 accepted")
	}
	// A stream that decodes fine but is no protobuf, and one with an index past the vertices.
	if _, err := Decode(append([]byte{1, 0, 0, 0, 0, 0, 0, 0}, lzmaLiterals([]byte{0xFF, 0xFF, 0xFF})...)); err == nil {
		t.Error("junk protobuf accepted")
	}
	m := fakeFlat()
	m.Chunks[0].Index[0] = uint32(m.Vertices())
	if _, err := Decode(encodeAt3d(m)); err == nil {
		t.Error("an index out of range accepted")
	}
}

// The test encoder and the decoder agree on edge cases of the range coder: nothing, one
// byte, runs that drive the probabilities to their ends, and every byte value.
func TestLZMARoundTrip(t *testing.T) {
	all := make([]byte, 256*4)
	for i := range all {
		all[i] = byte(i * 7)
	}
	for _, in := range [][]byte{{}, {0}, {0xFF}, bytes.Repeat([]byte{0}, 5000), bytes.Repeat([]byte{0xFF}, 5000), all} {
		out, err := unLZMA(lzmaLiterals(in), MaxModel)
		if err != nil || !bytes.Equal(out, in) {
			t.Errorf("%d bytes: got %d, %v", len(in), len(out), err)
		}
	}
	if _, err := unLZMA(lzmaLiterals(make([]byte, 100)), 50); err == nil {
		t.Error("the limit let 100 bytes through")
	}
}

func fakeFrame(t testing.TB) (Mesh, Frame) {
	t.Helper()
	m := fakeFlat()
	f, err := Orient(m, 0)
	if err != nil {
		t.Fatal(err)
	}
	return m, f
}

func TestOrientFictional(t *testing.T) {
	_, f := fakeFrame(t)
	if math.Abs(f.Angle-fakeAngle) > 0.005 || math.Abs(f.Floor-fakeFlr) > 0.01 || math.Abs(f.Ceiling-fakeCeiling) > 0.01 {
		t.Errorf("frame %+v", f)
	}
	// The flat's south-west inner corner lands half an outer wall in from the origin.
	p := f.Point(toScan(0, 0, 0)[0], toScan(0, 0, 0)[1], toScan(0, 0, 0)[2])
	if math.Abs(p[0]-ExteriorThick/2) > 0.03 || math.Abs(p[1]) > 0.01 {
		t.Errorf("south-west corner at %v", p)
	}
	if q := f.Point(toScan(0, 7.5, 0)[0], toScan(0, 7.5, 0)[1], 0); math.Abs(q[2]-ExteriorThick/2) > 0.03 {
		t.Errorf("north-west corner at %v", q)
	}
}

func planFake(t testing.TB) Result {
	t.Helper()
	m, f := fakeFrame(t)
	w, l := fakeWork()
	res, err := Plan(m, f, Options{Name: "示例 · 一室一厅", Hints: HintsFromScan(w, l, f), Textures: fakeImages(t), Windows: WindowsFromScan(w, f)})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestPlanFictional(t *testing.T) {
	res := planFake(t)
	h := res.Home
	for i, r := range h.Rooms {
		t.Logf("%-4s %-8s clear %5.2f gross %5.2f floor %s y %.2f %v", r.Name, r.ID, res.Rooms[i].Area, res.Rooms[i].Gross, r.Floor, r.Y, r.Poly)
	}
	for _, d := range h.Doors {
		t.Logf("door %s %q on %s at %.2f w %.2f h %.2f %s entry %v", d.ID, d.Name, d.Wall, d.At, d.Width, d.Height, d.Kind, d.Entry)
	}
	for _, w := range h.Windows {
		t.Logf("window %s on %s at %.2f w %.2f sill %.2f h %.2f", w.ID, w.Wall, w.At, w.Width, w.Sill, w.Height)
	}
	for _, l := range res.Levels {
		t.Logf("level %s %s y %.2f area %.2f %s", l.ID, l.Kind, l.Y, l.Area, l.Floor)
	}
	for _, n := range res.Notes {
		t.Logf("note: %s", n)
	}
	if p := h.Check(); len(p) > 0 {
		t.Errorf("Check: %v", p)
	}
	if math.Abs(h.WallHeight-fakeCeiling) > 0.02 {
		t.Errorf("wall height %.2f", h.WallHeight)
	}
	if len(h.Rooms) != len(fakeAreas) {
		t.Errorf("%d rooms, want %d", len(h.Rooms), len(fakeAreas))
	}
	byName := map[string]*home.Room{}
	for i, r := range h.Rooms {
		byName[r.Name] = &h.Rooms[i]
		want, ok := fakeAreas[r.Name]
		if !ok {
			t.Errorf("a room called %q", r.Name)
			continue
		}
		if a := res.Rooms[i].Area; math.Abs(a-want) > 0.06*want {
			t.Errorf("%s: %.2f m², want %.2f", r.Name, a, want)
		}
		if res.Rooms[i].Gross < res.Rooms[i].Area {
			t.Errorf("%s: gross %.2f below clear %.2f", r.Name, res.Rooms[i].Gross, res.Rooms[i].Area)
		}
	}
	for name, floor := range map[string]string{"客厅": "tile", "卧室": "wood", "卫生间": "tile"} {
		if r := byName[name]; r == nil || r.Floor != floor {
			t.Errorf("%s floor: %+v, want %s", name, r, floor)
		}
	}
	// The doors: the front door into 客厅 and one from it to each of the other rooms.
	if d, ok := h.EntryDoor(); !ok || d.Width < 0.8 || d.Width > 1.0 {
		t.Errorf("front door %+v %v", d, ok)
	}
	between := func(a, b string) *home.Door {
		for i, d := range h.Doors {
			w, _ := h.Wall(d.Wall)
			p, n := w.At(d.At), w.Normal()
			o := w.Thick/2 + 0.3
			ra, rb := h.RoomAt(home.P{p[0] + n[0]*o, p[1] + n[1]*o}), h.RoomAt(home.P{p[0] - n[0]*o, p[1] - n[1]*o})
			if ra != nil && rb != nil && ((ra.Name == a && rb.Name == b) || (ra.Name == b && rb.Name == a)) {
				return &h.Doors[i]
			}
		}
		return nil
	}
	for _, c := range []struct {
		to    string
		width float64
	}{{"卧室", 0.9}, {"卫生间", 0.75}} {
		d := between("客厅", c.to)
		if d == nil {
			t.Errorf("no door between 客厅 and %s", c.to)
			continue
		}
		if math.Abs(d.Width-c.width) > 0.08 || math.Abs(d.Height-2.0) > 0.1 {
			t.Errorf("door to %s: width %.2f height %.2f, want %.2f × 2.0", c.to, d.Width, d.Height, c.width)
		}
	}
	if len(h.Doors) != 3 {
		t.Errorf("%d doors, want 3", len(h.Doors))
	}
	// Both windows, 1.2 and 1.6 m wide, 0.9 to 2.1 m up.
	var widths []float64
	for _, w := range h.Windows {
		widths = append(widths, w.Width)
		if math.Abs(w.Sill-0.9) > 0.1 || math.Abs(w.Sill+w.Height-2.1) > 0.1 {
			t.Errorf("window %s sill %.2f height %.2f", w.ID, w.Sill, w.Height)
		}
	}
	sort.Float64s(widths)
	if len(widths) != 2 || math.Abs(widths[0]-1.2) > 0.08 || math.Abs(widths[1]-1.6) > 0.08 {
		t.Errorf("window widths %v, want 1.2 and 1.6", widths)
	}
	// The platform and its step in 客厅.
	var plat, step *Level
	for i, l := range res.Levels {
		if l.Room != byName["客厅"].ID {
			continue
		}
		switch l.Kind {
		case "platform":
			plat = &res.Levels[i]
		case "step":
			step = &res.Levels[i]
		}
	}
	if plat == nil || step == nil {
		t.Fatalf("platform %v, step %v", plat, step)
	}
	if math.Abs(plat.Y-0.26) > 0.02 || math.Abs(plat.Area-1.7*1.8) > 0.4 || plat.Floor != "wood" {
		t.Errorf("platform %+v", *plat)
	}
	if math.Abs(step.Y-0.13) > 0.02 {
		t.Errorf("step %+v", *step)
	}
	if len(h.Levels) != 2 {
		t.Errorf("home levels %v", h.Levels)
	}
}

// With nothing to name them, the rooms get numbers, the passage-free flat still plans.
func TestPlanFictionalWithoutHints(t *testing.T) {
	m, f := fakeFrame(t)
	res, err := Plan(m, f, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Home.Rooms) != len(fakeAreas) {
		t.Errorf("%d rooms", len(res.Home.Rooms))
	}
	for _, r := range res.Home.Rooms {
		if r.Floor != "" {
			t.Errorf("%s: floor %q without textures", r.Name, r.Floor)
		}
	}
}

func TestNamesFromFictionalScan(t *testing.T) {
	_, f := fakeFrame(t)
	w, l := fakeWork()
	hs := HintsFromScan(w, l, f)
	if len(hs) != len(w.Observers) {
		t.Fatalf("%d hints", len(hs))
	}
	for i, h := range hs {
		o := w.Observers[i]
		want := f.Point(o.Standing[0], -o.Standing[2], o.Standing[1])
		if h.Name != l[i].Name || h.Kind != KindOf(h.Name) || math.Abs(h.At[0]-want[0]) > 1e-9 || math.Abs(h.At[1]-want[2]) > 1e-9 {
			t.Errorf("hint %+v", h)
		}
	}
	ws := WindowsFromScan(w, f)
	if len(ws) != 2 {
		t.Fatalf("%d windows", len(ws))
	}
	for _, wh := range ws {
		if math.Abs(wh.Y-1.5) > 0.01 {
			t.Errorf("window label at %.2f m", wh.Y)
		}
	}
}

// fakePlanHints are the fictional rooms as a floor plan would give them: outlines, north
// up, in the home's frame (x east from the west wall's centre, z south from the north's).
func fakePlanHints() []RoomHint {
	box := func(x0, y0, x1, y1 float64) []home.P {
		const h = ExteriorThick / 2
		return []home.P{{x0 + h, 7.5 - y1 + h}, {x1 + h, 7.5 - y1 + h}, {x1 + h, 7.5 - y0 + h}, {x0 + h, 7.5 - y0 + h}}
	}
	return []RoomHint{
		{Name: "客厅", Poly: box(0, 0, 5, 4)},
		{Name: "卧室", Poly: box(0, 4.12, 3, 7.5)},
		{Name: "卫生间", Poly: box(3.12, 4.12, 5, 7.5)},
	}
}

func TestChooseTurnsFictional(t *testing.T) {
	m := fakeFlat()
	turns, err := ChooseTurns(m, fakePlanHints())
	if err != nil || turns != 0 {
		t.Errorf("turns %d, %v", turns, err)
	}
	// Turned a quarter clockwise (seen from above), it takes three more clockwise quarter
	// turns (Frame.Turns) to bring north back up.
	turned := fakeFlat()
	for _, c := range turned.Chunks {
		for i := 0; i+2 < len(c.Pos); i += 3 {
			c.Pos[i], c.Pos[i+1] = c.Pos[i+1], -c.Pos[i]
		}
	}
	if turns, err := ChooseTurns(turned, fakePlanHints()); err != nil || turns != 3 {
		t.Errorf("a turned scan: turns %d, %v", turns, err)
	}
}

func TestWriteGLBFictional(t *testing.T) {
	m, f := fakeFrame(t)
	tex := fakeTextures(t)
	for _, maxY := range []float64{0, 2.0} {
		var buf bytes.Buffer
		if err := WriteGLB(m, f, tex, &buf, GLBOptions{MaxY: maxY}); err != nil {
			t.Fatal(err)
		}
		doc, bin := readGLB(t, buf.Bytes())
		if n := len(doc["images"].([]any)); n != fakeChunks {
			t.Errorf("%d images", n)
		}
		prims := doc["meshes"].([]any)[0].(map[string]any)["primitives"].([]any)
		if len(prims) != fakeChunks {
			t.Fatalf("%d primitives", len(prims))
		}
		accs := doc["accessors"].([]any)
		views := doc["bufferViews"].([]any)
		tris, top, bottom := 0, -1e9, 1e9
		for _, p := range prims {
			pa := p.(map[string]any)
			pos := accs[int(pa["attributes"].(map[string]any)["POSITION"].(float64))].(map[string]any)
			top = max(top, pos["max"].([]any)[1].(float64))
			bottom = min(bottom, pos["min"].([]any)[1].(float64))
			ix := accs[int(pa["indices"].(float64))].(map[string]any)
			tris += int(ix["count"].(float64)) / 3
			v := views[int(ix["bufferView"].(float64))].(map[string]any)
			if end := int(v["byteOffset"].(float64)) + int(v["byteLength"].(float64)); end > len(bin) {
				t.Fatalf("view past the buffer")
			}
		}
		if math.Abs(bottom) > 0.01 {
			t.Errorf("floor at %.3f, want 0", bottom)
		}
		if maxY == 0 && (tris != m.Triangles() || math.Abs(top-fakeCeiling) > 0.01) {
			t.Errorf("triangles %d, want %d; top %.2f", tris, m.Triangles(), top)
		}
		if maxY > 0 && (tris >= m.Triangles() || top > maxY+0.3) {
			t.Errorf("cut at %.1f kept %d of %d triangles, top %.2f", maxY, tris, m.Triangles(), top)
		}
	}
}

func TestTopViewFictional(t *testing.T) {
	m, f := fakeFrame(t)
	v := TopView(m, f, fakeImages(t), 1.2, 0.02)
	b := v.Img.Bounds()
	// 5.24 × 7.74 m to the wall centres, plus the margins.
	if b.Dx() < 260 || b.Dx() > 320 || b.Dy() < 380 || b.Dy() > 440 || v.X0 > 0 || v.Z0 > 0 {
		t.Errorf("view %v at %.2f, %.2f", b, v.X0, v.Z0)
	}
	px := func(x, y float64) color.RGBA {
		s := toScan(x, y, 0)
		p := f.Point(s[0], s[1], s[2])
		return v.Img.RGBAAt(int((p[0]-v.X0)/v.Cell), int((p[2]-v.Z0)/v.Cell))
	}
	if c := px(1.5, 6.0); finishOf(fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)) != "wood" {
		t.Errorf("卧室 from above is %v", c)
	}
	if c := px(1.5, 2.0); finishOf(fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)) != "tile" {
		t.Errorf("客厅 from above is %v", c)
	}
}

func TestImportFictional(t *testing.T) {
	w, l := fakeWork()
	res, glb, err := Import(fakeFlat(), w, l, fakeTextures(t), 0, Options{Name: "示例"})
	if err != nil {
		t.Fatal(err)
	}
	if p := res.Home.Check(); len(p) > 0 {
		t.Errorf("Check: %v", p)
	}
	doc, _ := readGLB(t, glb)
	names := map[string]bool{}
	for _, r := range res.Home.Rooms {
		names[r.Name] = true
	}
	if len(doc["images"].([]any)) != fakeChunks || !names["客厅"] || !names["卧室"] || !names["卫生间"] || len(res.Home.Windows) != 2 {
		t.Errorf("%d images, rooms %v, %d windows", len(doc["images"].([]any)), names, len(res.Home.Windows))
	}
}

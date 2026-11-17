package floorplan

// A made-up flat, drawn at run time the way 链家/贝壳 draw their plans: thick dark walls with
// openings for doors and windows (thin lines across a window, a thin arc for a swing door),
// and a dimension chain with tick marks along each side. The reading that goes with it is
// what a model would say about the picture, with one mistake in it on purpose: it puts the
// bathroom door between 卫生间 and 主卧, while the picture has it opening onto 客厅.
//
//	+-----------+------+------------+
//	|           | 卫生间 |            |
//	|    主卧     +------+    次卧      |
//	|           |      |            |
//	|           |      +-----+------+
//	+-----------+            | 厨房  |
//	|                        |      |
//	|          客厅           +------+
//	|                               |
//	+---+------------------+--------+
//	    |       阳台        |
//	    +------------------+

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"math"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	agentpkg "github.com/liliang-cn/agent-go/v3/pkg/agent"
	"github.com/liliang-cn/agent-go/v3/pkg/extensiontest"

	"hometwin/internal/home"
)

// The drawing: millimetres to pixels, the plan's top-left wall corner at (left, top).
const (
	fScale        = 15.0 // mm a pixel
	fLeft, fTop   = 160, 160
	fWidth        = 920
	fHeight       = 960
	fChainGap     = 70 // pixels between the outline and its dimension chain
	fExterior     = 240.0
	fInterior     = 120.0
	fBalconyWall  = 120.0
	fBalconyFloor = 200.0 // the wall between the living room and the balcony
)

type fRoom struct {
	name, kind string
	poly       []home.P // mm, along the walls' centre lines
}

type fWall struct {
	a, b     home.P
	thick    float64
	exterior bool
}

type fDoor struct {
	between   []string
	a, b      home.P // the opening, on a wall's centre line
	kind      string
	opensInto string
}

type fWindow struct {
	room string
	a, b home.P
}

var (
	fRooms = []fRoom{
		{"主卧", "bedroom", []home.P{{0, 0}, {3600, 0}, {3600, 4200}, {0, 4200}}},
		{"卫生间", "bath", []home.P{{3600, 0}, {5700, 0}, {5700, 2700}, {3600, 2700}}},
		{"次卧", "bedroom", []home.P{{5700, 0}, {9000, 0}, {9000, 3600}, {5700, 3600}}},
		{"厨房", "kitchen", []home.P{{7200, 3600}, {9000, 3600}, {9000, 6300}, {7200, 6300}}},
		{"客厅", "living", []home.P{{3600, 2700}, {5700, 2700}, {5700, 3600}, {7200, 3600}, {7200, 6300}, {9000, 6300}, {9000, 8400}, {0, 8400}, {0, 4200}, {3600, 4200}}},
		{"阳台", "balcony", []home.P{{1200, 8400}, {6600, 8400}, {6600, 9900}, {1200, 9900}}},
	}
	fWalls = []fWall{
		// the outside
		{home.P{0, 0}, home.P{9000, 0}, fExterior, true},
		{home.P{9000, 0}, home.P{9000, 8400}, fExterior, true},
		{home.P{0, 0}, home.P{0, 8400}, fExterior, true},
		{home.P{0, 8400}, home.P{1200, 8400}, fExterior, true},
		{home.P{6600, 8400}, home.P{9000, 8400}, fExterior, true},
		{home.P{1200, 8400}, home.P{1200, 9900}, fBalconyWall, true},
		{home.P{1200, 9900}, home.P{6600, 9900}, fBalconyWall, true},
		{home.P{6600, 8400}, home.P{6600, 9900}, fBalconyWall, true},
		// inside
		{home.P{3600, 0}, home.P{3600, 4200}, fInterior, false},
		{home.P{0, 4200}, home.P{3600, 4200}, fInterior, false},
		{home.P{3600, 2700}, home.P{5700, 2700}, fInterior, false},
		{home.P{5700, 0}, home.P{5700, 3600}, fInterior, false},
		{home.P{5700, 3600}, home.P{9000, 3600}, fInterior, false},
		{home.P{7200, 3600}, home.P{7200, 6300}, fInterior, false},
		{home.P{7200, 6300}, home.P{9000, 6300}, fInterior, false},
		{home.P{1200, 8400}, home.P{6600, 8400}, fBalconyFloor, false},
	}
	fDoors = []fDoor{
		{[]string{"客厅", "outside"}, home.P{9000, 6850}, home.P{9000, 7850}, "swing", "客厅"},
		{[]string{"主卧", "客厅"}, home.P{3600, 3000}, home.P{3600, 3900}, "swing", "主卧"},
		{[]string{"卫生间", "客厅"}, home.P{4250, 2700}, home.P{5050, 2700}, "swing", "卫生间"},
		{[]string{"次卧", "客厅"}, home.P{6050, 3600}, home.P{6850, 3600}, "swing", "次卧"},
		{[]string{"厨房", "客厅"}, home.P{7200, 4350}, home.P{7200, 5550}, "sliding", "厨房"},
		{[]string{"客厅", "阳台"}, home.P{2700, 8400}, home.P{5100, 8400}, "sliding", "阳台"},
	}
	fWindows = []fWindow{
		{"主卧", home.P{900, 0}, home.P{2700, 0}},
		{"卫生间", home.P{4200, 0}, home.P{5100, 0}},
		{"次卧", home.P{6600, 0}, home.P{8100, 0}},
		{"厨房", home.P{9000, 4200}, home.P{9000, 5700}},
		{"客厅", home.P{0, 5400}, home.P{0, 7200}},
		{"阳台", home.P{1500, 9900}, home.P{6300, 9900}},
	}
	// the chains, tick to tick in millimetres along each side
	fChains = map[string][]float64{
		"top":    {0, 3600, 5700, 9000},
		"bottom": {0, 1200, 6600, 9000},
		"left":   {0, 4200, 8400, 9900},
		"right":  {0, 3600, 6300, 8400},
	}
)

func fpx(v home.P) (float64, float64) { return fLeft + v[0]/fScale, fTop + v[1]/fScale }

// fPrinted is a room's area as the plan prints it: on the centre lines, to 0.1 m².
func fPrinted(r fRoom) float64 { return math.Round(home.Area(r.poly)/1e5) / 10 }

// fictionalImage draws the made-up plan.
func fictionalImage() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, fWidth, fHeight))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	ink := func(v uint8) color.RGBA { return color.RGBA{v, v, v, 255} }
	rect := func(x0, y0, x1, y1 float64, c color.RGBA) {
		for y := int(math.Round(y0)); y < int(math.Round(y1)); y++ {
			for x := int(math.Round(x0)); x < int(math.Round(x1)); x++ {
				img.SetRGBA(x, y, c)
			}
		}
	}
	// a segment straight across or down, half pixels either side of it
	band := func(a, b home.P, half float64, c color.RGBA) {
		x0, y0 := fpx(a)
		x1, y1 := fpx(b)
		rect(math.Min(x0, x1)-half, math.Min(y0, y1)-half, math.Max(x0, x1)+half, math.Max(y0, y1)+half, c)
	}
	line := func(x0, y0, x1, y1 float64, c color.RGBA) {
		n := int(math.Hypot(x1-x0, y1-y0)) + 1
		for i := 0; i <= n; i++ {
			t := float64(i) / float64(n)
			img.SetRGBA(int(math.Round(x0+(x1-x0)*t)), int(math.Round(y0+(y1-y0)*t)), c)
		}
	}
	wallAt := func(a, b home.P) fWall {
		for _, w := range fWalls {
			on := func(p home.P) bool {
				return (w.a[0] == w.b[0] && p[0] == w.a[0] && p[1] >= math.Min(w.a[1], w.b[1]) && p[1] <= math.Max(w.a[1], w.b[1])) ||
					(w.a[1] == w.b[1] && p[1] == w.a[1] && p[0] >= math.Min(w.a[0], w.b[0]) && p[0] <= math.Max(w.a[0], w.b[0]))
			}
			if on(a) && on(b) {
				return w
			}
		}
		panic("an opening off every wall")
	}
	for _, w := range fWalls {
		half := w.thick / fScale / 2
		x0, y0 := fpx(w.a)
		x1, y1 := fpx(w.b)
		if w.a[0] == w.b[0] {
			rect(x0-half, math.Min(y0, y1)-half, x0+half, math.Max(y0, y1)+half, ink(25))
		} else {
			rect(math.Min(x0, x1)-half, y0-half, math.Max(x0, x1)+half, y0+half, ink(25))
		}
	}
	for _, d := range fDoors {
		w := wallAt(d.a, d.b)
		half := w.thick/fScale/2 + 1
		x0, y0 := fpx(d.a)
		x1, y1 := fpx(d.b)
		vertical := d.a[0] == d.b[0]
		if vertical {
			rect(x0-half, y0, x0+half, y1, ink(255))
		} else {
			rect(x0, y0-half, x1, y0+half, ink(255))
		}
		if d.kind == "sliding" {
			// two panels drawn as thin lines in the opening
			if vertical {
				line(x0-2, y0, x0-2, (y0+y1)/2+4, ink(140))
				line(x0+2, (y0+y1)/2-4, x0+2, y1, ink(140))
			} else {
				line(x0, y0-2, (x0+x1)/2+4, y0-2, ink(140))
				line((x0+x1)/2-4, y0+2, x1, y0+2, ink(140))
			}
			continue
		}
		// a swing door: the leaf standing open and its arc, hinged at a
		r := math.Hypot(x1-x0, y1-y0)
		for k := 0; k <= 90; k++ {
			t := float64(k) * math.Pi / 180
			if vertical {
				img.SetRGBA(int(x0-r*math.Sin(t)), int(y0+r*math.Cos(t)), ink(150))
			} else {
				img.SetRGBA(int(x0+r*math.Cos(t)), int(y0+r*math.Sin(t)), ink(150))
			}
		}
		if vertical {
			line(x0, y0, x0-r, y0, ink(150))
		} else {
			line(x0, y0, x0, y0+r, ink(150))
		}
	}
	for _, wn := range fWindows {
		w := wallAt(wn.a, wn.b)
		half := w.thick / fScale / 2
		band(wn.a, wn.b, half+1, ink(255))
		x0, y0 := fpx(wn.a)
		x1, y1 := fpx(wn.b)
		for _, o := range []float64{-half, 0, half} {
			if wn.a[0] == wn.b[0] {
				line(x0+o, y0, x1+o, y1, ink(120))
			} else {
				line(x0, y0+o, x1, y1+o, ink(120))
			}
		}
	}
	// dimension chains: a grey line and a dark tick across it at every mark
	for side, ticks := range fChains {
		for i, m := range ticks {
			var x, y float64
			switch side {
			case "top", "bottom":
				x, _ = fpx(home.P{m, 0})
				y = fChainLine(side)
				rect(x-1, y-9, x+1, y+10, ink(40))
				if i > 0 {
					px, _ := fpx(home.P{ticks[i-1], 0})
					line(px, y, x, y, ink(130))
				}
			default:
				_, y = fpx(home.P{0, m})
				x = fChainLine(side)
				rect(x-9, y-1, x+10, y+1, ink(40))
				if i > 0 {
					_, py := fpx(home.P{0, ticks[i-1]})
					line(x, py, x, y, ink(130))
				}
			}
		}
	}
	return img
}

// fChainLine is the pixel row (top, bottom) or column (left, right) of a side's chain.
func fChainLine(side string) float64 {
	switch side {
	case "top":
		return fTop - fChainGap
	case "bottom":
		_, y := fpx(home.P{0, 9900})
		return y + fChainGap
	case "left":
		return fLeft - fChainGap
	}
	x, _ := fpx(home.P{9000, 0})
	return x + fChainGap
}

// fictionalReading is what a model reads off the made-up plan. Its pixel positions are a
// little off, by scale and offset as a model's often are, and the bathroom door is put
// between the wrong rooms.
func fictionalReading() Reading {
	pt := func(v home.P) [2]float64 {
		x, y := fpx(v)
		return [2]float64{math.Round(x*1.015 - 5), math.Round(y*0.99 + 6)}
	}
	rd := Reading{Chains: map[string]Chain{}}
	for side, ticks := range fChains {
		c := Chain{Line: fChainLine(side)}
		for i := 1; i < len(ticks); i++ {
			a, b := home.P{ticks[i-1], 0}, home.P{ticks[i], 0}
			k := 0
			if !horizontal(side) {
				a, b, k = home.P{0, ticks[i-1]}, home.P{0, ticks[i]}, 1
			}
			c.Segments = append(c.Segments, Seg{MM: ticks[i] - ticks[i-1], From: pt(a)[k], To: pt(b)[k]})
		}
		rd.Chains[side] = c
	}
	for _, r := range fRooms {
		rr := RoomR{Name: r.name, Kind: r.kind, Area: fPrinted(r)}
		for _, v := range r.poly {
			rr.Poly = append(rr.Poly, pt(v))
		}
		rd.Rooms = append(rd.Rooms, rr)
	}
	for _, w := range fWalls {
		rd.Walls = append(rd.Walls, WallR{A: pt(w.a), B: pt(w.b), Exterior: w.exterior})
	}
	for _, d := range fDoors {
		between := d.between
		if d.between[0] == "卫生间" {
			between = []string{"卫生间", "主卧"} // the mistake
		}
		a, b := pt(d.a), pt(d.b)
		rd.Doors = append(rd.Doors, DoorR{Between: between, Center: [2]float64{(a[0] + b[0]) / 2, (a[1] + b[1]) / 2},
			Width: math.Hypot(b[0]-a[0], b[1]-a[1]), Kind: d.kind, OpensInto: d.opensInto})
	}
	for _, wn := range fWindows {
		rd.Windows = append(rd.Windows, WindowR{Room: wn.room, A: pt(wn.a), B: pt(wn.b)})
	}
	// it goes through JSON like a recorded reading does
	raw, err := json.Marshal(rd)
	if err != nil {
		panic(err)
	}
	out, err := ParseReading(string(raw))
	if err != nil {
		panic(err)
	}
	return out
}

func fictionalPlan(t *testing.T) (*Plan, Reading, image.Image) {
	t.Helper()
	rd, img := fictionalReading(), fictionalImage()
	p, err := Build(rd, img)
	if err != nil {
		t.Fatal(err)
	}
	return p, rd, img
}

// The chains drawn on the made-up plan give its scale, and their ticks are found where they
// were drawn, not where the model says.
func TestFictionalScale(t *testing.T) {
	p, rd, _ := fictionalPlan(t)
	for _, ax := range []axis{p.x, p.z} {
		if math.Abs(ax.mmPerPx/fScale-1) > 0.02 {
			t.Errorf("scale %.2f mm/px, want %.1f", ax.mmPerPx, fScale)
		}
	}
	for side, ticks := range fChains {
		f, ok := findChain(p.r, side, rd.Chains[side])
		if !ok {
			t.Errorf("%s chain not found", side)
			continue
		}
		if math.Abs(float64(f.line)-fChainLine(side)) > 1 {
			t.Errorf("%s chain on line %d, drawn on %.0f", side, f.line, fChainLine(side))
		}
		for i, m := range ticks {
			x, y := fpx(home.P{m, m})
			want := x
			if !horizontal(side) {
				want = y
			}
			if math.Abs(f.px[i]-want) > 2 {
				t.Errorf("%s tick %d at %.1f, drawn at %.0f", side, i, f.px[i], want)
			}
		}
	}
}

// The made-up plan comes out as drawn: every room at its printed area, nothing overlapping or
// left over, every door between two places, and the one door the model got wrong reported.
func TestFictionalDraft(t *testing.T) {
	p, _, _ := fictionalPlan(t)
	d := p.Draft("plan-test", "测试")
	if errs := d.Home.Check(); len(errs) > 0 {
		t.Fatal(errs)
	}
	if len(d.Areas) != len(fRooms) {
		t.Fatalf("%d rooms, want %d", len(d.Areas), len(fRooms))
	}
	for i, a := range d.Areas {
		t.Logf("%s 图上 %.1f 算出 %.2f (%+.1f%%)", a.Name, a.Printed, a.Computed, a.Diff*100)
		if a.Name != fRooms[i].name || a.Printed != fPrinted(fRooms[i]) {
			t.Errorf("room %d is %s %.1f m², want %s %.1f", i, a.Name, a.Printed, fRooms[i].name, fPrinted(fRooms[i]))
		}
		if math.Abs(a.Diff) > 0.05 {
			t.Errorf("%s computed %.2f m², printed %.1f", a.Name, a.Computed, a.Printed)
		}
	}
	if d.Convention != "轴线面积" {
		t.Errorf("convention %s", d.Convention)
	}
	reported := false
	for _, is := range d.Issues {
		t.Logf("issue: %s", is.Text)
		switch {
		case is.Kind == "door" && strings.Contains(is.Text, "卫生间门"):
			reported = true
		case is.Level == "warn":
			t.Errorf("unexpected issue %s", is.Text)
		}
	}
	if !reported {
		t.Error("the 卫生间 door mismatch is not reported")
	}
	entry, ok := d.Home.EntryDoor()
	if !ok {
		t.Fatal("no entry door")
	}
	if w, _ := d.Home.Wall(entry.Wall); math.Abs(w.At(entry.At)[0]-9) > 0.05 || math.Abs(w.At(entry.At)[1]-7.35) > 0.15 {
		t.Errorf("entry door at %v", w.At(entry.At))
	}
	if len(d.Home.Doors) != len(fDoors) || len(d.Home.Windows) != len(fWindows) {
		t.Errorf("%d doors, %d windows; want %d, %d", len(d.Home.Doors), len(d.Home.Windows), len(fDoors), len(fWindows))
	}
	// the outline starts at (0, 0) and spans the chains: 9.0 m by 9.9 m
	maxX, maxZ := 0.0, 0.0
	for i, r := range d.Home.Rooms {
		for _, v := range r.Poly {
			if v[0] < 0 || v[1] < 0 {
				t.Fatalf("%s goes below 0: %v", r.Name, v)
			}
			maxX, maxZ = math.Max(maxX, v[0]), math.Max(maxZ, v[1])
		}
		if math.Abs(home.Area(r.Poly)-home.Area(fRooms[i].poly)/1e6) > 0.05 {
			t.Errorf("%s is %v, drawn as %v", r.Name, r.Poly, fRooms[i].poly)
		}
	}
	if math.Abs(maxX-9) > 0.05 || math.Abs(maxZ-9.9) > 0.05 {
		t.Errorf("home is %.2f × %.2f m", maxX, maxZ)
	}
	// every door joins two different places, the ones the picture shows
	for i, dr := range d.Home.Doors {
		got := joins(d.Home, dr)
		if got[0] == got[1] {
			t.Errorf("door %s has %s on both sides", dr.Name, got[0])
		}
		if !sameSet(got, fDoors[i].between) {
			t.Errorf("door %s joins %v, drawn between %v", dr.Name, got, fDoors[i].between)
		}
	}
	for _, w := range d.Home.Walls {
		if w.Exterior && w.Thick < 0.1 {
			t.Errorf("outside wall %s is %.2f m thick", w.ID, w.Thick)
		}
	}
}

// The fixer looks at the bathroom door on the made-up plan and confirms where the picture
// has it, and nothing is left to warn about.
func TestFictionalFixer(t *testing.T) {
	t.Setenv("AGENTGO_HOME", t.TempDir())
	p, _, img := fictionalPlan(t)
	door := ""
	for i, d := range p.Doors {
		if doorLabel(d) == "卫生间门" {
			door = "d" + string(rune('1'+i))
		}
	}
	if door == "" {
		t.Fatal("no 卫生间门 in the plan")
	}
	llm := extensiontest.Script(
		extensiontest.CallTool("look", map[string]any{"x0": 3.5, "z0": 2.0, "x1": 5.8, "z1": 3.5, "question": "卫生间的门在哪面墙上，通向哪里？"}),
		extensiontest.CallTool("confirm_door", map[string]any{"id": door, "reason": "门开在南墙，通客厅"}),
		extensiontest.Answer("卫生间的门其实通向客厅，改好了。"),
	)
	vision := &fakeVision{}
	f := NewFixer(llm, vision, t.TempDir())
	f.build = func(b *agentpkg.Builder) (*agentpkg.Service, error) {
		return extensiontest.NewServiceWithBuilder(b, t.TempDir())
	}
	defer f.Close()
	var log []string
	d, err := f.Fix(context.Background(), p, img, "plan-test", "测试", 1, func(s string) { log = append(log, s) })
	if err != nil {
		t.Fatal(err)
	}
	t.Log(strings.Join(log, "\n"))
	if len(vision.asked) != 1 || !strings.Contains(vision.asked[0], "卫生间的门在哪面墙上") {
		t.Fatalf("vision was asked %q", vision.asked)
	}
	for _, is := range d.Issues {
		if is.Level == "warn" {
			t.Errorf("left: %s", is.Text)
		}
	}
	if len(log) < 3 || !strings.Contains(strings.Join(log, " "), "认下门") {
		t.Errorf("steps: %q", log)
	}
}

// The endpoint takes the made-up plan as a phone screenshot (dark bars above and below), cuts
// it to the drawing, runs the pipeline on a recorded reading and makes the draft the home on
// confirm.
func TestFictionalHandler(t *testing.T) {
	rd, plan := fictionalReading(), fictionalImage()
	const bar = 150
	shot := image.NewRGBA(image.Rect(0, 0, fWidth, fHeight+2*bar))
	for y := range shot.Bounds().Dy() {
		for x := range fWidth {
			c := color.RGBA{28, 28, 30, 255}
			if y >= bar && y < bar+fHeight {
				c = plan.RGBAAt(x, y-bar)
			}
			shot.SetRGBA(x, y, c)
		}
	}
	var used home.Home
	im, err := NewImporter(t.TempDir(), nil, home.Sample, func(h home.Home) error { used = h; return nil }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	var seen image.Rectangle
	im.read = func(_ context.Context, img image.Image) (Reading, string, error) {
		seen = img.Bounds()
		return rd, "{}", nil
	}
	srv := httptest.NewServer(im.Handler())
	defer srv.Close()

	var pic bytes.Buffer
	if err := png.Encode(&pic, shot); err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("image", "plan.png")
	fw.Write(pic.Bytes())
	mw.Close()
	res, err := http.Post(srv.URL+"/api/floorplan", mw.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	var j Job
	json.NewDecoder(res.Body).Decode(&j)
	res.Body.Close()
	if res.StatusCode != 200 || j.ID == "" {
		t.Fatalf("upload: %d %+v", res.StatusCode, j)
	}
	deadline := time.Now().Add(20 * time.Second)
	for j.Status == "running" && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		res, err := http.Get(srv.URL + "/api/floorplan/" + j.ID)
		if err != nil {
			t.Fatal(err)
		}
		json.NewDecoder(res.Body).Decode(&j)
		res.Body.Close()
	}
	if j.Status != "done" || j.Draft == nil {
		t.Fatalf("job ended %s: %s %+v", j.Status, j.Error, j.Steps)
	}
	if seen != image.Rect(0, 0, fWidth, fHeight) {
		t.Errorf("the model was shown %v, want the drawing alone", seen)
	}
	fr := j.Draft.Frame
	if fr.Size != [2]int{fWidth, fHeight + 2*bar} || fr.Crop != [4]int{0, bar, fWidth, bar + fHeight} {
		t.Errorf("frame size %v crop %v", fr.Size, fr.Crop)
	}
	if len(fr.X) < 4 || len(fr.Z) < 4 || math.Abs(fr.PxM[0]-1000/fScale) > 1.5 {
		t.Errorf("frame %+v", fr)
	}
	res, err = http.Get(srv.URL + "/api/floorplan/" + j.ID + "/image")
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("image: %v %v", res.StatusCode, err)
	}
	res.Body.Close()
	res, err = http.Post(srv.URL+"/api/floorplan/"+j.ID+"/confirm", "application/json", nil)
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("confirm: %v %v", res.StatusCode, err)
	}
	res.Body.Close()
	if used.ID != "plan-"+j.ID || len(used.Rooms) != len(fRooms) || len(used.Doors) != len(fDoors) {
		t.Fatalf("the home in use is %q with %d rooms, %d doors", used.ID, len(used.Rooms), len(used.Doors))
	}
	if !strings.Contains(used.Name, "2室1厅") {
		t.Errorf("home name %q", used.Name)
	}
}

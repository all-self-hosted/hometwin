// Package floorplan turns a picture of a floor plan (the 链家/贝壳 kind: rooms with printed
// names and areas, dimension chains in millimetres along the sides) into the shell of a home.
//
// A vision model reads the plan: the chains' numbers, the rooms, walls, doors and windows. It
// reads numbers well and positions badly, so the positions are not used as they come: the
// chains' tick marks are found in the image and fitted to the numbers, which gives the scale
// and a grid of exact lines; the model's positions are mapped onto the image through the same
// ticks and snapped to that grid, or to wall strokes measured in the image. Walls are then
// drawn along the rooms' edges, and the result is checked against what the plan prints (room
// areas, which rooms a door joins). An agent goes through what the checks find and fixes what
// it can, looking again at the part of the picture in question.
package floorplan

import (
	"errors"
	"fmt"
	"image"
	"math"
	"sort"
	"strings"

	"hometwin/internal/home"
)

// Plan is a plan being turned into a home, in millimetres on the home's axes (x east, z
// south), with what was measured in the image to check it against.
type Plan struct {
	Rooms   []PRoom   `json:"rooms"`
	Doors   []PDoor   `json:"doors"`
	Windows []PWindow `json:"windows"`
	// Stubs are walls that are not the edge of any room (a wall end standing into a room).
	Stubs [][2]home.P `json:"stubs,omitempty"`

	reading Reading
	r       *raster
	m       []bool
	x, z    axis
	shift   home.P // mm added to the plan's coordinates to get the axes' millimetres
	walls   []mwall
}

type PRoom struct {
	Name    string   `json:"name"`
	Kind    string   `json:"kind"`
	Printed float64  `json:"printed"` // m², 0 when the plan prints none
	Poly    []home.P `json:"poly"`
}

type PDoor struct {
	Between   []string `json:"between"`
	At        home.P   `json:"at"`
	Width     float64  `json:"width"` // mm, 0 for the default
	Kind      string   `json:"kind"`
	OpensInto string   `json:"opensInto,omitempty"`
}

type PWindow struct {
	Room string `json:"room"`
	A    home.P `json:"a"`
	B    home.P `json:"b"`
}

// mwall is one of the model's walls on the plan's axes.
type mwall struct {
	a, b     home.P
	exterior bool
}

var kinds = map[string]bool{"living": true, "bedroom": true, "kitchen": true, "bath": true, "balcony": true, "entry": true}

// Build makes a plan from what the model read and the picture it read it from.
func Build(rd Reading, img image.Image) (*Plan, error) {
	p := &Plan{reading: rd, r: gray(img)}
	var fx, fz, mx, mz []chainFit
	for _, side := range []string{"top", "bottom", "left", "right"} {
		c, ok := rd.Chains[side]
		if !ok || len(c.Segments) == 0 {
			continue
		}
		f, found := findChain(p.r, side, c)
		switch {
		case found && horizontal(side):
			fx = append(fx, f)
		case found:
			fz = append(fz, f)
		case horizontal(side):
			mx = append(mx, f)
		default:
			mz = append(mz, f)
		}
	}
	var okx, okz bool
	p.x, okx = buildAxis(fx, mx)
	p.z, okz = buildAxis(fz, mz)
	if !okx || !okz {
		return nil, errors.New("图上没有找到尺寸标注，没法定比例")
	}
	win := int(math.Round(80 / ((p.x.mmPerPx + p.z.mmPerPx) / 2)))
	win = max(3, min(9, win|1))
	p.m = p.r.mask(win)
	p.register(rd.Walls)

	// the walls the model saw, measured in the image, give the lines no chain names
	for _, w := range rd.Walls {
		a := [2]float64{p.x.fix(w.A[0]), p.z.fix(w.A[1])}
		b := [2]float64{p.x.fix(w.B[0]), p.z.fix(w.B[1])}
		vertical := math.Abs(a[0]-b[0]) < math.Abs(a[1]-b[1])
		ax, along := &p.x, [2]float64{a[1], b[1]}
		across := (a[0] + b[0]) / 2
		if !vertical {
			ax, along, across = &p.z, [2]float64{a[0], b[0]}, (a[1]+b[1])/2
		}
		reach := int(400 / ax.mmPerPx)
		tol := 120 / ax.mmPerPx
		if c, width, cover, ok := band(p.m, p.r.w, p.r.h, vertical, across, along[0], along[1], reach); ok && cover >= 0.4 {
			ax.addLine(c, math.Max(tol, width/2+3))
		}
	}
	snapTol := func(ax *axis) float64 { return 450 / ax.mmPerPx }
	pt := func(v [2]float64) home.P {
		x, _ := p.x.snap(p.x.fix(v[0]), snapTol(&p.x))
		z, _ := p.z.snap(p.z.fix(v[1]), snapTol(&p.z))
		return home.P{x, z}
	}
	raw := func(v [2]float64) home.P {
		return home.P{p.x.mm(p.x.fix(v[0])), p.z.mm(p.z.fix(v[1]))}
	}
	for _, rr := range rd.Rooms {
		poly := p.outline(rr.Poly)
		if poly == nil {
			for _, v := range rr.Poly {
				poly = append(poly, pt(v))
			}
		}
		kind := strings.ToLower(strings.TrimSpace(rr.Kind))
		if !kinds[kind] {
			kind = guessKind(rr.Name)
		}
		p.Rooms = append(p.Rooms, PRoom{Name: strings.TrimSpace(rr.Name), Kind: kind, Printed: rr.Area, Poly: rectilinear(poly)})
	}
	for _, w := range rd.Walls {
		a, b := pt(w.A), pt(w.B)
		if math.Abs(a[0]-b[0]) < math.Abs(a[1]-b[1]) {
			b[0] = a[0]
		} else {
			b[1] = a[1]
		}
		p.walls = append(p.walls, mwall{a, b, w.Exterior})
	}
	for _, d := range rd.Doors {
		width := 0.0
		if d.Width > 0 {
			width = d.Width * p.x.a * (p.x.mmPerPx + p.z.mmPerPx) / 2
		}
		p.Doors = append(p.Doors, PDoor{Between: d.Between, At: raw(d.Center), Width: width, Kind: d.Kind, OpensInto: d.OpensInto})
	}
	for _, w := range rd.Windows {
		p.Windows = append(p.Windows, PWindow{Room: w.Room, A: raw(w.A), B: raw(w.B)})
	}
	// the home starts at (0, 0)
	lo := home.P{math.Inf(1), math.Inf(1)}
	for _, r := range p.Rooms {
		for _, v := range r.Poly {
			lo = home.P{math.Min(lo[0], v[0]), math.Min(lo[1], v[1])}
		}
	}
	if math.IsInf(lo[0], 1) {
		return nil, errors.New("没有认出房间")
	}
	p.shift = lo
	mv := func(v home.P) home.P { return home.P{v[0] - lo[0], v[1] - lo[1]} }
	for i := range p.Rooms {
		for j := range p.Rooms[i].Poly {
			p.Rooms[i].Poly[j] = mv(p.Rooms[i].Poly[j])
		}
	}
	for i := range p.walls {
		p.walls[i].a, p.walls[i].b = mv(p.walls[i].a), mv(p.walls[i].b)
	}
	for i := range p.Doors {
		p.Doors[i].At = mv(p.Doors[i].At)
	}
	for i := range p.Windows {
		p.Windows[i].A, p.Windows[i].B = mv(p.Windows[i].A), mv(p.Windows[i].B)
	}
	p.Stubs = p.stubs()
	return p, nil
}

func guessKind(name string) string {
	switch {
	case strings.Contains(name, "卧") || strings.Contains(name, "房间"):
		return "bedroom"
	case strings.Contains(name, "厨"):
		return "kitchen"
	case strings.Contains(name, "卫") || strings.Contains(name, "浴") || strings.Contains(name, "洗"):
		return "bath"
	case strings.Contains(name, "阳台") || strings.Contains(name, "露台"):
		return "balcony"
	case strings.Contains(name, "玄关") || strings.Contains(name, "过道") || strings.Contains(name, "走廊"):
		return "entry"
	}
	return "living"
}

// rectilinear cleans a snapped outline: no repeated or collinear corners, and a corner put
// into any edge that is not straight across or down.
func rectilinear(poly []home.P) []home.P {
	var out []home.P
	for _, v := range poly {
		if len(out) > 0 {
			prev := out[len(out)-1]
			if prev == v {
				continue
			}
			if prev[0] != v[0] && prev[1] != v[1] {
				// keep the longer leg first
				if math.Abs(prev[0]-v[0]) > math.Abs(prev[1]-v[1]) {
					out = append(out, home.P{v[0], prev[1]})
				} else {
					out = append(out, home.P{prev[0], v[1]})
				}
			}
		}
		out = append(out, v)
	}
	// close: the last edge back to the first corner
	if len(out) > 2 {
		first, last := out[0], out[len(out)-1]
		if first == last {
			out = out[:len(out)-1]
		} else if first[0] != last[0] && first[1] != last[1] {
			if math.Abs(first[0]-last[0]) > math.Abs(first[1]-last[1]) {
				out = append(out, home.P{first[0], last[1]})
			} else {
				out = append(out, home.P{last[0], first[1]})
			}
		}
	}
	return dropCollinear(out)
}

func dropCollinear(poly []home.P) []home.P {
	for changed := true; changed && len(poly) > 3; {
		changed = false
		for i := range poly {
			a, b, c := poly[(i+len(poly)-1)%len(poly)], poly[i], poly[(i+1)%len(poly)]
			if (a[0] == b[0] && b[0] == c[0]) || (a[1] == b[1] && b[1] == c[1]) || a == b {
				poly = append(poly[:i:i], poly[i+1:]...)
				changed = true
				break
			}
		}
	}
	return poly
}

// outline makes a room's outline from the model's corners: each edge goes onto the wall
// stroke nearest it in the image, and onto a grid line when one runs there. Nil when the
// corners do not make a straight-sided outline.
func (p *Plan) outline(raw [][2]float64) []home.P {
	type ed struct {
		vertical bool
		c        float64 // pixel position across
		s0, s1   float64 // pixel extent along
	}
	n := len(raw)
	var es []ed
	for i := range n {
		a := [2]float64{p.x.fix(raw[i][0]), p.z.fix(raw[i][1])}
		bb := raw[(i+1)%n]
		b := [2]float64{p.x.fix(bb[0]), p.z.fix(bb[1])}
		dx, dz := math.Abs(b[0]-a[0]), math.Abs(b[1]-a[1])
		if dx < 1 && dz < 1 {
			continue
		}
		e := ed{vertical: dz > dx, c: (a[1] + b[1]) / 2, s0: a[0], s1: b[0]}
		if e.vertical {
			e.c, e.s0, e.s1 = (a[0]+b[0])/2, a[1], b[1]
		}
		if len(es) > 0 && es[len(es)-1].vertical == e.vertical {
			last := &es[len(es)-1]
			l1, l2 := math.Abs(last.s1-last.s0), math.Abs(e.s1-e.s0)
			last.c = (last.c*l1 + e.c*l2) / math.Max(l1+l2, 1)
			last.s1 = e.s1
			continue
		}
		es = append(es, e)
	}
	if len(es) > 1 && es[0].vertical == es[len(es)-1].vertical {
		last, first := es[len(es)-1], &es[0]
		l1, l2 := math.Abs(last.s1-last.s0), math.Abs(first.s1-first.s0)
		first.c = (last.c*l1 + first.c*l2) / math.Max(l1+l2, 1)
		first.s0 = last.s0
		es = es[:len(es)-1]
	}
	if len(es) < 4 || len(es)%2 == 1 {
		return nil
	}
	mm := make([]float64, len(es))
	for i, e := range es {
		ax := &p.z
		if e.vertical {
			ax = &p.x
		}
		mm[i] = p.edgeLine(ax, e.vertical, e.c, e.s0, e.s1)
	}
	out := make([]home.P, 0, len(es))
	for i := range es {
		j := (i + 1) % len(es)
		if es[i].vertical {
			out = append(out, home.P{mm[i], mm[j]})
		} else {
			out = append(out, home.P{mm[j], mm[i]})
		}
	}
	return rectilinear(out)
}

// edgeLine is the millimetre position of a room edge the model put at pixel c: the wall
// stroke nearest it (on a grid line when one is close), else the nearest grid line.
func (p *Plan) edgeLine(ax *axis, vertical bool, c, s0, s1 float64) float64 {
	reach := int(math.Max(20, 700/ax.mmPerPx))
	// a wall with a wide door in it shows little stroke, so a little is enough
	if centre, width, cover, ok := band(p.m, p.r.w, p.r.h, vertical, c, s0, s1, reach); ok && cover >= 0.15 {
		if mm, ok := ax.snap(centre, width/2+150/ax.mmPerPx); ok {
			return mm
		}
		ax.addLine(centre, 0)
		mm, _ := ax.snap(centre, 1)
		return mm
	}
	mm, _ := ax.snap(c, 600/ax.mmPerPx)
	return mm
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	return s[len(s)/2]
}

// register maps the model's pixel positions onto the image: each of its walls is paired with
// the nearest wall stroke, and a scale and offset per axis fitted on the pairs most agree on.
func (p *Plan) register(walls []WallR) {
	var xs, xt, zs, zt []float64
	for _, w := range walls {
		vertical := math.Abs(w.A[0]-w.B[0]) < math.Abs(w.A[1]-w.B[1])
		if vertical {
			x := (w.A[0] + w.B[0]) / 2
			if c, _, cover, ok := band(p.m, p.r.w, p.r.h, true, x, w.A[1], w.B[1], int(800/p.x.mmPerPx)); ok && cover >= 0.4 {
				xs, xt = append(xs, x), append(xt, c)
			}
		} else {
			z := (w.A[1] + w.B[1]) / 2
			if c, _, cover, ok := band(p.m, p.r.w, p.r.h, false, z, w.A[0], w.B[0], int(800/p.z.mmPerPx)); ok && cover >= 0.4 {
				zs, zt = append(zs, z), append(zt, c)
			}
		}
	}
	// only when it clearly brings the model's walls closer to the strokes: the model's errors
	// are often not a matter of scale at all
	fit := func(ax *axis, s, t []float64) {
		a, c, ok := robustLine(s, t, 150/ax.mmPerPx)
		if !ok || a < 0.8 || a > 1.25 {
			return
		}
		before, after := make([]float64, len(s)), make([]float64, len(s))
		for i := range s {
			before[i], after[i] = math.Abs(t[i]-s[i]), math.Abs(t[i]-(a*s[i]+c))
		}
		if median(after) < 0.7*median(before) {
			ax.a, ax.c = a, c
		}
	}
	fit(&p.x, xs, xt)
	fit(&p.z, zs, zt)
}

// px is where a point of the plan is in the image.
func (p *Plan) px(v home.P) [2]float64 {
	return [2]float64{p.x.px(v[0] + p.shift[0]), p.z.px(v[1] + p.shift[1])}
}

// measure looks for a wall stroke along the plan's segment a–b (straight across or down) and
// says how thick it is and how much of the segment it covers.
func (p *Plan) measure(a, b home.P) (thick, cover float64, ok bool) {
	pa, pb := p.px(a), p.px(b)
	if a[0] == b[0] {
		_, w, cov, ok := band(p.m, p.r.w, p.r.h, true, pa[0], pa[1], pb[1], int(250/p.x.mmPerPx))
		return w * p.x.mmPerPx, cov, ok
	}
	_, w, cov, ok := band(p.m, p.r.w, p.r.h, false, pa[1], pa[0], pb[0], int(250/p.z.mmPerPx))
	return w * p.z.mmPerPx, cov, ok
}

// edge is a piece of a room's outline on one grid line.
type edge struct {
	vertical bool
	at       float64 // x of a vertical line, z of a horizontal one
	lo, hi   float64
	room     int
	plus     bool // the room is on the + side (east or south)
}

func (p *Plan) edges() []edge {
	var out []edge
	for ri, r := range p.Rooms {
		n := len(r.Poly)
		for i := range n {
			a, b := r.Poly[i], r.Poly[(i+1)%n]
			if a == b {
				continue
			}
			e := edge{room: ri}
			switch {
			case a[0] == b[0]:
				e.vertical, e.at, e.lo, e.hi = true, a[0], math.Min(a[1], b[1]), math.Max(a[1], b[1])
				e.plus = home.Inside(r.Poly, home.P{a[0] + 20, (a[1] + b[1]) / 2})
			case a[1] == b[1]:
				e.at, e.lo, e.hi = a[1], math.Min(a[0], b[0]), math.Max(a[0], b[0])
				e.plus = home.Inside(r.Poly, home.P{(a[0] + b[0]) / 2, a[1] + 20})
			default:
				continue
			}
			out = append(out, e)
		}
	}
	return out
}

// piece is a stretch of a grid line between rooms (or between a room and the outside).
type piece struct {
	vertical   bool
	at, lo, hi float64
	minus      []int // rooms on the − side
	plus       []int
	wall       bool
	exterior   bool
	thick      float64 // mm
}

func (pc piece) a() home.P {
	if pc.vertical {
		return home.P{pc.at, pc.lo}
	}
	return home.P{pc.lo, pc.at}
}

func (pc piece) b() home.P {
	if pc.vertical {
		return home.P{pc.at, pc.hi}
	}
	return home.P{pc.hi, pc.at}
}

// pieces splits every grid line the rooms' edges lie on into stretches with the same rooms on
// either side, and decides which stretches are walls.
func (p *Plan) pieces() []piece {
	type key struct {
		v  bool
		at float64
	}
	groups := map[key][]edge{}
	var keys []key
	for _, e := range p.edges() {
		k := key{e.vertical, e.at}
		if _, ok := groups[k]; !ok {
			keys = append(keys, k)
		}
		groups[k] = append(groups[k], e)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].v != keys[j].v {
			return !keys[i].v
		}
		return keys[i].at < keys[j].at
	})
	var out []piece
	for _, k := range keys {
		es := groups[k]
		var cuts []float64
		for _, e := range es {
			cuts = append(cuts, e.lo, e.hi)
		}
		sort.Float64s(cuts)
		for i := 0; i+1 < len(cuts); i++ {
			lo, hi := cuts[i], cuts[i+1]
			if hi-lo < 1 {
				continue
			}
			pc := piece{vertical: k.v, at: k.at, lo: lo, hi: hi}
			for _, e := range es {
				if e.lo <= lo && e.hi >= hi {
					if e.plus {
						pc.plus = append(pc.plus, e.room)
					} else {
						pc.minus = append(pc.minus, e.room)
					}
				}
			}
			if len(pc.plus)+len(pc.minus) == 0 {
				continue
			}
			p.decide(&pc)
			out = append(out, pc)
		}
	}
	return out
}

// decide says whether a stretch is a wall, and how thick: one with a room on one side only is
// an outside wall; one between rooms is a wall when the image shows one there, the model saw
// one, or a door is in it.
func (p *Plan) decide(pc *piece) {
	pc.exterior = len(pc.plus) == 0 || len(pc.minus) == 0
	thick, cover, seen := p.measure(pc.a(), pc.b())
	seen = seen && cover >= 0.25
	switch {
	case pc.exterior:
		pc.wall = true
	case seen:
		pc.wall = true
	case p.modelWallOn(*pc) >= 0.5:
		pc.wall = true
	case p.doorOn(*pc):
		pc.wall = true
	}
	if !pc.wall {
		return
	}
	if seen {
		pc.thick = math.Max(80, math.Min(300, round10(thick)))
		return
	}
	balcony := false
	for _, ri := range append(append([]int{}, pc.plus...), pc.minus...) {
		balcony = balcony || p.Rooms[ri].Kind == "balcony"
	}
	switch {
	case pc.exterior && balcony:
		pc.thick = 120
	case pc.exterior:
		pc.thick = 240
	default:
		pc.thick = 120
	}
}

// modelWallOn is how much of a stretch the model's walls cover.
func (p *Plan) modelWallOn(pc piece) float64 {
	cov := 0.0
	for _, w := range p.walls {
		if pc.vertical != (w.a[0] == w.b[0]) {
			continue
		}
		at, lo, hi := w.a[1], math.Min(w.a[0], w.b[0]), math.Max(w.a[0], w.b[0])
		if pc.vertical {
			at, lo, hi = w.a[0], math.Min(w.a[1], w.b[1]), math.Max(w.a[1], w.b[1])
		}
		if math.Abs(at-pc.at) > 60 {
			continue
		}
		cov += math.Max(0, math.Min(hi, pc.hi)-math.Max(lo, pc.lo))
	}
	return cov / (pc.hi - pc.lo)
}

func (p *Plan) doorOn(pc piece) bool {
	for _, d := range p.Doors {
		across, along := d.At[1], d.At[0]
		if pc.vertical {
			across, along = d.At[0], d.At[1]
		}
		if math.Abs(across-pc.at) <= 400 && along >= pc.lo && along <= pc.hi {
			return true
		}
	}
	return false
}

// stubs are the model's walls that no room edge accounts for but the image shows.
func (p *Plan) stubs() [][2]home.P {
	var out [][2]home.P
	es := p.edges()
	for _, w := range p.walls {
		vertical := w.a[0] == w.b[0]
		at, lo, hi := w.a[1], math.Min(w.a[0], w.b[0]), math.Max(w.a[0], w.b[0])
		if vertical {
			at, lo, hi = w.a[0], math.Min(w.a[1], w.b[1]), math.Max(w.a[1], w.b[1])
		}
		if hi-lo < 300 {
			continue
		}
		// what part of it no room edge covers
		var spans [][2]float64
		for _, e := range es {
			if e.vertical == vertical && math.Abs(e.at-at) <= 60 {
				spans = append(spans, [2]float64{e.lo, e.hi})
			}
		}
		for _, free := range subtract([2]float64{lo, hi}, spans) {
			if free[1]-free[0] < 300 {
				continue
			}
			a, b := home.P{free[0], at}, home.P{free[1], at}
			if vertical {
				a, b = home.P{at, free[0]}, home.P{at, free[1]}
			}
			if _, cover, ok := p.measure(a, b); ok && cover >= 0.5 {
				out = append(out, [2]home.P{a, b})
			}
		}
	}
	return out
}

// subtract is what is left of span after taking out the others.
func subtract(span [2]float64, others [][2]float64) [][2]float64 {
	out := [][2]float64{span}
	for _, o := range others {
		var next [][2]float64
		for _, s := range out {
			if o[1] <= s[0] || o[0] >= s[1] {
				next = append(next, s)
				continue
			}
			if o[0] > s[0] {
				next = append(next, [2]float64{s[0], o[0]})
			}
			if o[1] < s[1] {
				next = append(next, [2]float64{o[1], s[1]})
			}
		}
		out = next
	}
	return out
}

// Home makes the home from the plan: rooms as they are, walls along the rooms' edges (joined
// where a line runs on), doors and windows on the walls they are in. Doors and windows that
// find no wall are left out and said in the issues.
func (p *Plan) Home(id, name string) (home.Home, []Issue) {
	issues := []Issue{}
	h := home.Home{ID: id, Name: name, WallHeight: 2.8, CutHeight: 1.2}
	m := func(v home.P) home.P { return home.P{v[0] / 1000, v[1] / 1000} }
	ids := map[string]int{}
	for _, r := range p.Rooms {
		ids[r.Kind]++
	}
	seen := map[string]int{}
	total := 0.0
	for _, r := range p.Rooms {
		seen[r.Kind]++
		id := r.Kind
		if ids[r.Kind] > 1 {
			id = fmt.Sprintf("%s-%d", r.Kind, seen[r.Kind])
		}
		poly := make([]home.P, len(r.Poly))
		for i, v := range r.Poly {
			poly[i] = m(v)
		}
		h.Rooms = append(h.Rooms, home.Room{ID: id, Name: r.Name, Kind: r.Kind, Poly: poly})
		total += home.Area(poly)
	}
	h.Area = math.Round(total*10) / 10

	// walls: runs of stretches on one line with the same make
	ps := p.pieces()
	n := 0
	for i := 0; i < len(ps); {
		if !ps[i].wall {
			i++
			continue
		}
		j := i + 1
		for j < len(ps) && ps[j].wall && ps[j].vertical == ps[i].vertical && ps[j].at == ps[i].at &&
			ps[j].lo == ps[j-1].hi && ps[j].exterior == ps[i].exterior && math.Abs(ps[j].thick-ps[i].thick) <= 40 {
			j++
		}
		// the longest stretch says how thick
		thick, best := ps[i].thick, 0.0
		for k := i; k < j; k++ {
			if l := ps[k].hi - ps[k].lo; l > best {
				thick, best = ps[k].thick, l
			}
		}
		n++
		a, b := ps[i].a(), ps[j-1].b()
		h.Walls = append(h.Walls, home.Wall{ID: fmt.Sprintf("w%d", n), A: m(a), B: m(b), Thick: thick / 1000, Exterior: ps[i].exterior})
		i = j
	}
	for _, s := range p.Stubs {
		n++
		thick, _, ok := p.measure(s[0], s[1])
		if !ok {
			thick = 120
		}
		thick = math.Max(80, math.Min(300, round10(thick)))
		h.Walls = append(h.Walls, home.Wall{ID: fmt.Sprintf("w%d", n), A: m(s[0]), B: m(s[1]), Thick: thick / 1000})
	}

	// doors and windows go into the openings in the walls' strokes nearest where the model
	// put them; one that finds none stays where the model put it, on the nearest wall
	gaps := make([][][2]float64, len(h.Walls))
	for i, w := range h.Walls {
		gaps[i] = p.gaps(w)
	}
	used := map[[2]int]bool{} // wall, gap
	type cand struct {
		item, wall, gap int
		score           float64
	}
	pick := func(n int, reach float64, centre func(int) home.P, fits func(item, wall int, c home.P) (bool, float64)) []cand {
		var cs []cand
		for i := range n {
			c0 := centre(i)
			for wi, w := range h.Walls {
				for gi, g := range gaps[wi] {
					c := w.At((g[0] + g[1]) / 2)
					d := math.Hypot(c[0]-c0[0], c[1]-c0[1])
					if d > reach {
						continue
					}
					if ok, bonus := fits(i, wi, c); ok {
						cs = append(cs, cand{i, wi, gi, d - bonus})
					}
				}
			}
		}
		sort.Slice(cs, func(i, j int) bool { return cs[i].score < cs[j].score })
		out := make([]cand, n)
		for i := range out {
			out[i].wall = -1
		}
		for _, c := range cs {
			if out[c.item].wall >= 0 || used[[2]int{c.wall, c.gap}] {
				continue
			}
			out[c.item] = c
			used[[2]int{c.wall, c.gap}] = true
		}
		return out
	}
	sides := func(w home.Wall, c home.P) []string {
		n := w.Normal()
		var out []string
		for _, s := range []float64{0.3, -0.3} {
			name := "outside"
			if r := h.RoomAt(home.P{c[0] + n[0]*s, c[1] + n[1]*s}); r != nil {
				name = r.Name
			}
			out = append(out, name)
		}
		return out
	}
	doorAt := pick(len(p.Doors), 1.3, func(i int) home.P { return m(p.Doors[i].At) }, func(i, wi int, c home.P) (bool, float64) {
		got := sides(h.Walls[wi], c)
		match := 0
		for _, b := range p.Doors[i].Between {
			if slicesHas(got, b) {
				match++
			}
		}
		return true, 0.4 * float64(match)
	})
	entry := false
	for i, d := range p.Doors {
		if d.Kind == "removed" {
			continue
		}
		label := doorLabel(d)
		kind := d.Kind
		if kind != "swing" && kind != "sliding" && kind != "opening" {
			kind = "swing"
		}
		outside := slicesHas(d.Between, "outside")
		width := d.Width / 1000
		var w home.Wall
		var at float64
		if c := doorAt[i]; c.wall >= 0 {
			w = h.Walls[c.wall]
			g := gaps[c.wall][c.gap]
			at, width = (g[0]+g[1])/2, g[1]-g[0]
			if kind == "swing" && width > 1.3 {
				// a wide opening with a door in it: the door where the model put it
				d0 := w.Dir()
				t := (m(d.At)[0]-w.A[0])*d0[0] + (m(d.At)[1]-w.A[1])*d0[1]
				width = 0.9
				at = clamp(t, g[0]+width/2, g[1]-width/2)
			}
		} else {
			wi, t, ok := nearestWall(h.Walls, m(d.At), 0.6, -1)
			if !ok {
				issues = append(issues, Issue{Kind: "door", Level: "warn", Text: fmt.Sprintf("%s没有落在墙上", label), At: ptr(m(d.At))})
				continue
			}
			w, at = h.Walls[wi], t
			if width == 0 {
				width = 0.8
				if outside {
					width = 0.95
				}
				if kind != "swing" {
					width = 1.5
				}
			}
		}
		if kind == "swing" {
			width = clamp(math.Round(width*20)/20, 0.6, 1.2)
		} else {
			width = clamp(math.Round(width*20)/20, 0.6, 4)
		}
		width = math.Min(width, w.Len()-0.1)
		if width < 0.5 {
			issues = append(issues, Issue{Kind: "door", Level: "warn", Text: fmt.Sprintf("%s所在的墙太短", label), At: ptr(m(d.At))})
			continue
		}
		at = clamp(at, width/2+0.02, w.Len()-width/2-0.02)
		door := home.Door{ID: fmt.Sprintf("d%d", i+1), Name: label, Wall: w.ID, At: round2(at), Width: width, Kind: kind}
		if kind == "swing" {
			door.Hinge = "a"
			door.Side = -1
			c := w.At(at)
			nn := w.Normal()
			if r := h.RoomAt(home.P{c[0] + nn[0]*0.3, c[1] + nn[1]*0.3}); r != nil && r.Name == d.OpensInto {
				door.Side = 1
			} else if r == nil && (d.OpensInto == "" || d.OpensInto == "outside") {
				door.Side = 1
			}
		}
		if outside && !entry {
			door.Entry, entry = true, true
		}
		h.Doors = append(h.Doors, door)
	}

	// windows: openings in the outside walls of their room
	winAt := pick(len(p.Windows), 2.5, func(i int) home.P {
		a, b := m(p.Windows[i].A), m(p.Windows[i].B)
		return home.P{(a[0] + b[0]) / 2, (a[1] + b[1]) / 2}
	}, func(i, wi int, c home.P) (bool, float64) {
		return h.Walls[wi].Exterior && slicesHas(sides(h.Walls[wi], c), p.Windows[i].Room), 0
	})
	for i, wn := range p.Windows {
		if wn.Room == "" {
			continue
		}
		c := winAt[i]
		if c.wall < 0 {
			issues = append(issues, Issue{Kind: "window", Level: "info", Text: fmt.Sprintf("%s的窗在图上没找到开口，没放", wn.Room), Room: wn.Room})
			continue
		}
		w := h.Walls[c.wall]
		g := gaps[c.wall][c.gap]
		width := math.Min(math.Max(0.4, math.Round((g[1]-g[0])*20)/20), w.Len()-0.1)
		at := clamp((g[0]+g[1])/2, width/2+0.02, w.Len()-width/2-0.02)
		sill, height := 0.9, 1.5
		switch p.kindOf(wn.Room) {
		case "bath":
			sill, height = 1.4, 0.8
		case "kitchen":
			sill, height = 1.0, 1.2
		case "balcony":
			sill, height = 1.0, 1.6
		}
		h.Windows = append(h.Windows, home.Window{ID: fmt.Sprintf("n%d", i+1), Wall: w.ID, At: round2(at), Width: width, Sill: sill, Height: height})
	}
	h.Windows = dropOverlaps(h)
	return h, issues
}

// gaps are the openings in a wall's stroke in the image, metres from its A end: where a door
// or a window is drawn. A wall drawn thin all along (a balcony's glazing) is one opening.
func (p *Plan) gaps(w home.Wall) [][2]float64 {
	const step = 0.02
	l := w.Len()
	n := int(l / step)
	if n < 2 {
		return nil
	}
	nn := w.Normal()
	half := w.Thick/2 + 0.12 // the stroke may sit off the line by up to a face
	open := make([]bool, n)
	for i := range n {
		c := w.At((float64(i) + 0.5) * step)
		covered := false
		for o := -half; o <= half && !covered; o += 0.01 {
			q := p.px(home.P{(c[0] + nn[0]*o) * 1000, (c[1] + nn[1]*o) * 1000})
			x, y := int(math.Round(q[0])), int(math.Round(q[1]))
			covered = x >= 0 && y >= 0 && x < p.r.w && y < p.r.h && p.m[y*p.r.w+x]
		}
		open[i] = !covered
	}
	var out [][2]float64
	for i := 0; i < n; {
		if !open[i] {
			i++
			continue
		}
		j := i
		for j < n && open[j] {
			j++
		}
		if float64(j-i)*step >= 0.4 {
			out = append(out, [2]float64{round2(float64(i) * step), round2(math.Min(l, float64(j)*step))})
		}
		i = j
	}
	return out
}

// dropOverlaps leaves out windows that would sit in a door, or in another window.
func dropOverlaps(h home.Home) []home.Window {
	var out []home.Window
	type span struct {
		wall   string
		lo, hi float64
	}
	var taken []span
	for _, d := range h.Doors {
		taken = append(taken, span{d.Wall, d.At - d.Width/2, d.At + d.Width/2})
	}
	for _, w := range h.Windows {
		lo, hi := w.At-w.Width/2, w.At+w.Width/2
		clash := false
		for _, s := range taken {
			if s.wall == w.Wall && lo < s.hi && hi > s.lo {
				clash = true
			}
		}
		if !clash {
			out = append(out, w)
			taken = append(taken, span{w.Wall, lo, hi})
		}
	}
	return out
}

func (p *Plan) kindOf(name string) string {
	for _, r := range p.Rooms {
		if r.Name == name {
			return r.Kind
		}
	}
	return ""
}

// nearestWall is the wall nearest c within reach metres whose length c falls on; dir 0 wants a
// wall across (east–west), 1 one down (north–south), -1 either.
func nearestWall(walls []home.Wall, c home.P, reach float64, dir int) (int, float64, bool) {
	best, bd, bat := -1, math.Inf(1), 0.0
	for i, w := range walls {
		vertical := math.Abs(w.B[0]-w.A[0]) < math.Abs(w.B[1]-w.A[1])
		if (dir == 0 && vertical) || (dir == 1 && !vertical) {
			continue
		}
		d := w.Dir()
		t := (c[0]-w.A[0])*d[0] + (c[1]-w.A[1])*d[1]
		if t < -0.05 || t > w.Len()+0.05 {
			continue
		}
		q := w.At(t)
		dist := math.Hypot(c[0]-q[0], c[1]-q[1])
		if dist < bd {
			best, bd, bat = i, dist, t
		}
	}
	return best, bat, best >= 0 && bd <= reach
}

func doorLabel(d PDoor) string {
	var names []string
	for _, b := range d.Between {
		if b == "outside" {
			return "入户门"
		}
		names = append(names, b)
	}
	// a door is named after the room it leads into from the common space
	for _, n := range names {
		if !strings.Contains(n, "客厅") && !strings.Contains(n, "餐厅") && !strings.Contains(n, "过道") {
			if strings.Contains(n, "阳台") && d.Kind == "sliding" {
				return n + "推拉门"
			}
			return n + "门"
		}
	}
	return strings.Join(names, "–") + "门"
}

func slicesHas(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func clamp(v, lo, hi float64) float64 {
	if hi < lo {
		return (lo + hi) / 2
	}
	return math.Max(lo, math.Min(hi, v))
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

func ptr(p home.P) *home.P { return &p }

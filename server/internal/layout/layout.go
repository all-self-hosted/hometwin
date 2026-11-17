// Package layout is where the furniture stands in a home, and the checks a careful person would
// make before buying it: does it collide, does it leave room to walk, does it block a door or a
// window, does it get through the front door at all.
package layout

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"

	"hometwin/internal/home"
	"hometwin/internal/ikea"
)

// Item is one piece of furniture placed in the home. X and Z are its centre in metres; Rot
// turns it about the vertical, degrees, with its front (local +z) facing south at 0.
type Item struct {
	ID      string       `json:"id"`
	Product ikea.Product `json:"product"`
	X       float64      `json:"x"`
	Z       float64      `json:"z"`
	Rot     float64      `json:"rot"`
}

// Layout is a home's furniture.
type Layout struct {
	Home  string `json:"home"`
	Rev   int    `json:"rev"`
	Items []Item `json:"items"`
}

// Warning is one problem with where something stands.
type Warning struct {
	Item string `json:"item"`
	// With is the other party: an item, a wall, a door or a window id.
	With string `json:"with,omitempty"`
	// Kind: collide, wall, aisle, door, window, outside, entry.
	Kind string `json:"kind"`
	// Level: warn (fix it), info (worth knowing).
	Level string `json:"level"`
	Text  string `json:"text"`
	// At is a point on the floor to show the warning at.
	At *P `json:"at,omitempty"`
}

const (
	// Aisle is the narrowest a walkway should be.
	Aisle = 0.80
	// below MinGap a gap is furniture standing against something, not a way through
	MinGap = 0.30
	// MinFacing is how long two things must face each other for the gap to be a walkway.
	MinFacing = 0.30
	// tolerance before touching counts as running into something
	touch = 0.01
)

// Label names an item for people: "BILLY 书柜", or the name typed in.
func Label(p ikea.Product) string {
	if p.Family != "" && p.TypeLabel != "" {
		return p.Family + " " + p.TypeLabel
	}
	if p.Name != "" {
		return p.Name
	}
	return p.TypeLabel
}

func (it Item) poly() poly {
	return rect(it.X, it.Z, it.Product.W/100, it.Product.D/100, it.Rot)
}

func flat(p ikea.Product) bool { return p.Type == "rug" || p.H < 3 }

// small things (a floor lamp) are moved aside, nobody plans a walkway around them
func small(p ikea.Product) bool { return p.Type == "lamp" }

// tucks says whether one of two pieces is a chair that slides under the other (a table, a desk)
func tucks(a, b ikea.Product) bool {
	under := func(c, t ikea.Product) bool {
		return (c.Type == "chair" || c.Type == "armchair" && c.H < 90) && (t.Type == "table" || t.Type == "desk")
	}
	return under(a, b) || under(b, a)
}

// solid is something fixed in the home that furniture can run into.
type solid struct {
	id, label, kind string // kind: wall, fixture
	p               poly
}

// solids are the walls (minus their door openings) and the fixtures.
func solids(h home.Home) []solid {
	var out []solid
	for _, w := range h.Walls {
		var cuts [][2]float64
		for _, d := range h.Doors {
			if d.Wall == w.ID {
				cuts = append(cuts, [2]float64{d.At - d.Width/2, d.At + d.Width/2})
			}
		}
		sort.Slice(cuts, func(i, j int) bool { return cuts[i][0] < cuts[j][0] })
		t := 0.0
		for _, c := range append(cuts, [2]float64{w.Len(), w.Len()}) {
			if c[0]-t > 0.005 {
				out = append(out, solid{w.ID, "墙", "wall", along(w, t, c[0], -w.Thick/2, w.Thick/2)})
			}
			t = c[1]
		}
	}
	for _, f := range h.Fixtures {
		out = append(out, solid{f.ID, f.Name, "fixture", rect(f.X, f.Z, f.W, f.D, f.Rot)})
	}
	return out
}

func cm(m float64) int { return int(math.Round(m * 100)) }

// Check finds every problem with a layout in a home.
func Check(h home.Home, l Layout) []Warning {
	out := []Warning{}
	ss := solids(h)
	walls := []poly{}
	for _, s := range ss {
		if s.kind == "wall" {
			walls = append(walls, s.p)
		}
	}
	// a gap whose shortest line crosses a wall is two rooms, not a walkway
	throughWall := func(a, b P) bool {
		// shortened a little so a line that only ends on a wall face does not count
		d := mul(sub(b, a), 0.02)
		seg := poly{add(a, d), sub(b, d)}
		for _, w := range walls {
			if meets(seg, w) {
				return true
			}
		}
		return false
	}
	// nor is one that runs through another piece of furniture (chairs across a table)
	polys := make([]poly, len(l.Items))
	for i, it := range l.Items {
		polys[i] = it.poly()
	}
	throughItem := func(a, b P, skip ...int) bool {
		d := mul(sub(b, a), 0.02)
		seg := poly{add(a, d), sub(b, d)}
		for k, it := range l.Items {
			if flat(it.Product) || slices.Contains(skip, k) {
				continue
			}
			if meets(seg, polys[k]) {
				return true
			}
		}
		return false
	}
	at := func(p P) *P { return &p }

	for i, it := range l.Items {
		ip := it.poly()
		c := P{it.X, it.Z}
		if h.RoomAt(c) == nil {
			out = append(out, Warning{Item: it.ID, Kind: "outside", Level: "warn", Text: "放到房间外面了", At: at(c)})
		}

		// walls and fixtures: running into them, and the walkway left beside them
		nearest := map[string]float64{}
		nearAt := map[string]P{}
		for _, s := range ss {
			if flat(it.Product) {
				break
			}
			if d := overlap(ip, s.p); d > touch {
				if s.kind == "wall" {
					out = append(out, Warning{Item: it.ID, With: s.id, Kind: "wall", Level: "warn", Text: fmt.Sprintf("顶进墙里了 %d cm", max(1, cm(d))), At: at(centroid(ip))})
				} else {
					out = append(out, Warning{Item: it.ID, With: s.id, Kind: "collide", Level: "warn", Text: fmt.Sprintf("和%s重叠了 %d cm", s.label, max(1, cm(d))), At: at(centroid(ip))})
				}
				continue
			}
			if small(it.Product) {
				continue
			}
			g, pa, pb := gap(ip, s.p)
			if g < MinGap || g >= Aisle || facing(ip, s.p, pa, pb) < MinFacing || throughWall(pa, pb) || throughItem(pa, pb, i) {
				continue
			}
			key := s.kind + ":" + s.label
			if prev, ok := nearest[key]; !ok || g < prev {
				nearest[key] = g
				nearAt[key] = mul(add(pa, pb), 0.5)
			}
		}
		keys := make([]string, 0, len(nearest))
		for k := range nearest {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			g := nearest[k]
			what := "离墙"
			if !strings.HasPrefix(k, "wall:") {
				what = "离" + strings.TrimPrefix(k, "fixture:")
			}
			out = append(out, Warning{Item: it.ID, Kind: "aisle", Level: "warn", Text: fmt.Sprintf("%s只有 %d cm，过道至少要 80 cm", what, cm(g)), At: at(nearAt[k])})
		}

		// other furniture
		for j0, o := range l.Items[i+1:] {
			j := i + 1 + j0
			if flat(it.Product) || flat(o.Product) {
				continue
			}
			op := o.poly()
			if d := overlap(ip, op); d > touch && !tucks(it.Product, o.Product) {
				out = append(out, Warning{Item: it.ID, With: o.ID, Kind: "collide", Level: "warn", Text: fmt.Sprintf("和 %s 重叠了 %d cm", Label(o.Product), max(1, cm(d))), At: at(mul(add(centroid(ip), centroid(op)), 0.5))})
				continue
			}
			g, pa, pb := gap(ip, op)
			if small(it.Product) || small(o.Product) || overlap(ip, op) > touch {
				continue
			}
			if g >= MinGap && g < Aisle && facing(ip, op, pa, pb) >= MinFacing && !throughWall(pa, pb) && !throughItem(pa, pb, i, j) {
				out = append(out, Warning{Item: it.ID, With: o.ID, Kind: "aisle", Level: "warn", Text: fmt.Sprintf("和 %s 之间只有 %d cm，过道至少要 80 cm", Label(o.Product), cm(g)), At: at(mul(add(pa, pb), 0.5))})
			}
		}

		if !flat(it.Product) {
			out = append(out, doorChecks(h, it, ip)...)
			out = append(out, windowChecks(h, it, ip)...)
		}
		if w := entryCheck(h, it); w != nil {
			out = append(out, *w)
		}
	}
	return out
}

// swingArc is the floor a swing door's leaf sweeps, as a convex fan from its hinge.
func swingArc(w home.Wall, d home.Door) poly {
	t0, t1 := d.At-d.Width/2, d.At+d.Width/2
	hinge, free := t0, t1
	if d.Hinge == "b" {
		hinge, free = t1, t0
	}
	side := float64(d.Side)
	if side == 0 {
		side = 1
	}
	n := mul(w.Normal(), side)
	h := add(w.At(hinge), mul(n, w.Thick/2))
	u := norm(sub(w.At(free), w.At(hinge)))
	out := poly{h}
	for k := 0; k <= 8; k++ {
		a := float64(k) / 8 * math.Pi / 2
		out = append(out, add(h, add(mul(u, d.Width*math.Cos(a)), mul(n, d.Width*math.Sin(a)))))
	}
	return out
}

// SwingArc is exported for the scene's door arcs and the tests.
func SwingArc(h home.Home, d home.Door) []P {
	w, ok := h.Wall(d.Wall)
	if !ok || d.Kind != "swing" {
		return nil
	}
	return swingArc(w, d)
}

func doorChecks(h home.Home, it Item, ip poly) []Warning {
	var out []Warning
	for _, d := range h.Doors {
		w, ok := h.Wall(d.Wall)
		if !ok {
			continue
		}
		name := d.Name
		if name == "" {
			name = "门"
		}
		if d.Kind == "swing" {
			if overlap(ip, swingArc(w, d)) > touch {
				c := w.At(d.At)
				out = append(out, Warning{Item: it.ID, With: d.ID, Kind: "door", Level: "warn", Text: "挡住了" + name + "，门开不全", At: &c})
				continue
			}
		}
		// the way through: the opening itself and a step in front of it on both sides
		t0, t1 := d.At-d.Width/2, d.At+d.Width/2
		pass := along(w, t0, t1, -w.Thick/2-0.3, w.Thick/2+0.3)
		if overlap(ip, pass) > touch {
			c := w.At(d.At)
			out = append(out, Warning{Item: it.ID, With: d.ID, Kind: "door", Level: "warn", Text: "堵住了" + name + "的进出", At: &c})
		}
	}
	return out
}

func windowChecks(h home.Home, it Item, ip poly) []Warning {
	var out []Warning
	top := it.Product.H / 100
	for _, wn := range h.Windows {
		if top <= wn.Sill+0.02 {
			continue
		}
		w, ok := h.Wall(wn.Wall)
		if !ok {
			continue
		}
		t0, t1 := wn.At-wn.Width/2, wn.At+wn.Width/2
		for _, side := range []float64{1, -1} {
			o0, o1 := w.Thick/2, w.Thick/2+0.25
			if side < 0 {
				o0, o1 = -o1, -o0
			}
			front := along(w, t0, t1, o0, o1)
			if overlap(ip, front) <= touch {
				continue
			}
			room := "这扇"
			if r := h.RoomAt(centroid(front)); r != nil {
				room = r.Name + "的"
			}
			c := w.At(wn.At)
			out = append(out, Warning{Item: it.ID, With: wn.ID, Kind: "window", Level: "warn",
				Text: fmt.Sprintf("挡住了%s窗户：窗台 %d cm 高，它有 %d cm", room, cm(wn.Sill), int(math.Round(it.Product.H))), At: &c})
			break
		}
	}
	return out
}

// fits says whether a box of these sizes (cm) can be carried through an opening w×h (m).
func fits(a, b, c, w, h float64) bool {
	s := []float64{a, b, c}
	sort.Float64s(s)
	return s[0]/100 <= w && s[1]/100 <= h
}

func entryCheck(h home.Home, it Item) *Warning {
	d, ok := h.EntryDoor()
	if !ok {
		return nil
	}
	p := it.Product
	clearW := d.Width - 0.06
	hgt := d.Height
	if hgt == 0 {
		hgt = 2.1
	}
	clearH := hgt - 0.05
	door := fmt.Sprintf("（%s净宽约 %d cm）", d.Name, cm(clearW))
	for i, pk := range p.Packages {
		if !fits(pk.W, pk.H, pk.L, clearW, clearH) {
			return &Warning{Item: it.ID, With: d.ID, Kind: "entry", Level: "warn",
				Text: fmt.Sprintf("第 %d 个包装 %.0f×%.0f×%.0f cm 过不了%s%s", i+1, pk.W, pk.H, pk.L, d.Name, door)}
		}
	}
	if fits(p.W, p.D, p.H, clearW, clearH) {
		return nil
	}
	if len(p.Packages) > 0 {
		return &Warning{Item: it.ID, With: d.ID, Kind: "entry", Level: "info",
			Text: fmt.Sprintf("组装好后过不了%s%s，包装能进，要在屋里组装", d.Name, door)}
	}
	return &Warning{Item: it.ID, With: d.ID, Kind: "entry", Level: "warn",
		Text: fmt.Sprintf("整件 %.0f×%.0f×%.0f cm 过不了%s%s", p.W, p.D, p.H, d.Name, door)}
}

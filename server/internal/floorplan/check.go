package floorplan

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"hometwin/internal/home"
)

// Issue is something about a draft a person (or the fixer) should look at.
type Issue struct {
	Kind  string  `json:"kind"`  // home, area, overlap, gap, door, window
	Level string  `json:"level"` // warn, info
	Text  string  `json:"text"`
	Room  string  `json:"room,omitempty"`
	At    *home.P `json:"at,omitempty"` // metres
}

// RoomArea compares a room's area with what the plan prints.
type RoomArea struct {
	Name     string  `json:"name"`
	Kind     string  `json:"kind"`
	Printed  float64 `json:"printed"`  // m², 0 when none is printed
	Computed float64 `json:"computed"` // m², by the convention the plan uses
	Diff     float64 `json:"diff"`     // computed/printed − 1
}

// Frame maps the home's metres onto the picture: a metre position is interpolated between
// the marks ([metres, pixels]) and carried on past them at the same scale. Pixels are in the
// uploaded picture; Crop is the part of it the drawing takes up.
type Frame struct {
	Crop [4]int       `json:"crop"` // x0, y0, x1, y1
	X    [][2]float64 `json:"x"`
	Z    [][2]float64 `json:"z"`
	PxM  [2]float64   `json:"pxPerM"`
	Size [2]int       `json:"size"` // the uploaded picture's width and height
}

// Draft is a plan made into a home, with how it measures up.
type Draft struct {
	Home       home.Home  `json:"home"`
	Areas      []RoomArea `json:"areas"`
	Issues     []Issue    `json:"issues"`
	Convention string     `json:"convention"` // which area the printed numbers are: 轴线面积 or 套内净面积
	Frame      Frame      `json:"frame"`
}

// tolerance is how far a computed room area may be from the printed one before it is an issue.
const tolerance = 0.08

// Draft builds the home and checks it.
func (p *Plan) Draft(id, name string) Draft {
	h, issues := p.Home(id, name)
	d := Draft{Home: h}
	for _, e := range h.Check() {
		issues = append(issues, Issue{Kind: "home", Level: "warn", Text: e})
	}
	d.Areas, d.Convention = areas(h, p.Rooms)
	for _, a := range d.Areas {
		if a.Printed > 0 && math.Abs(a.Diff) > tolerance {
			issues = append(issues, Issue{Kind: "area", Level: "warn", Room: a.Name,
				Text: fmt.Sprintf("%s算出 %.1f m²，图上写 %.1f m²（差 %+.0f%%）", a.Name, a.Computed, a.Printed, a.Diff*100)})
		}
	}
	issues = append(issues, overlaps(h)...)
	issues = append(issues, p.doorChecks(h)...)
	if _, ok := h.EntryDoor(); !ok {
		issues = append(issues, Issue{Kind: "door", Level: "info", Text: "没有找到入户门"})
	}
	d.Issues = issues
	d.Frame = p.frame()
	return d
}

// areas works out each room's area both on the walls' centre lines and inside the walls, and
// reports the one the printed numbers agree with better (plans differ in which they print).
func areas(h home.Home, rooms []PRoom) ([]RoomArea, string) {
	centre := make([]float64, len(h.Rooms))
	net := make([]float64, len(h.Rooms))
	for i, r := range h.Rooms {
		centre[i] = home.Area(r.Poly)
		net[i] = centre[i] - wallShare(h, r.Poly)
	}
	errOf := func(vals []float64) float64 {
		var es []float64
		for i, r := range rooms {
			if i < len(vals) && r.Printed > 0 {
				es = append(es, math.Abs(vals[i]/r.Printed-1))
			}
		}
		if len(es) == 0 {
			return 0
		}
		sort.Float64s(es)
		return es[len(es)/2]
	}
	vals, conv := centre, "轴线面积"
	if errOf(net) < errOf(centre) {
		vals, conv = net, "套内净面积"
	}
	out := make([]RoomArea, len(h.Rooms))
	for i, r := range h.Rooms {
		a := RoomArea{Name: r.Name, Kind: r.Kind, Computed: math.Round(vals[i]*100) / 100}
		if i < len(rooms) {
			a.Printed = rooms[i].Printed
		}
		if a.Printed > 0 {
			a.Diff = math.Round((a.Computed/a.Printed-1)*1000) / 1000
		}
		out[i] = a
	}
	return out, conv
}

// wallShare is the floor of a room its walls stand on: half of each wall along its edges.
func wallShare(h home.Home, poly []home.P) float64 {
	s := 0.0
	for i := range poly {
		a, b := poly[i], poly[(i+1)%len(poly)]
		for _, w := range h.Walls {
			s += overlapLen(a, b, w.A, w.B) * w.Thick / 2
		}
	}
	return s
}

// overlapLen is how long two straight-across-or-down segments run together.
func overlapLen(a, b, c, d home.P) float64 {
	const eps = 1e-6
	switch {
	case math.Abs(a[0]-b[0]) < eps && math.Abs(c[0]-d[0]) < eps && math.Abs(a[0]-c[0]) < eps:
		return math.Max(0, math.Min(math.Max(a[1], b[1]), math.Max(c[1], d[1]))-math.Max(math.Min(a[1], b[1]), math.Min(c[1], d[1])))
	case math.Abs(a[1]-b[1]) < eps && math.Abs(c[1]-d[1]) < eps && math.Abs(a[1]-c[1]) < eps:
		return math.Max(0, math.Min(math.Max(a[0], b[0]), math.Max(c[0], d[0]))-math.Max(math.Min(a[0], b[0]), math.Min(c[0], d[0])))
	}
	return 0
}

// overlaps finds rooms that cover the same floor, and floor no room covers that is closed in
// by rooms, on a 5 cm grid.
func overlaps(h home.Home) []Issue {
	const cell = 0.05
	lo, hi := home.P{math.Inf(1), math.Inf(1)}, home.P{math.Inf(-1), math.Inf(-1)}
	for _, r := range h.Rooms {
		for _, v := range r.Poly {
			lo = home.P{math.Min(lo[0], v[0]), math.Min(lo[1], v[1])}
			hi = home.P{math.Max(hi[0], v[0]), math.Max(hi[1], v[1])}
		}
	}
	if math.IsInf(lo[0], 1) {
		return nil
	}
	nx, nz := int((hi[0]-lo[0])/cell)+2, int((hi[1]-lo[1])/cell)+2
	if nx*nz > 4e6 {
		return nil
	}
	owner := make([]int, nx*nz) // room index + 1, or 0
	pair := map[[2]int]int{}
	pairAt := map[[2]int]home.P{}
	for i := range nz {
		for j := range nx {
			c := home.P{lo[0] + (float64(j)+0.5)*cell - cell, lo[1] + (float64(i)+0.5)*cell - cell}
			for ri, r := range h.Rooms {
				if !home.Inside(r.Poly, c) {
					continue
				}
				if o := owner[i*nx+j]; o > 0 {
					k := [2]int{o - 1, ri}
					pair[k]++
					pairAt[k] = c
				} else {
					owner[i*nx+j] = ri + 1
				}
			}
		}
	}
	var out []Issue
	for k, n := range pair {
		if a := float64(n) * cell * cell; a >= 0.2 {
			at := pairAt[k]
			out = append(out, Issue{Kind: "overlap", Level: "warn", Room: h.Rooms[k[0]].Name, At: &at,
				Text: fmt.Sprintf("%s和%s重叠了 %.1f m²", h.Rooms[k[0]].Name, h.Rooms[k[1]].Name, a)})
		}
	}
	// holes: empty cells the outside cannot reach
	seen := make([]bool, nx*nz)
	var stack []int
	for i := range nz {
		for j := range nx {
			if (i == 0 || j == 0 || i == nz-1 || j == nx-1) && owner[i*nx+j] == 0 {
				seen[i*nx+j] = true
				stack = append(stack, i*nx+j)
			}
		}
	}
	flood := func(stack []int, mark []bool) int {
		n := 0
		for len(stack) > 0 {
			c := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			n++
			i, j := c/nx, c%nx
			for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
				ii, jj := i+d[0], j+d[1]
				if ii < 0 || jj < 0 || ii >= nz || jj >= nx {
					continue
				}
				q := ii*nx + jj
				if !mark[q] && owner[q] == 0 {
					mark[q] = true
					stack = append(stack, q)
				}
			}
		}
		return n
	}
	flood(stack, seen)
	for c := range owner {
		if owner[c] != 0 || seen[c] {
			continue
		}
		seen[c] = true
		n := flood([]int{c}, seen)
		if a := float64(n) * cell * cell; a >= 0.3 {
			at := home.P{lo[0] + float64(c%nx)*cell, lo[1] + float64(c/nx)*cell}
			out = append(out, Issue{Kind: "gap", Level: "warn", At: &at,
				Text: fmt.Sprintf("(%.1f, %.1f) 附近有 %.1f m² 不属于任何房间", at[0], at[1], a)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Text < out[j].Text })
	return out
}

// doorChecks says where a door does not join the rooms the plan shows it between.
func (p *Plan) doorChecks(h home.Home) []Issue {
	var out []Issue
	for _, d := range h.Doors {
		w, _ := h.Wall(d.Wall)
		c, n := w.At(d.At), w.Normal()
		name := func(s float64) string {
			if r := h.RoomAt(home.P{c[0] + n[0]*s, c[1] + n[1]*s}); r != nil {
				return r.Name
			}
			return "outside"
		}
		got := []string{name(0.3), name(-0.3)}
		var want []string
		var i int
		if _, err := fmt.Sscanf(d.ID, "d%d", &i); err == nil && i >= 1 && i <= len(p.Doors) {
			want = p.Doors[i-1].Between
		}
		if len(want) == 2 && !sameSet(got, want) {
			out = append(out, Issue{Kind: "door", Level: "warn", At: &c,
				Text: fmt.Sprintf("%s应该连着%s，现在连着%s", d.Name, strings.Join(nice(want), "和"), strings.Join(nice(got), "和"))})
		}
	}
	return out
}

func nice(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		if n == "outside" {
			n = "户外"
		}
		out[i] = n
	}
	return out
}

func sameSet(a, b []string) bool {
	return (a[0] == b[0] && a[1] == b[1]) || (a[0] == b[1] && a[1] == b[0])
}

// frame is how to draw the home on the picture the plan came from.
func (p *Plan) frame() Frame {
	f := Frame{PxM: [2]float64{1000 / p.x.mmPerPx, 1000 / p.z.mmPerPx}}
	for _, l := range p.x.lines {
		f.X = append(f.X, [2]float64{round3((l.mm - p.shift[0]) / 1000), l.px})
	}
	for _, l := range p.z.lines {
		f.Z = append(f.Z, [2]float64{round3((l.mm - p.shift[1]) / 1000), l.px})
	}
	return f
}

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

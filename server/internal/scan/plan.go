package scan

import (
	"errors"
	"fmt"
	"image"
	"math"

	"hometwin/internal/home"
)

// Options for Plan.
type Options struct {
	ID, Name string
	// Area is the deed's floor area (建筑面积) when known; else the rooms' sum.
	Area float64
	// Hints name the rooms; see RoomHint. Rooms no hint names are called 房间 1, 2, ….
	Hints []RoomHint
	// Textures, by chunk, let the floors' finish (wood or tile) be told; nil entries or
	// none at all leave it to the room kinds.
	Textures []image.Image
	// Windows are where the scan says windows are (WindowsFromScan). A window the walls'
	// openings did not show (glass and curtains read as wall) is added at each.
	Windows []WindowHint
	// Reference is the floor plan, when one was read too.
	Reference *Reference
}

// Result is a scan read as a home, with what the home schema has no place for: each room's
// measured heights and areas, and the frame. Home.Levels holds the platforms and steps;
// Room.Y the rooms whose whole floor sits higher or lower than the main floor.
type Result struct {
	Home  home.Home `json:"home"`
	Frame Frame     `json:"frame"`
	// Rooms carry each room's measured floor height and ceiling, by room ID.
	Rooms []RoomLevel `json:"rooms"`
	// Levels are the raised parts within rooms, platforms and their steps, with areas.
	Levels []Level `json:"levels,omitempty"`
	// Notes say what the import is unsure of, in Chinese for the UI.
	Notes []string `json:"notes,omitempty"`
}

type RoomLevel struct {
	Room    string  `json:"room"`
	Floor   float64 `json:"floor"`   // m above the main floor
	Ceiling float64 `json:"ceiling"` // m above the main floor
	Area    float64 `json:"area"`    // m², clear floor inside the walls
	// Gross is the area out to the middle of the walls, as floor plans state it.
	Gross float64 `json:"gross"`
	// Colour is the floor's mean texture colour (#rrggbb), when textures were given.
	Colour string `json:"colour,omitempty"`
	// Printed is the area the floor plan prints for the room; Added says the room comes
	// from the plan alone (nobody scanned it).
	Printed float64 `json:"printed,omitempty"`
	Added   bool    `json:"added,omitempty"`
}

// Level is part of a room's floor at another height: a platform (地台) or a step up to it.
type Level struct {
	ID   string   `json:"id"`
	Room string   `json:"room"`
	Kind string   `json:"kind"` // platform, step
	Y    float64  `json:"y"`    // top, m above the main floor
	Poly []home.P `json:"poly"`
	Area float64  `json:"area"`
	// Floor is wood or tile, from the texture; empty when unknown.
	Floor  string `json:"floor,omitempty"`
	Colour string `json:"colour,omitempty"`
}

// Plan reads walls, doors, windows, rooms and floor levels from a scan in frame f.
func Plan(m Mesh, f Frame, opt Options) (Result, error) {
	ts := worldTris(m, f)
	if len(ts) == 0 {
		return Result{}, errors.New("扫描是空的")
	}
	res := Result{Frame: f}
	g := newGrid(ts, cellSize, 0.3)
	fm := newFloorMap(ts, newGrid(ts, 0.1, 0.3))

	var seeds []seed
	for _, h := range opt.Hints {
		for _, p := range hintPoints(h) {
			seeds = append(seeds, seed{P: p, Room: h.Name})
		}
	}
	regs := findRooms(ts, fm, g, seeds)
	if len(regs) == 0 {
		return Result{}, errors.New("扫描里找不到封闭的房间")
	}
	names := assignNames(g, regs, opt.Hints)
	regs, names = mergeHalls(g, regs, names)

	h := home.Home{ID: opt.ID, Name: opt.Name, Area: opt.Area, WallHeight: round2(f.Ceiling), CutHeight: 1.2}
	if h.ID == "" {
		h.ID = "scan"
	}
	if h.Name == "" {
		h.Name = "扫描导入"
	}
	ids := map[string]int{}
	unnamed := 0
	kept := regs[:0]
	for i, r := range regs {
		n := names[i]
		if n.Name == "" {
			switch {
			case narrow(r.poly):
				n.Name, n.Kind = "过道", "entry"
			case polyArea(r.poly) < minRoom:
				continue // a sliver between rooms, not a room
			default:
				unnamed++
				n.Name, n.Kind = fmt.Sprintf("房间 %d", unnamed), "bedroom"
			}
		}
		kept = append(kept, r)
		ids[n.Kind]++
		id := n.Kind
		if ids[n.Kind] > 1 {
			id = fmt.Sprintf("%s-%d", n.Kind, ids[n.Kind])
		}
		h.Rooms = append(h.Rooms, home.Room{ID: id, Name: n.Name, Kind: n.Kind, Poly: r.poly})
	}
	regs = kept
	// IDs of a kind that appears more than once all get a number.
	for i := range h.Rooms {
		if k := h.Rooms[i].Kind; ids[k] > 1 && h.Rooms[i].ID == k {
			h.Rooms[i].ID = k + "-1"
		}
	}
	res.Rooms, res.Levels = levels(ts, g, regs, h.Rooms, opt.Textures)
	for i, rl := range res.Rooms {
		h.Rooms[i].Floor = finishOf(rl.Colour)
		// A whole room higher or lower than the main floor (2 cm and less is scan noise).
		if math.Abs(rl.Floor) >= 0.03 {
			h.Rooms[i].Y = rl.Floor
		}
	}

	// The floor plan, when there is one, fills in what the scan could not see.
	var off home.P
	ref := opt.Reference
	if ref != nil {
		var n int
		if off, n = ref.offset(h.Rooms); n < 2 {
			res.Notes = append(res.Notes, "户型图和扫描对不上（同名的房间不到两个），没用户型图补全")
			ref = nil
		}
	}
	if ref != nil {
		res.Notes = append(res.Notes, ref.widen(h.Rooms, off)...)
		for _, r := range ref.missingRooms(h.Rooms, off) {
			ids[r.Kind]++
			r.ID = fmt.Sprintf("%s-%d", r.Kind, ids[r.Kind])
			h.Rooms = append(h.Rooms, r)
			res.Rooms = append(res.Rooms, RoomLevel{Room: r.ID, Ceiling: round2(f.Ceiling), Added: true})
			res.Notes = append(res.Notes, fmt.Sprintf("%s没有扫到，按户型图补上", r.Name))
		}
	}
	named := map[string]bool{}
	for _, r := range h.Rooms {
		named[r.Name] = true
	}
	for _, hint := range opt.Hints {
		if !named[hint.Name] {
			named[hint.Name] = true
			res.Notes = append(res.Notes, fmt.Sprintf("没在扫描里找到%s（可能没扫到）", hint.Name))
		}
	}

	h.Walls = extendEnds(walls(h.Rooms))
	unscanned := map[string]bool{}
	for _, rl := range res.Rooms {
		if rl.Added {
			unscanned[rl.Room] = true
		}
	}
	h.Doors, h.Windows = openings(ts, fm, h.Rooms, h.Walls, unscanned)
	h.Windows = addHintedWindows(h, opt.Windows)
	if ref != nil {
		ref.windows(&h, off)
		res.Notes = append(res.Notes, ref.doors(&h, off)...)
	}
	for i := range res.Rooms {
		res.Rooms[i].Area = round2(home.Area(h.Rooms[i].Poly))
		res.Rooms[i].Gross = round2(grossArea(h.Rooms[i].Poly, h.Walls))
		if ref != nil {
			res.Rooms[i].Printed = ref.Printed[h.Rooms[i].Name]
		}
	}
	for _, l := range res.Levels {
		h.Levels = append(h.Levels, home.Level{ID: l.ID, Room: l.Room, Kind: l.Kind, Y: l.Y, Poly: l.Poly, Floor: l.Floor})
	}
	sum := 0.0
	for _, r := range h.Rooms {
		sum += home.Area(r.Poly)
	}
	if h.Area == 0 {
		h.Area = math.Round(sum*10) / 10
	}
	res.Home = h
	if !hasEntry(h) {
		res.Notes = append(res.Notes, "没找到入户门（扫描时门多半关着），请在编辑里补上")
	}
	return res, nil
}

// narrow says a room is a passage: less than 1.4 m across its bounding box one way.
func narrow(p []home.P) bool {
	x0, z0, x1, z1 := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, q := range p {
		x0, x1 = math.Min(x0, q[0]), math.Max(x1, q[0])
		z0, z1 = math.Min(z0, q[1]), math.Max(z1, q[1])
	}
	return math.Min(x1-x0, z1-z0) < 1.4
}

func hasEntry(h home.Home) bool {
	_, ok := h.EntryDoor()
	return ok
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

// levels measures each room's floor and ceiling and finds the parts of its floor at other
// heights. Heights come from the area-weighted histogram of the upward faces in the room.
func levels(ts []tri, g grid, regs []region, rooms []home.Room, tex []image.Image) ([]RoomLevel, []Level) {
	var rls []RoomLevel
	var lvs []Level
	for i, r := range regs {
		var ys, ws, cys, cws []float64
		var mine []tri
		for _, t := range ts {
			c := t.centre()
			k, ok := g.at(c[0], c[2])
			if !ok || !r.cells[k] {
				continue
			}
			switch {
			case upFacing(t) && c[1] >= floorLo && c[1] <= floorHi:
				ys, ws = append(ys, c[1]), append(ws, t.area)
				mine = append(mine, t)
			case t.n[1] < -0.95 && c[1] > 1.9:
				cys, cws = append(cys, c[1]), append(cws, t.area)
			}
		}
		rl := RoomLevel{Room: rooms[i].ID, Area: math.Round(home.Area(rooms[i].Poly)*100) / 100}
		pk := heightPeaks(ys, ws, floorLo, floorHi, 0.08, 0.05)
		if len(pk) > 0 {
			rl.Floor = round2(pk[0].Y)
			rl.Colour = meanColour(mine, tex, func(t tri) bool { return math.Abs(t.centre()[1]-pk[0].Y) <= 0.03 })
		}
		if cp := heightPeaks(cys, cws, 1.9, 3.5, 0.2, 0.05); len(cp) > 0 {
			rl.Ceiling = round2(cp[0].Y)
		}
		rls = append(rls, rl)
		if len(pk) < 2 {
			continue
		}
		// Other heights above the floor: platforms first, then steps up to them.
		floor := pk[0].Y
		lower := make([]bool, g.n()) // where the room's own floor is seen
		for _, t := range mine {
			if math.Abs(t.centre()[1]-floor) <= 0.03 {
				eachCell(g, t, func(k int) { lower[k] = true })
			}
		}
		type piece struct {
			lv    Level
			cells []bool
		}
		var plats, steps []piece
		for _, p := range pk[1:] {
			if p.Y-floor < 0.06 {
				continue
			}
			m := make([]bool, g.n())
			for _, t := range mine {
				if math.Abs(t.centre()[1]-p.Y) <= 0.045 {
					eachCell(g, t, func(k int) {
						if r.cells[k] {
							m[k] = true
						}
					})
				}
			}
			// Close the scan's cracks, then trace each piece.
			m = g.erode(g.dilate(m, 4), 4)
			lab, sizes := g.label(m)
			for l := 1; l < len(sizes); l++ {
				area := float64(sizes[l]) * g.cell * g.cell
				if area < 0.08 {
					continue
				}
				cells := make([]bool, len(lab))
				x0, z0, x1, z1 := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
				for k, v := range lab {
					if v == int32(l) {
						cells[k] = true
						c := g.centre(k)
						x0, x1, z0, z1 = math.Min(x0, c[0]), math.Max(x1, c[0]), math.Min(z0, c[1]), math.Max(z1, c[1])
					}
				}
				lv := Level{Room: rooms[i].ID, Y: round2(p.Y)}
				py := p.Y
				lv.Colour = meanColour(mine, tex, func(t tri) bool {
					c := t.centre()
					k, ok := g.at(c[0], c[2])
					return ok && cells[k] && math.Abs(c[1]-py) <= 0.045
				})
				lv.Floor = finishOf(lv.Colour)
				// A platform is big and solid; sofa seats and beds are ragged next to it.
				solid := area / ((x1 - x0 + g.cell) * (z1 - z0 + g.cell))
				if area >= 1.2 && solid >= 0.5 {
					// Furniture on a platform hides bits of it: fill notches up to 0.5 m
					// where the room's lower floor is not seen, and square it up.
					// The walls count as platform here so notches against them fill too.
					withWalls := make([]bool, len(cells))
					for k := range cells {
						withWalls[k] = cells[k] || !r.cells[k]
					}
					closed := g.erode(g.dilate(withWalls, 20), 20)
					near := g.dilate(cells, 24)
					for k := range cells {
						cells[k] = cells[k] || (closed[k] && near[k] && r.cells[k] && !lower[k])
					}
					cells = g.largest(cells)
					lv.Kind = "platform"
					lv.Poly = squarePoly(g.outline(cells), 0.06, 0.25)
					plats = append(plats, piece{lv, cells})
				} else {
					// Steps are narrow strips the scan covers thinly: take their box.
					h := g.cell / 2
					lv.Kind = "step"
					lv.Poly = []home.P{{round3(x0 - h), round3(z0 - h)}, {round3(x1 + h), round3(z0 - h)}, {round3(x1 + h), round3(z1 + h)}, {round3(x0 - h), round3(z1 + h)}}
					steps = append(steps, piece{lv, cells})
				}
				if len(lv.Poly) < 3 {
					continue
				}
				lv.Area = math.Round(home.Area(lv.Poly)*100) / 100
				if lv.Kind == "platform" {
					plats[len(plats)-1].lv = lv
				} else {
					steps[len(steps)-1].lv = lv
				}
			}
		}
		var found []Level
		for _, p := range plats {
			found = append(found, p.lv)
		}
		for _, s := range steps {
			// A step lies between the floor and a platform it touches.
			for _, p := range plats {
				if s.lv.Y >= p.lv.Y-0.04 {
					continue
				}
				near := g.dilate(p.cells, 6)
				touch := false
				for k, c := range s.cells {
					touch = touch || (c && near[k])
				}
				if touch {
					found = append(found, s.lv)
					break
				}
			}
		}
		for j := range found {
			found[j].ID = fmt.Sprintf("%s-%s-%d", rooms[i].ID, found[j].Kind, j+1)
		}
		lvs = append(lvs, found...)
	}
	return rls, lvs
}

// grossArea is a room's area out to the middle of its walls: each edge pushed out by half
// the thickness of the wall along it (corners left out, a few hundredths of a m²).
func grossArea(poly []home.P, ws []home.Wall) float64 {
	a := home.Area(poly)
	for i := range poly {
		p, q := poly[i], poly[(i+1)%len(poly)]
		l := math.Hypot(q[0]-p[0], q[1]-p[1])
		if l < 1e-6 {
			continue
		}
		mid := home.P{(p[0] + q[0]) / 2, (p[1] + q[1]) / 2}
		d := home.P{(q[0] - p[0]) / l, (q[1] - p[1]) / l}
		best := 0.0
		for _, w := range ws {
			wd := w.Dir()
			if math.Abs(wd[0]*d[1]-wd[1]*d[0]) > 0.05 {
				continue
			}
			// Distance from the edge's middle to the wall's centre line, and along it.
			v := home.P{mid[0] - w.A[0], mid[1] - w.A[1]}
			across := math.Abs(v[0]*wd[1] - v[1]*wd[0])
			along := v[0]*wd[0] + v[1]*wd[1]
			if along < 0 || along > w.Len() || math.Abs(across-w.Thick/2) > 0.04 {
				continue
			}
			best = math.Max(best, w.Thick/2)
		}
		a += l * best
	}
	return a
}

// meanColour is the area-weighted mean texture colour of the triangles that pass keep.
func meanColour(ts []tri, tex []image.Image, keep func(tri) bool) string {
	var r, g, b, w float64
	for _, t := range ts {
		if t.chunk >= len(tex) || tex[t.chunk] == nil || !keep(t) {
			continue
		}
		im := tex[t.chunk]
		bd := im.Bounds()
		u, v := float64(t.uv[0]), float64(t.uv[1])
		x := bd.Min.X + int(math.Max(0, math.Min(0.999, u))*float64(bd.Dx()))
		y := bd.Min.Y + int(math.Max(0, math.Min(0.999, 1-v))*float64(bd.Dy()))
		cr, cg, cb, _ := im.At(x, y).RGBA()
		r += float64(cr>>8) * t.area
		g += float64(cg>>8) * t.area
		b += float64(cb>>8) * t.area
		w += t.area
	}
	if w == 0 {
		return ""
	}
	return fmt.Sprintf("#%02x%02x%02x", int(r/w), int(g/w), int(b/w))
}

// finishOf tells wood from tile by colour: timber is a warm, saturated brown or orange;
// tiles, stone and terrazzo are pale or grey.
func finishOf(hex string) string {
	var r, g, b int
	if _, err := fmt.Sscanf(hex, "#%02x%02x%02x", &r, &g, &b); err != nil {
		return ""
	}
	mx := max(r, g, b)
	mn := min(r, g, b)
	if mx == 0 {
		return ""
	}
	sat := float64(mx-mn) / float64(mx)
	warm := r >= g && g >= b
	// Pale warm tiles (bathrooms) are brighter than any timber seen so far.
	if warm && sat >= 0.25 && mx <= 190 {
		return "wood"
	}
	return "tile"
}

// Window size where only the place is known: a common Chinese casement.
const hintWidth, hintSill, hintHeight = 1.2, 0.9, 1.5

// addHintedWindows puts a window on the outer wall nearest each hint, unless one is there.
func addHintedWindows(h home.Home, hints []WindowHint) []home.Window {
	out := h.Windows
	for _, hw := range hints {
		best, bd, bat := -1, 0.6, 0.0
		for i, w := range h.Walls {
			if !w.Exterior || w.Len() < 0.5 {
				continue
			}
			d, n := w.Dir(), w.Normal()
			v := home.P{hw.At[0] - w.A[0], hw.At[1] - w.A[1]}
			along := v[0]*d[0] + v[1]*d[1]
			across := math.Abs(v[0]*n[0] + v[1]*n[1])
			if along < 0 || along > w.Len() || across > bd {
				continue
			}
			best, bd, bat = i, across, along
		}
		if best < 0 {
			continue
		}
		w := h.Walls[best]
		dup := false
		for _, o := range out {
			dup = dup || (o.Wall == w.ID && math.Abs(o.At-bat) < o.Width/2+0.3)
		}
		for _, d := range h.Doors {
			dup = dup || (d.Wall == w.ID && math.Abs(d.At-bat) < d.Width/2+0.3)
		}
		if dup {
			continue
		}
		at, width := fit(bat, math.Min(hintWidth, w.Len()-0.1), w.Len())
		out = append(out, home.Window{ID: fmt.Sprintf("win%d", len(out)+1), Wall: w.ID, At: round3(at), Width: round3(width), Sill: hintSill, Height: hintHeight})
	}
	return out
}

// hintPoints are the points a hint vouches for: its point, or for an outline the points of
// a 0.5 m lattice at least 0.6 m inside it (an outline read off a drawing is not exact).
func hintPoints(h RoomHint) []home.P {
	if h.At != nil {
		return []home.P{*h.At}
	}
	if len(h.Poly) < 3 {
		return nil
	}
	x0, z0, x1, z1 := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, q := range h.Poly {
		x0, x1 = math.Min(x0, q[0]), math.Max(x1, q[0])
		z0, z1 = math.Min(z0, q[1]), math.Max(z1, q[1])
	}
	var out []home.P
	for x := x0 + 0.25; x < x1; x += 0.5 {
		for z := z0 + 0.25; z < z1; z += 0.5 {
			p := home.P{x, z}
			if !home.Inside(h.Poly, p) {
				continue
			}
			ok := true
			for i := range h.Poly {
				if segDist(p, h.Poly[i], h.Poly[(i+1)%len(h.Poly)]) < 0.6 {
					ok = false
					break
				}
			}
			if ok {
				out = append(out, p)
			}
		}
	}
	if len(out) == 0 {
		cx, cz := centroid(h.Poly)
		out = append(out, home.P{cx, cz})
	}
	return out
}

// mergeHalls joins a passage nobody named (a hall in front of the kitchen, say) to the
// living room it opens onto, as floor plans count it: the two and the opening between them
// become one room.
func mergeHalls(g grid, regs []region, names []RoomHint) ([]region, []RoomHint) {
	const reach = 20 // cells, 0.5 m
	for i := 0; i < len(regs); i++ {
		if names[i].Name != "" || !narrow(regs[i].poly) {
			continue
		}
		near := g.dilate(regs[i].cells, reach)
		for j := range regs {
			if j == i || names[j].Kind != "living" {
				continue
			}
			touch := false
			for k, c := range regs[j].cells {
				if c && near[k] {
					touch = true
					break
				}
			}
			if !touch {
				continue
			}
			union := make([]bool, len(near))
			for k := range union {
				union[k] = regs[i].cells[k] || regs[j].cells[k]
			}
			closed := g.erode(g.dilate(union, 12), 12)
			n := 0
			for k := range union {
				union[k] = union[k] || (closed[k] && near[k])
				if union[k] {
					n++
				}
			}
			regs[j].cells = union
			regs[j].area = float64(n) * g.cell * g.cell
			regs[j].poly = squarePoly(g.outline(union), 0.06, 0.25)
			regs = append(regs[:i], regs[i+1:]...)
			names = append(names[:i], names[i+1:]...)
			i--
			break
		}
	}
	return regs, names
}

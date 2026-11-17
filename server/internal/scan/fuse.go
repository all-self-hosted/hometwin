package scan

import (
	"fmt"
	"math"
	"sort"

	"hometwin/internal/home"
)

// Reference is the same home read from its floor plan (floorplan.Draft): north up, metres
// from the plan's north-west corner, rooms to the middle of the walls. The scan measures
// better; the plan knows what the scan cannot see: rooms nobody walked into, doors shut
// during the scan, the extent of rooms behind full-height wardrobes, window sizes.
type Reference struct {
	Home home.Home `json:"home"`
	// Printed are the areas the plan prints, by room name.
	Printed map[string]float64 `json:"printed,omitempty"`
}

// offset is where the reference's origin lies in the scan's frame: the median shift between
// the centres of the rooms both name.
func (ref Reference) offset(rooms []home.Room) (home.P, int) {
	var dx, dz []float64
	for _, rr := range ref.Home.Rooms {
		for _, r := range rooms {
			if r.Name == rr.Name {
				ax, az := centroid(r.Poly)
				bx, bz := centroid(rr.Poly)
				dx, dz = append(dx, ax-bx), append(dz, az-bz)
			}
		}
	}
	if len(dx) == 0 {
		return home.P{}, 0
	}
	return home.P{median(dx), median(dz)}, len(dx)
}

func shift(poly []home.P, d home.P) []home.P {
	out := make([]home.P, len(poly))
	for i, p := range poly {
		out[i] = home.P{round3(p[0] + d[0]), round3(p[1] + d[1])}
	}
	return out
}

// widen moves room edges that stop well short of where the plan has the room's wall (a
// wardrobe up to the ceiling reads as wall in the scan) out to that wall, leaving a thin
// wall, as long as no other room is in the way.
func (ref Reference) widen(rooms []home.Room, d home.P) []string {
	var notes []string
	for i := range rooms {
		var rr *home.Room
		for k := range ref.Home.Rooms {
			if ref.Home.Rooms[k].Name == rooms[i].Name {
				rr = &ref.Home.Rooms[k]
			}
		}
		if rr == nil {
			continue
		}
		rp := shift(rr.Poly, d)
		es := edgesOf([]home.Room{rooms[i]})
		res := edgesOf([]home.Room{{Poly: rp}})
		p := rooms[i].Poly
		for k, e := range es {
			if e.axis == 2 {
				continue
			}
			for _, f := range res {
				if f.axis != e.axis || f.out != e.out {
					continue
				}
				lo, hi := math.Max(e.s0, f.s0), math.Min(e.s1, f.s1)
				gap := (f.c - e.c) * e.out // how far the plan's wall line is beyond this face
				if hi-lo < 0.5*(e.s1-e.s0) || gap < 0.4 || gap > 1.3 {
					continue
				}
				to := f.c - e.out*interiorThick/2
				// The strip the room would take must be free of other rooms.
				free := true
				for j, o := range rooms {
					if j == i {
						continue
					}
					for _, t := range []float64{0.25, 0.5, 0.75} {
						s := e.s0 + (e.s1-e.s0)*t
						for _, c := range []float64{e.c + (to-e.c)*0.3, e.c + (to-e.c)*0.9} {
							q := home.P{s, c}
							if e.axis == 1 {
								q = home.P{c, s}
							}
							if home.Inside(o.Poly, q) {
								free = false
							}
						}
					}
				}
				if !free {
					continue
				}
				a, b := k, (k+1)%len(p)
				p[a][1-e.axis], p[b][1-e.axis] = round3(to), round3(to)
				notes = append(notes, fmt.Sprintf("%s按户型图延伸 %.2f m 到墙（扫描里那里是到顶的柜子）", rooms[i].Name, gap-interiorThick/2))
				break
			}
		}
		rooms[i].Poly = p
	}
	return notes
}

// missingRooms are the plan's rooms the scan has none of, moved into the scan's frame and
// pulled in to the faces of the scan's walls they share (or by a thin wall elsewhere).
func (ref Reference) missingRooms(rooms []home.Room, d home.P) []home.Room {
	var out []home.Room
	for _, rr := range ref.Home.Rooms {
		found := false
		for _, r := range rooms {
			found = found || r.Name == rr.Name
		}
		if found || len(rr.Poly) < 3 {
			continue
		}
		p := shift(rr.Poly, d)
		// It must not sit on a scanned room.
		cx, cz := centroid(p)
		if roomAt(rooms, home.P{cx, cz}) != nil {
			continue
		}
		// Edges next to a scanned room stop at that room's face less an outer wall; the
		// others come in by half a thin wall. Edge k runs from p[k] to p[k+1].
		es := edgesOf([]home.Room{{Poly: p}})
		to := make([]float64, len(es))
		for k, e := range es {
			if e.axis == 2 {
				continue
			}
			best := math.Inf(1)
			for _, f := range edgesOf(rooms) {
				if f.axis != e.axis || f.out != -e.out || math.Min(e.s1, f.s1)-math.Max(e.s0, f.s0) < 0.3 {
					continue
				}
				if g := (f.c - e.c) * e.out; math.Abs(g) < 0.5 && math.Abs(g) < math.Abs(best) {
					best = g
				}
			}
			to[k] = e.c - e.out*interiorThick/2
			if !math.IsInf(best, 1) {
				to[k] = e.c + e.out*best - e.out*ExteriorThick
			}
		}
		q := append([]home.P(nil), p...)
		for k, e := range es {
			if e.axis == 2 {
				continue
			}
			q[k][1-e.axis] = round3(to[k])
			q[(k+1)%len(q)][1-e.axis] = round3(to[k])
		}
		p = q
		out = append(out, home.Room{ID: rr.ID, Name: rr.Name, Kind: rr.Kind, Poly: p, Floor: rr.Floor})
	}
	return out
}

// refPoint is a reference wall's point t from its A end, moved into the scan's frame, with
// the wall's direction.
func (ref Reference) refPoint(wallID string, t float64, d home.P) (home.P, home.P, bool) {
	w, ok := ref.Home.Wall(wallID)
	if !ok || w.Len() == 0 {
		return home.P{}, home.P{}, false
	}
	p := w.At(t)
	return home.P{p[0] + d[0], p[1] + d[1]}, w.Dir(), true
}

// onWall finds the scan wall running the reference wall's way through p, and where along it.
func onWall(ws []home.Wall, p, dir home.P, width float64) (int, float64, bool) {
	best, bd, bt := -1, math.Inf(1), 0.0
	for i, w := range ws {
		wd := w.Dir()
		if math.Abs(wd[0]*dir[1]-wd[1]*dir[0]) > 0.1 {
			continue
		}
		v := home.P{p[0] - w.A[0], p[1] - w.A[1]}
		t := v[0]*wd[0] + v[1]*wd[1]
		across := math.Abs(v[0]*wd[1] - v[1]*wd[0])
		if across > w.Thick/2+0.4 || t < width/2-0.2 || t > w.Len()-width/2+0.2 {
			continue
		}
		if across < bd {
			best, bd, bt = i, across, t
		}
	}
	return best, bt, best >= 0
}

// doors adds the plan's doors the scan did not find (shut doors read as wall) and takes the
// plan's names and the front door from it.
func (ref Reference) doors(h *home.Home, d home.P) []string {
	var notes []string
	for _, rd := range ref.Home.Doors {
		p, dir, ok := ref.refPoint(rd.Wall, rd.At, d)
		if !ok {
			continue
		}
		matched := false
		for i, sd := range h.Doors {
			w, _ := h.Wall(sd.Wall)
			c := w.At(sd.At)
			if math.Hypot(c[0]-p[0], c[1]-p[1]) < math.Max(0.6, (rd.Width+sd.Width)/2) {
				matched = true
				if rd.Entry {
					h.Doors[i].Entry, h.Doors[i].Name = true, "入户门"
				}
				break
			}
		}
		if matched {
			continue
		}
		wi, t, ok := onWall(h.Walls, p, dir, rd.Width)
		if !ok {
			continue
		}
		w := h.Walls[wi]
		at, width := fit(t, math.Min(rd.Width, w.Len()-0.1), w.Len())
		nd := home.Door{Wall: w.ID, At: round3(at), Width: round3(width), Kind: rd.Kind, Hinge: rd.Hinge, Side: rd.Side, Entry: rd.Entry, Name: rd.Name, Height: rd.Height}
		// The scan's wall may run the other way.
		if wd := w.Dir(); wd[0]*dir[0]+wd[1]*dir[1] < 0 {
			nd.Side = -nd.Side
			switch nd.Hinge {
			case "a":
				nd.Hinge = "b"
			case "b":
				nd.Hinge = "a"
			}
		}
		// Not over an opening the scan already has.
		clash := false
		for _, o := range h.Doors {
			clash = clash || (o.Wall == w.ID && math.Abs(o.At-nd.At) < (o.Width+nd.Width)/2)
		}
		for _, o := range h.Windows {
			clash = clash || (o.Wall == w.ID && math.Abs(o.At-nd.At) < (o.Width+nd.Width)/2)
		}
		if clash {
			continue
		}
		h.Doors = append(h.Doors, nd)
		notes = append(notes, fmt.Sprintf("按户型图补上了%s（扫描时门关着）", doorLabel(nd)))
	}
	sort.SliceStable(h.Doors, func(i, j int) bool { return h.Doors[i].Wall < h.Doors[j].Wall })
	for i := range h.Doors {
		h.Doors[i].ID = fmt.Sprintf("d%d", i+1)
	}
	return notes
}

func doorLabel(d home.Door) string {
	if d.Name != "" {
		return d.Name
	}
	return "一扇门"
}

// windows puts the plan's windows, with their sizes, where the scan has an outer wall; the
// scan's own guesses on that stretch give way.
func (ref Reference) windows(h *home.Home, d home.P) {
	for _, rw := range ref.Home.Windows {
		p, dir, ok := ref.refPoint(rw.Wall, rw.At, d)
		if !ok {
			continue
		}
		wi, t, ok := onWall(h.Walls, p, dir, math.Min(rw.Width, 0.6))
		if !ok {
			continue
		}
		w := h.Walls[wi]
		at, width := fit(t, math.Min(rw.Width, w.Len()-0.1), w.Len())
		clash := false
		for _, o := range h.Doors {
			clash = clash || (o.Wall == w.ID && math.Abs(o.At-at) < (o.Width+width)/2)
		}
		if clash {
			continue
		}
		kept := h.Windows[:0]
		for _, o := range h.Windows {
			if !(o.Wall == w.ID && math.Abs(o.At-at) < (o.Width+width)/2+0.2) {
				kept = append(kept, o)
			}
		}
		h.Windows = append(kept, home.Window{Wall: w.ID, At: round3(at), Width: round3(width), Sill: rw.Sill, Height: rw.Height})
	}
	for i := range h.Windows {
		h.Windows[i].ID = fmt.Sprintf("win%d", i+1)
	}
}

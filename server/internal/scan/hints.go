package scan

import (
	"sort"
	"strings"

	"hometwin/internal/home"
)

// RoomHint names a room: a point in it or its outline, in the home's frame (after Frame).
// They come from the scan itself (where each panorama was taken, HintsFromScan) or from a
// floor plan read elsewhere.
type RoomHint struct {
	Name string `json:"name"`
	// Kind is a home.Room kind; empty means guessed from the name.
	Kind string   `json:"kind,omitempty"`
	At   *home.P  `json:"at,omitempty"`
	Poly []home.P `json:"poly,omitempty"`
	Area float64  `json:"area,omitempty"` // m², as the source states it
}

// HintsFromScan names rooms after the panoramas taken in them: room_layout.json says which
// room each panorama is in, the observers where it was taken.
func HintsFromScan(w Work, layout []LayoutRoom, f Frame) []RoomHint {
	name := map[int]string{}
	for _, l := range layout {
		if n := strings.TrimSpace(l.Name); n != "" {
			name[l.Pano] = n
		}
	}
	var out []RoomHint
	for _, o := range w.Observers {
		n, ok := name[o.Index]
		if !ok {
			continue
		}
		// The viewer's frame (x, y up, z) is the model's (x, −z, y).
		p := f.Point(o.Standing[0], -o.Standing[2], o.Standing[1])
		at := home.P{p[0], p[2]}
		out = append(out, RoomHint{Name: n, Kind: KindOf(n), At: &at})
	}
	return out
}

// KindOf guesses a room kind from its Chinese name.
func KindOf(name string) string {
	for _, k := range []struct{ word, kind string }{
		{"阳台", "balcony"}, {"露台", "balcony"},
		{"卫", "bath"}, {"厕", "bath"}, {"洗手间", "bath"}, {"浴", "bath"},
		{"厨", "kitchen"},
		{"玄关", "entry"}, {"门厅", "entry"}, {"走廊", "entry"}, {"过道", "entry"},
		{"卧", "bedroom"}, {"书房", "bedroom"}, {"儿童", "bedroom"}, {"客房", "bedroom"},
		{"厅", "living"}, {"起居", "living"},
	} {
		if strings.Contains(name, k.word) {
			return k.kind
		}
	}
	return "living"
}

// assignNames gives each region the name that most hints vote for: a hint point inside the
// region is one vote, an outline votes with the share of it that falls in the region. A
// name goes to one region only, the one it votes for most.
func assignNames(g grid, regs []region, hints []RoomHint) []RoomHint {
	type vote struct {
		reg  int
		name string
		w    float64
	}
	byName := map[string]RoomHint{}
	type key struct {
		reg  int
		name string
	}
	score := map[key]float64{}
	for _, h := range hints {
		if _, ok := byName[h.Name]; !ok {
			byName[h.Name] = h
		}
		for i, r := range regs {
			w := 0.0
			if h.At != nil {
				if k, ok := g.at(h.At[0], h.At[1]); ok && r.cells[k] {
					w = 1
				}
			}
			if len(h.Poly) >= 3 {
				in, all := 0, 0
				for k, c := range r.cells {
					if !c {
						continue
					}
					all++
					if home.Inside(h.Poly, g.centre(k)) {
						in++
					}
				}
				if all > 0 {
					// Share of the hint's outline covered by the region, by area.
					cover := float64(in) * g.cell * g.cell / max(home.Area(h.Poly), 0.01)
					share := float64(in) / float64(all)
					w = max(w, min(cover, share))
				}
			}
			if w > 0 {
				score[key{i, h.Name}] += w
			}
		}
	}
	var vs []vote
	for k, w := range score {
		vs = append(vs, vote{k.reg, k.name, w})
	}
	sort.Slice(vs, func(i, j int) bool {
		if vs[i].w != vs[j].w {
			return vs[i].w > vs[j].w
		}
		if vs[i].reg != vs[j].reg {
			return vs[i].reg < vs[j].reg
		}
		return vs[i].name < vs[j].name
	})
	out := make([]RoomHint, len(regs))
	usedName := map[string]bool{}
	for _, v := range vs {
		if out[v.reg].Name != "" || usedName[v.name] || v.w < 0.3 {
			continue
		}
		out[v.reg] = byName[v.name]
		usedName[v.name] = true
	}
	for i := range out {
		if out[i].Kind == "" && out[i].Name != "" {
			out[i].Kind = KindOf(out[i].Name)
		}
	}
	return out
}

// WindowsFromScan places the scan's window labels on the floor plan: one point per window,
// in the home's frame, with the label's height above the main floor.
func WindowsFromScan(w Work, f Frame) []WindowHint {
	var out []WindowHint
	for _, l := range w.WindowLabels {
		if len(l.Box) == 0 {
			continue
		}
		var c [3]float64
		for _, p := range l.Box {
			c[0] += p[0] / float64(len(l.Box))
			c[1] += p[1] / float64(len(l.Box))
			c[2] += p[2] / float64(len(l.Box))
		}
		p := f.Point(c[0], c[2], c[1])
		out = append(out, WindowHint{At: home.P{p[0], p[2]}, Y: p[1]})
	}
	return out
}

// WindowHint says a window is at At (on the floor plan), Y m above the main floor.
type WindowHint struct {
	At home.P  `json:"at"`
	Y  float64 `json:"y"`
}

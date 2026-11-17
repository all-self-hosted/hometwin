package ha

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"

	"hometwin/internal/devices"
	"hometwin/internal/home"
)

// Mapping ties one or more HA entities to a twin device (two sensors of one HA device — a
// temperature and a humidity — make one twin sensor).
type Mapping struct {
	Entities []string `json:"entities"`
	Device   Placed   `json:"device"`
	// TakeOver is the simulated device this mapping replaced (it keeps its id and place in the
	// scene); Original is that device as it was, to put back when the mapping is removed.
	TakeOver string          `json:"takeOver,omitempty"`
	Original *devices.Device `json:"original,omitempty"`
}

// Placed is a twin device: what and where.
type Placed struct {
	ID    string  `json:"id"`
	Kind  string  `json:"kind"`
	Name  string  `json:"name"`
	Room  string  `json:"room"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	Z     float64 `json:"z"`
	Rot   float64 `json:"rot"`
	Width float64 `json:"width,omitempty"`
	Door  string  `json:"door,omitempty"`
}

// MappingStore keeps the mappings in <dir>/ha-mapping.json.
type MappingStore struct {
	path string
	mu   sync.Mutex
	list []Mapping
}

func NewMappingStore(dir string) (*MappingStore, error) {
	s := &MappingStore{}
	if dir == "" {
		return s, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s.path = filepath.Join(dir, "ha-mapping.json")
	b, err := os.ReadFile(s.path)
	switch {
	case err == nil:
		if err := json.Unmarshal(b, &s.list); err != nil {
			return nil, fmt.Errorf("%s：%w", s.path, err)
		}
	case !errors.Is(err, os.ErrNotExist):
		return nil, err
	}
	return s, nil
}

func (s *MappingStore) save() error {
	if s.path == "" {
		return nil
	}
	b, _ := json.MarshalIndent(s.list, "", "  ")
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *MappingStore) All() []Mapping {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.list)
}

// ForEntity is the mapping an entity belongs to.
func (s *MappingStore) ForEntity(entity string) (Mapping, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.list {
		if slices.Contains(m.Entities, entity) {
			return m, true
		}
	}
	return Mapping{}, false
}

func (s *MappingStore) put(m Mapping) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.list = slices.DeleteFunc(s.list, func(o Mapping) bool {
		if o.Device.ID == m.Device.ID {
			return true
		}
		for _, e := range m.Entities {
			if slices.Contains(o.Entities, e) {
				return true
			}
		}
		return false
	})
	s.list = append(s.list, m)
	return s.save()
}

func (s *MappingStore) drop(entity string) (Mapping, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.list, func(m Mapping) bool { return slices.Contains(m.Entities, entity) })
	if i < 0 {
		return Mapping{}, false, nil
	}
	m := s.list[i]
	s.list = slices.Delete(s.list, i, i+1)
	return m, true, s.save()
}

// --- rooms from HA areas ---

// roomWords maps area names and words in entity names to the twin's rooms (room kinds or ids).
var roomWords = []struct {
	room  string
	words []string
}{
	{"living", []string{"living room", "living", "lounge", "family room", "sitting room", "dining", "客厅", "客餐厅", "餐厅", "起居室"}},
	{"master", []string{"master bedroom", "master", "main bedroom", "bedroom", "主卧", "卧室", "主人房"}},
	{"second", []string{"guest room", "guest", "kids room", "kids", "children", "nursery", "office", "study", "次卧", "书房", "儿童房", "客房"}},
	{"kitchen", []string{"kitchen", "厨房"}},
	{"bath", []string{"bathroom", "bath", "toilet", "restroom", "washroom", "shower", "卫生间", "浴室", "洗手间", "厕所"}},
	{"entry", []string{"hallway", "entrance", "entry", "front door", "foyer", "hall", "porch", "玄关", "门厅", "走廊", "入户"}},
	{"balcony", []string{"balcony", "terrace", "patio", "阳台", "露台"}},
	{"outdoors", []string{"outside", "outdoor", "garden", "yard", "garage", "室外", "户外", "花园"}},
}

var nonWord = regexp.MustCompile(`[^\p{L}\p{N}]+`)

func norm(s string) string {
	return strings.TrimSpace(nonWord.ReplaceAllString(strings.ToLower(s), " "))
}

// roomFor finds the twin room for a text (an HA area name or an entity name): the longest
// matching word wins. Returns a room id of h, "outdoors", or "".
func roomFor(h home.Home, text string) string {
	t := " " + norm(text) + " "
	best, bestLen := "", 0
	for _, r := range h.Rooms {
		n := norm(r.Name)
		if n != "" && strings.Contains(t, n) && len(n) > bestLen {
			best, bestLen = r.ID, len(n)
		}
	}
	for _, rw := range roomWords {
		for _, w := range rw.words {
			nw := norm(w)
			// latin words must match whole words; Chinese ones anywhere
			hit := strings.Contains(t, " "+nw+" ")
			if !hit && nw != "" && nw[0] >= 0x80 {
				hit = strings.Contains(t, nw)
			}
			if hit && len(nw) > bestLen {
				best, bestLen = rw.room, len(nw)
			}
		}
	}
	if best == "" || best == "outdoors" {
		return best
	}
	// a word names a room id or a room kind of this home
	for _, r := range h.Rooms {
		if r.ID == best {
			return r.ID
		}
	}
	for _, r := range h.Rooms {
		if r.Kind == best {
			return r.ID
		}
	}
	return ""
}

// --- placement ---

func roomOf(h home.Home, id string) *home.Room {
	for i := range h.Rooms {
		if h.Rooms[i].ID == id {
			return &h.Rooms[i]
		}
	}
	return nil
}

// centroid is the area-weighted centre of a polygon, pulled inside when the shape is concave.
func centroid(r home.Room) home.P {
	var cx, cz, a float64
	p := r.Poly
	for i := range p {
		q := p[(i+1)%len(p)]
		f := p[i][0]*q[1] - q[0]*p[i][1]
		a += f
		cx += (p[i][0] + q[0]) * f
		cz += (p[i][1] + q[1]) * f
	}
	c := home.P{cx / (3 * a), cz / (3 * a)}
	if home.Inside(p, c) {
		return c
	}
	// the inside point nearest the centre, on a 10 cm grid
	minx, maxx, minz, maxz := math.Inf(1), math.Inf(-1), math.Inf(1), math.Inf(-1)
	for _, v := range p {
		minx, maxx = math.Min(minx, v[0]), math.Max(maxx, v[0])
		minz, maxz = math.Min(minz, v[1]), math.Max(maxz, v[1])
	}
	best, bd := c, math.Inf(1)
	for x := minx; x <= maxx; x += 0.1 {
		for z := minz; z <= maxz; z += 0.1 {
			q := home.P{x, z}
			if d := math.Hypot(x-c[0], z-c[1]); d < bd && home.Inside(p, q) {
				best, bd = q, d
			}
		}
	}
	return best
}

// face is the stretch of a wall that bounds a room, seen from inside it.
type face struct {
	wall   home.Wall
	t0, t1 float64 // along the wall
	in     home.P  // unit normal pointing into the room
}

func (f face) length() float64 { return f.t1 - f.t0 }

// point is a spot on the room-side face of the wall, t metres along it, off metres out from it.
func (f face) point(t, off float64) home.P {
	c := f.wall.At(t)
	d := f.wall.Thick/2 + off
	return home.P{c[0] + f.in[0]*d, c[1] + f.in[1]*d}
}

// rot is the twin rotation (degrees) that turns a device's front (local +z) to face into the room.
func (f face) rot() float64 { return math.Round(math.Atan2(f.in[0], f.in[1])*180/math.Pi*100) / 100 }

func faces(h home.Home, r home.Room) []face {
	var out []face
	for _, w := range h.Walls {
		d, n := w.Dir(), w.Normal()
		for i := range r.Poly {
			a, b := r.Poly[i], r.Poly[(i+1)%len(r.Poly)]
			off := func(p home.P) (t, o float64) {
				v := home.P{p[0] - w.A[0], p[1] - w.A[1]}
				return v[0]*d[0] + v[1]*d[1], v[0]*n[0] + v[1]*n[1]
			}
			ta, oa := off(a)
			tb, ob := off(b)
			if math.Abs(oa) > 0.02 || math.Abs(ob) > 0.02 {
				continue
			}
			t0, t1 := math.Max(0, math.Min(ta, tb)), math.Min(w.Len(), math.Max(ta, tb))
			if t1-t0 < 0.3 {
				continue
			}
			mid := w.At((t0 + t1) / 2)
			in := n
			if !home.Inside(r.Poly, home.P{mid[0] + n[0]*0.2, mid[1] + n[1]*0.2}) {
				in = home.P{-n[0], -n[1]}
			}
			out = append(out, face{w, t0, t1, in})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].length() > out[j].length() })
	return out
}

// freeT is the middle of the longest stretch of a face that no door or window takes.
func freeT(h home.Home, f face) float64 {
	cuts := [][2]float64{}
	for _, d := range h.Doors {
		if d.Wall == f.wall.ID {
			cuts = append(cuts, [2]float64{d.At - d.Width/2 - 0.1, d.At + d.Width/2 + 0.1})
		}
	}
	for _, wn := range h.Windows {
		if wn.Wall == f.wall.ID {
			cuts = append(cuts, [2]float64{wn.At - wn.Width/2 - 0.1, wn.At + wn.Width/2 + 0.1})
		}
	}
	sort.Slice(cuts, func(i, j int) bool { return cuts[i][0] < cuts[j][0] })
	best, bl, t := (f.t0+f.t1)/2, -1.0, f.t0+0.2
	for _, c := range append(cuts, [2]float64{f.t1 - 0.2, f.t1 - 0.2}) {
		end := math.Min(c[0], f.t1-0.2)
		if end-t > bl {
			best, bl = (t+end)/2, end-t
		}
		t = math.Max(t, c[1])
	}
	return best
}

// Place is where a device of a kind goes in a room, as Suggest would put it.
func Place(h home.Home, kind, roomID string) (Placed, bool) {
	p := Placed{Kind: kind, Room: roomID}
	if roomID == "outdoors" {
		// on the outside of the longest exterior wall
		var best *home.Wall
		for i := range h.Walls {
			w := &h.Walls[i]
			if w.Exterior && (best == nil || w.Len() > best.Len()) {
				best = w
			}
		}
		if best == nil {
			return p, false
		}
		c, n := best.At(best.Len()/2), best.Normal()
		// the outside is the side no room is on
		if h.RoomAt(home.P{c[0] + n[0]*0.3, c[1] + n[1]*0.3}) != nil {
			n = home.P{-n[0], -n[1]}
		}
		d := best.Thick / 2
		p.X, p.Z, p.Y = round2(c[0]+n[0]*d), round2(c[1]+n[1]*d), 1.0
		p.Rot = math.Round(math.Atan2(n[0], n[1]) * 180 / math.Pi)
		return p, true
	}
	r := roomOf(h, roomID)
	if r == nil {
		return p, false
	}
	fs := faces(h, *r)
	if len(fs) == 0 {
		return p, false
	}
	set := func(f face, t, off, y float64) {
		q := f.point(t, off)
		p.X, p.Z, p.Y, p.Rot = round2(q[0]), round2(q[1]), y, f.rot()
	}
	switch kind {
	case devices.Light:
		c := centroid(*r)
		p.X, p.Z, p.Y = round2(c[0]), round2(c[1]), 2.72
	case devices.AC:
		f := fs[0]
		for _, x := range fs {
			if x.wall.Exterior {
				f = x
				break
			}
		}
		set(f, freeT(h, f), 0, 2.3)
	case devices.Lock:
		door := home.Door{}
		for _, d := range h.Doors {
			if d.Entry {
				door = d
			}
		}
		if door.ID == "" {
			return p, false
		}
		w, _ := h.Wall(door.Wall)
		side := float64(door.Side)
		if side == 0 {
			side = 1
		}
		n := w.Normal()
		in := home.P{n[0] * side, n[1] * side}
		c := w.At(door.At + door.Width/2 - 0.08)
		p.X, p.Z, p.Y, p.Door = round2(c[0]+in[0]*w.Thick/2), round2(c[1]+in[1]*w.Thick/2), 1.0, door.ID
		p.Rot = math.Round(math.Atan2(in[0], in[1]) * 180 / math.Pi)
		if r := h.RoomAt(home.P{c[0] + in[0]*0.3, c[1] + in[1]*0.3}); r != nil {
			p.Room = r.ID
		}
	case devices.Curtain:
		// the room's biggest window, or its widest door
		type opening struct {
			wall      string
			at, width float64
		}
		var best opening
		for _, wn := range h.Windows {
			for _, f := range fs {
				if f.wall.ID == wn.Wall && wn.At > f.t0 && wn.At < f.t1 && wn.Width > best.width {
					best = opening{wn.Wall, wn.At, wn.Width}
				}
			}
		}
		if best.wall == "" {
			for _, d := range h.Doors {
				for _, f := range fs {
					if f.wall.ID == d.Wall && d.At > f.t0 && d.At < f.t1 && d.Width > best.width {
						best = opening{d.Wall, d.At, d.Width}
					}
				}
			}
		}
		if best.wall == "" {
			return p, false
		}
		for _, f := range fs {
			if f.wall.ID == best.wall {
				set(f, best.at, 0.04, 0)
				p.Width = round2(best.width + 0.5)
				break
			}
		}
	case devices.Vacuum:
		f := fs[0]
		set(f, freeT(h, f), 0.16, 0)
	case devices.Sensor:
		f := fs[len(fs)-1]
		for i := len(fs) - 1; i >= 0; i-- {
			if !fs[i].wall.Exterior {
				f = fs[i]
				break
			}
		}
		set(f, f.t0+math.Min(0.4, f.length()/2), 0, 1.0)
	default:
		return p, false
	}
	return p, true
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

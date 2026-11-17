// Package home is the shell of a home: rooms, walls, doors and windows, in metres on the floor
// plane. x runs east, z runs south, y is up. A wall goes from A to B; its normal, (-dz, dx)
// along A→B, is the side +1.
package home

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
)

type P [2]float64 // x, z in metres

type Home struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Area is the floor area the deed states (建筑面积), m².
	Area float64 `json:"area"`
	// WallHeight is the ceiling height; CutHeight is where the cutaway view cuts the walls.
	WallHeight float64  `json:"wallHeight"`
	CutHeight  float64  `json:"cutHeight"`
	Rooms      []Room   `json:"rooms"`
	Walls      []Wall   `json:"walls"`
	Doors      []Door   `json:"doors"`
	Windows    []Window `json:"windows"`
	// Fixtures are what comes with the home (kitchen run, toilet, shower), drawn but not moved.
	Fixtures []Fixture `json:"fixtures,omitempty"`
	// Levels are parts of a room's floor at another height: a platform (地台) and its steps.
	Levels []Level `json:"levels,omitempty"`
	// Scan is the address of the home's scanned model (a glTF binary in the home's frame),
	// when it was imported from a VR scan.
	Scan string `json:"scan,omitempty"`
}

type Room struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Kind picks the floor: living, bedroom, kitchen, bath, entry, balcony.
	Kind string `json:"kind"`
	Poly []P    `json:"poly"`
	// Floor overrides the finish Kind picks: wood or tile.
	Floor string `json:"floor,omitempty"`
	// Y is the height of the room's whole floor above the main floor: a raised bedroom, a
	// bathroom a step down. Platforms within a room are Levels.
	Y float64 `json:"y,omitempty"`
}

// Level is a raised (or sunken) part of a room's floor. Y is its top above the main floor;
// a platform's steps are levels of their own, lower than the platform.
type Level struct {
	ID    string  `json:"id"`
	Room  string  `json:"room"`
	Kind  string  `json:"kind"` // platform, step
	Y     float64 `json:"y"`
	Poly  []P     `json:"poly"`
	Floor string  `json:"floor,omitempty"` // wood or tile; the room's finish when empty
}

type Wall struct {
	ID    string  `json:"id"`
	A     P       `json:"a"`
	B     P       `json:"b"`
	Thick float64 `json:"thick"`
	// H is the wall's own height when lower than the ceiling (a balcony parapet).
	H float64 `json:"h,omitempty"`
	// Exterior walls face outside.
	Exterior bool `json:"exterior,omitempty"`
	// Railing is the height of an open balustrade (a balcony's front) standing in place of the
	// wall; Glazed fills the rest up to the ceiling with glass, or all of it above a curb when
	// there is no railing.
	Railing float64 `json:"railing,omitempty"`
	Glazed  bool    `json:"glazed,omitempty"`
}

type Door struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	// Color is the leaf's colour (#rrggbb), the default finish when empty.
	Color string `json:"color,omitempty"`
	Wall  string `json:"wall"`
	// At is the opening's centre, metres from the wall's A end.
	At    float64 `json:"at"`
	Width float64 `json:"width"`
	// Kind: swing, sliding, opening (no leaf).
	Kind string `json:"kind"`
	// Hinge is "a" or "b": which end of the opening the leaf hangs on (swing doors).
	Hinge string `json:"hinge,omitempty"`
	// Side is the side of the wall the leaf opens into: +1 the wall's normal, -1 the other.
	Side  int  `json:"side,omitempty"`
	Entry bool `json:"entry,omitempty"`
	// Height of the opening, 2.1 m when not given.
	Height float64 `json:"height,omitempty"`
}

type Window struct {
	ID     string  `json:"id"`
	Wall   string  `json:"wall"`
	At     float64 `json:"at"`
	Width  float64 `json:"width"`
	Sill   float64 `json:"sill"`
	Height float64 `json:"height"`
	// Bay is how far a bay window stands out from the wall's outside face: a box up to the sill,
	// glazed on three sides up to the head, under a roof.
	Bay float64 `json:"bay,omitempty"`
}

type Fixture struct {
	ID   string  `json:"id"`
	Kind string  `json:"kind"` // counter, upper, fridge, toilet, basin, shower, washer, shoe
	Name string  `json:"name"`
	X    float64 `json:"x"`
	Z    float64 `json:"z"`
	W    float64 `json:"w"`
	D    float64 `json:"d"`
	H    float64 `json:"h"`
	Rot  float64 `json:"rot"`
}

//go:embed sample.json
var sampleJSON []byte

// Sample is the bundled apartment.
func Sample() Home {
	var h Home
	if err := json.Unmarshal(sampleJSON, &h); err != nil {
		panic(err)
	}
	return h
}

func (h Home) Wall(id string) (Wall, bool) {
	for _, w := range h.Walls {
		if w.ID == id {
			return w, true
		}
	}
	return Wall{}, false
}

// Check says what is wrong with a home, nothing when it can be used.
func (h Home) Check() []string {
	var out []string
	ids := map[string]bool{}
	for _, w := range h.Walls {
		if ids[w.ID] {
			out = append(out, fmt.Sprintf("墙 %s 重复", w.ID))
		}
		ids[w.ID] = true
		if w.Len() < 0.05 || w.Thick <= 0 {
			out = append(out, fmt.Sprintf("墙 %s 太短或没有厚度", w.ID))
		}
	}
	for _, r := range h.Rooms {
		if len(r.Poly) < 3 {
			out = append(out, fmt.Sprintf("房间 %s 至少要 3 个点", r.Name))
		}
		if r.Y < -0.5 || r.Y > 0.6 {
			out = append(out, fmt.Sprintf("房间 %s 的地面高度 %.2f m 不合理", r.Name, r.Y))
		}
	}
	check := func(kind, id, wall string, at, width float64) {
		w, ok := h.Wall(wall)
		if !ok {
			out = append(out, fmt.Sprintf("%s %s 所在的墙 %s 不存在", kind, id, wall))
			return
		}
		if at-width/2 < -1e-6 || at+width/2 > w.Len()+1e-6 {
			out = append(out, fmt.Sprintf("%s %s 超出了墙 %s", kind, id, wall))
		}
	}
	rooms := map[string]bool{}
	for _, r := range h.Rooms {
		rooms[r.ID] = true
	}
	for _, l := range h.Levels {
		if !rooms[l.Room] {
			out = append(out, fmt.Sprintf("地面高差 %s 所在的房间 %s 不存在", l.ID, l.Room))
		}
		if len(l.Poly) < 3 {
			out = append(out, fmt.Sprintf("地面高差 %s 至少要 3 个点", l.ID))
		}
		if l.Y < -0.5 || l.Y > 0.6 || l.Y == 0 {
			out = append(out, fmt.Sprintf("地面高差 %s 的高度 %.2f m 不合理", l.ID, l.Y))
		}
	}
	for _, d := range h.Doors {
		check("门", d.ID, d.Wall, d.At, d.Width)
	}
	for _, wn := range h.Windows {
		check("窗", wn.ID, wn.Wall, wn.At, wn.Width)
	}
	return out
}

func (w Wall) Len() float64 { return math.Hypot(w.B[0]-w.A[0], w.B[1]-w.A[1]) }

// Dir is the unit vector A→B; Normal is the side +1.
func (w Wall) Dir() P {
	l := w.Len()
	return P{(w.B[0] - w.A[0]) / l, (w.B[1] - w.A[1]) / l}
}
func (w Wall) Normal() P { d := w.Dir(); return P{-d[1], d[0]} }

// At is the point t metres from A along the wall's centre line.
func (w Wall) At(t float64) P { d := w.Dir(); return P{w.A[0] + d[0]*t, w.A[1] + d[1]*t} }

// Area of a polygon, m².
func Area(poly []P) float64 {
	s := 0.0
	for i := range poly {
		a, b := poly[i], poly[(i+1)%len(poly)]
		s += a[0]*b[1] - b[0]*a[1]
	}
	return math.Abs(s) / 2
}

// Inside says whether p is inside the polygon.
func Inside(poly []P, p P) bool {
	in := false
	for i, j := 0, len(poly)-1; i < len(poly); j, i = i, i+1 {
		a, b := poly[i], poly[j]
		if (a[1] > p[1]) != (b[1] > p[1]) && p[0] < (b[0]-a[0])*(p[1]-a[1])/(b[1]-a[1])+a[0] {
			in = !in
		}
	}
	return in
}

// FloorAt is the height of the floor at p above the main floor: the highest level there,
// else the room's own floor.
func (h Home) FloorAt(p P) float64 {
	y, found := 0.0, false
	for _, l := range h.Levels {
		if Inside(l.Poly, p) && (!found || l.Y > y) {
			y, found = l.Y, true
		}
	}
	if found {
		return y
	}
	if r := h.RoomAt(p); r != nil {
		return r.Y
	}
	return 0
}

// RoomAt is the room p stands in, or nil.
func (h Home) RoomAt(p P) *Room {
	for i := range h.Rooms {
		if Inside(h.Rooms[i].Poly, p) {
			return &h.Rooms[i]
		}
	}
	return nil
}

// EntryDoor is the front door, if the home has one.
func (h Home) EntryDoor() (Door, bool) {
	for _, d := range h.Doors {
		if d.Entry {
			return d, true
		}
	}
	return Door{}, false
}

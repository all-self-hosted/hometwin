package ontology

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"

	"hometwin/internal/devices"
	"hometwin/internal/home"
	"hometwin/internal/layout"
)

// roomClass is the class of a room by its kind.
var roomClass = map[string]string{"living": "LivingRoom", "bedroom": "Bedroom", "kitchen": "Kitchen", "bath": "Bathroom", "balcony": "Balcony", "entry": "Entry"}

// furnitureClass is the class of a piece of furniture by its product type.
var furnitureClass = map[string]string{
	"bed": "Bed", "sofa": "Sofa", "armchair": "Armchair", "bookcase": "Bookcase", "wardrobe": "Wardrobe", "drawers": "Drawers",
	"tvbench": "TVBench", "desk": "Desk", "table": "Table", "chair": "Chair", "shelf": "Shelf", "lamp": "FloorLamp", "rug": "Rug",
}

var deviceClass = map[string]string{
	devices.Light: "Light", devices.AC: "AirConditioner", devices.Lock: "DoorLock",
	devices.Curtain: "Curtain", devices.Vacuum: "RobotVacuum", devices.Sensor: "Sensor",
}

// what a device does to the room it is in
var deviceRel = map[string]string{devices.Light: "illuminates", devices.AC: "controls", devices.Sensor: "measures", devices.Vacuum: "cleans"}

// facts is what is known about one home, its furniture and its devices, as triples, and the
// names of things for people.
type facts struct {
	triples []graph.RDFTriple
	names   map[string]string // node id → name
	types   map[string]string // node id → its most specific class (ht id)
	props   map[string]map[string]any
}

func dist(a, b home.P) float64 { return math.Hypot(a[0]-b[0], a[1]-b[1]) }

// side is the compass side of a room an edge a→b of its outline is on: 北 is −z.
func side(poly []home.P, a, b home.P) string {
	l := dist(a, b)
	nx, nz := -(b[1]-a[1])/l, (b[0]-a[0])/l
	mid := home.P{(a[0] + b[0]) / 2, (a[1] + b[1]) / 2}
	if home.Inside(poly, home.P{mid[0] + nx*0.1, mid[1] + nz*0.1}) {
		nx, nz = -nx, -nz
	}
	if math.Abs(nx) > math.Abs(nz) {
		if nx > 0 {
			return "东"
		}
		return "西"
	}
	if nz > 0 {
		return "南"
	}
	return "北"
}

// segOverlap is how long segment a1-a2 runs along wall w's centre line.
func segOverlap(w home.Wall, a1, a2 home.P) float64 {
	d, n := w.Dir(), w.Normal()
	off := func(p home.P) (t, o float64) {
		v := home.P{p[0] - w.A[0], p[1] - w.A[1]}
		return v[0]*d[0] + v[1]*d[1], v[0]*n[0] + v[1]*n[1]
	}
	t1, o1 := off(a1)
	t2, o2 := off(a2)
	if math.Abs(o1) > 0.02 || math.Abs(o2) > 0.02 {
		return 0
	}
	lo, hi := math.Max(0, math.Min(t1, t2)), math.Min(w.Len(), math.Max(t1, t2))
	return hi - lo
}

type wallTouch struct {
	wall string
	back bool
}

// against finds the walls an item stands flat against (within 8 cm, parallel).
func against(h home.Home, it layout.Item) []wallTouch {
	rot := it.Rot * math.Pi / 180
	c, s := math.Cos(rot), math.Sin(rot)
	w, d := it.Product.W/100, it.Product.D/100
	at := func(lx, lz float64) home.P { return home.P{it.X + lx*c + lz*s, it.Z - lx*s + lz*c} }
	faces := []struct {
		mid  home.P
		dir  home.P
		back bool
	}{
		{at(0, -d/2), home.P{c, -s}, true},
		{at(0, d/2), home.P{c, -s}, false},
		{at(-w/2, 0), home.P{s, c}, false},
		{at(w/2, 0), home.P{s, c}, false},
	}
	var out []wallTouch
	seen := map[string]bool{}
	for _, f := range faces {
		for _, wl := range h.Walls {
			wd, wn := wl.Dir(), wl.Normal()
			if math.Abs(wd[0]*f.dir[0]+wd[1]*f.dir[1]) < 0.98 {
				continue
			}
			v := home.P{f.mid[0] - wl.A[0], f.mid[1] - wl.A[1]}
			t := v[0]*wd[0] + v[1]*wd[1]
			o := math.Abs(v[0]*wn[0]+v[1]*wn[1]) - wl.Thick/2
			if o < -0.02 || o > 0.08 || t < -0.05 || t > wl.Len()+0.05 {
				continue
			}
			if !seen[wl.ID] {
				seen[wl.ID] = true
				out = append(out, wallTouch{wl.ID, f.back})
			}
		}
	}
	return out
}

// footprintGap is roughly the distance between two items' footprints.
func footprintGap(a, b layout.Item) float64 {
	ext := func(it layout.Item, ux, uz float64) float64 {
		r := it.Rot * math.Pi / 180
		c, s := math.Cos(r), math.Sin(r)
		lx := ux*c - uz*s
		lz := ux*s + uz*c
		return math.Abs(lx)*it.Product.W/200 + math.Abs(lz)*it.Product.D/200
	}
	dx, dz := b.X-a.X, b.Z-a.Z
	l := math.Hypot(dx, dz)
	if l < 1e-6 {
		return 0
	}
	return l - ext(a, dx/l, dz/l) - ext(b, dx/l, dz/l)
}

func compile(h home.Home, l layout.Layout, devs []devices.Device) *facts {
	fs := &facts{names: map[string]string{}, types: map[string]string{}, props: map[string]map[string]any{}}
	add := func(s, p, o graph.RDFTerm) {
		fs.triples = append(fs.triples, graph.RDFTriple{Subject: s, Predicate: p, Object: o, Graph: &homeGraph})
	}
	node := func(id, class, name string, props map[string]any) graph.RDFTerm {
		t := thing(id)
		if _, ok := fs.names[id]; !ok {
			add(t, iriType, ht(class))
			add(t, iriLabel, literal(name))
			fs.names[id], fs.types[id] = name, class
			if props == nil {
				props = map[string]any{}
			}
			fs.props[id] = props
			for _, k := range sortedKeys(props) {
				switch v := props[k].(type) {
				case float64:
					add(t, ht(k), number(v))
				case string:
					if v != "" {
						add(t, ht(k), literal(v))
					}
				case bool:
					add(t, ht(k), graph.NewTypedLiteral(fmt.Sprint(v), nsXSD+"boolean"))
				}
			}
		}
		return t
	}
	link := func(from, rel, to string) { add(thing(from), ht(rel), thing(to)) }

	homeID := "home:" + h.ID
	node(homeID, "Home", h.Name, map[string]any{"area": h.Area})
	node("outside", "Outside", "户外/楼道", nil)
	roomName := map[string]string{}
	for _, r := range h.Rooms {
		class := roomClass[r.Kind]
		if class == "" {
			class = "Room"
		}
		id := "room:" + r.ID
		roomName[id] = r.Name
		node(id, class, r.Name, map[string]any{"kind": r.Kind, "area": math.Round(home.Area(r.Poly)*10) / 10})
		link(id, "locatedIn", homeID)
	}
	roomAt := func(p home.P) string {
		if r := h.RoomAt(p); r != nil {
			return "room:" + r.ID
		}
		return "outside"
	}

	// walls bound the rooms whose outline runs along them; each room sees its own face of it
	face := map[string]string{} // wall id + room id → face node id
	for _, w := range h.Walls {
		id := "wall:" + w.ID
		node(id, "Wall", "墙 "+w.ID, map[string]any{"thick": w.Thick, "exterior": w.Exterior})
		link(id, "partOf", homeID)
		for _, r := range h.Rooms {
			for i := range r.Poly {
				a, b := r.Poly[i], r.Poly[(i+1)%len(r.Poly)]
				if segOverlap(w, a, b) > 0.15 {
					s := side(r.Poly, a, b)
					fid := "wallface:" + w.ID + "@" + r.ID
					node(fid, "WallFace", r.Name+s+"墙", map[string]any{"side": s})
					link(fid, "partOf", id)
					link(fid, "faces", "room:"+r.ID)
					link(id, "bounds", "room:"+r.ID)
					face[w.ID+"@"+r.ID] = fid
					break
				}
			}
		}
	}
	for _, d := range h.Doors {
		w, ok := h.Wall(d.Wall)
		if !ok {
			continue
		}
		id := "door:" + d.ID
		name := d.Name
		if name == "" {
			name = "门"
		}
		node(id, "Door", name, map[string]any{"width": d.Width, "kind": d.Kind, "entry": d.Entry})
		link(id, "partOf", "wall:"+w.ID)
		c, n := w.At(d.At), w.Normal()
		off := w.Thick/2 + 0.25
		link(id, "connects", roomAt(home.P{c[0] + n[0]*off, c[1] + n[1]*off}))
		link(id, "connects", roomAt(home.P{c[0] - n[0]*off, c[1] - n[1]*off}))
	}
	for _, wn := range h.Windows {
		w, ok := h.Wall(wn.Wall)
		if !ok {
			continue
		}
		c, n := w.At(wn.At), w.Normal()
		off := w.Thick/2 + 0.25
		room := roomAt(home.P{c[0] + n[0]*off, c[1] + n[1]*off})
		if room == "outside" {
			room = roomAt(home.P{c[0] - n[0]*off, c[1] - n[1]*off})
		}
		id := "window:" + wn.ID
		node(id, "Window", roomName[room]+"窗", map[string]any{"width": wn.Width, "sill": wn.Sill})
		link(id, "partOf", "wall:"+w.ID)
		link(id, "locatedIn", room)
	}
	for _, fx := range h.Fixtures {
		id := "fixture:" + fx.ID
		node(id, "Fixture", fx.Name, map[string]any{"kind": fx.Kind})
		link(id, "locatedIn", roomAt(home.P{fx.X, fx.Z}))
	}

	// furniture: in a room, against walls, near each other, a model of a product
	for _, it := range l.Items {
		id := "item:" + it.ID
		p := it.Product
		class := furnitureClass[p.Type]
		if class == "" {
			class = "OtherFurniture"
		}
		node(id, class, layout.Label(p), map[string]any{"type": p.Type, "typeLabel": p.TypeLabel, "size": fmt.Sprintf("%.0f×%.0f×%.0f cm", p.W, p.D, p.H)})
		room := roomAt(home.P{it.X, it.Z})
		link(id, "locatedIn", room)
		for _, wa := range against(h, it) {
			target := "wall:" + wa.wall
			if fid, ok := face[wa.wall+"@"+room[len("room:"):]]; ok && room != "outside" {
				target = fid
			}
			rel := "against"
			if wa.back {
				rel = "backAgainst"
			}
			link(id, rel, target)
		}
		key := p.Article
		if key == "" {
			key = p.URL
		}
		if key == "" {
			key = fmt.Sprintf("%s-%.0fx%.0fx%.0f", p.Name, p.W, p.D, p.H)
		}
		pid := "product:" + key
		node(pid, "Product", p.Name, map[string]any{"article": p.Article, "url": p.URL, "width": p.W, "depth": p.D, "height": p.H, "price": p.Price, "currency": p.Currency, "source": p.Source})
		link(id, "modelOf", pid)
	}
	for i, a := range l.Items {
		for _, b := range l.Items[i+1:] {
			if footprintGap(a, b) < 0.6 {
				link("item:"+a.ID, "near", "item:"+b.ID)
			}
		}
	}

	// devices: where they are, what they act on, the points they publish
	for _, d := range devs {
		id := "device:" + d.ID
		class := deviceClass[d.Kind]
		if d.Kind == devices.Sensor && (containsAny(d.Name, "冰箱", "冷藏", "冷柜")) {
			class = "FridgeSensor"
		}
		node(id, class, d.Name, map[string]any{"kind": d.Kind, "source": d.Source, "topic": d.Topic})
		room := "room:" + d.Room
		if _, ok := fs.names[room]; !ok {
			room = "outside"
		}
		link(id, "deviceIn", room)
		if rel, ok := deviceRel[d.Kind]; ok && room != "outside" {
			link(id, rel, room)
		}
		if d.Kind == devices.Lock && d.Door != "" {
			link(id, "secures", "door:"+d.Door)
			link(id, "mountedOn", "door:"+d.Door)
		}
		if d.Kind == devices.Curtain {
			best, bestD := "", 1.0
			for _, wn := range h.Windows {
				if w, ok := h.Wall(wn.Wall); ok {
					if dd := dist(w.At(wn.At), home.P{d.X, d.Z}); dd < bestD {
						best, bestD = "window:"+wn.ID, dd
					}
				}
			}
			for _, dr := range h.Doors {
				if w, ok := h.Wall(dr.Wall); ok {
					if dd := dist(w.At(dr.At), home.P{d.X, d.Z}); dd < bestD {
						best, bestD = "door:"+dr.ID, dd
					}
				}
			}
			if best != "" {
				link(id, "covers", best)
			}
		}
		// on a wall: the nearest wall face within 15 cm
		if d.Kind == devices.AC || d.Kind == devices.Sensor || d.Kind == devices.Curtain {
			best, bestD := "", 0.15
			for _, w := range h.Walls {
				dd := math.Abs((d.X-w.A[0])*w.Normal()[0]+(d.Z-w.A[1])*w.Normal()[1]) - w.Thick/2
				t := (d.X-w.A[0])*w.Dir()[0] + (d.Z-w.A[1])*w.Dir()[1]
				if dd >= -0.02 && dd < bestD && t >= 0 && t <= w.Len() {
					best, bestD = w.ID, dd
				}
			}
			if best != "" {
				target := "wall:" + best
				if fid, ok := face[best+"@"+d.Room]; ok {
					target = fid
				}
				link(id, "mountedOn", target)
			}
		}
		for _, k := range sortedKeys(d.State) {
			pt := id + "/" + k
			node(pt, "Point", d.Name+" "+k, map[string]any{"topic": d.Topic, "field": k})
			link(id, "hasPoint", pt)
		}
	}
	return fs
}

func containsAny(s string, subs ...string) bool {
	for _, x := range subs {
		if strings.Contains(s, x) {
			return true
		}
	}
	return false
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

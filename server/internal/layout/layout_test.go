package layout

import (
	"math"
	"strings"
	"testing"

	"hometwin/internal/home"
	"hometwin/internal/ikea"
)

func prod(typ string, w, d, h float64) ikea.Product {
	return ikea.Product{Source: "manual", Name: typ, Type: typ, TypeLabel: typ, W: w, D: d, H: h}
}

func kinds(ws []Warning, id string) map[string]Warning {
	out := map[string]Warning{}
	for _, w := range ws {
		if w.Item == id || w.With == id {
			out[w.Kind] = w
		}
	}
	return out
}

func check(items ...Item) []Warning {
	return Check(home.Sample(), Layout{Home: "chengdu-89", Items: items})
}

func TestBookcaseAgainstWallIsFine(t *testing.T) {
	// BILLY 80×28×202 flat against the master bedroom's west wall, facing into the room
	ws := check(Item{ID: "billy", Product: prod("bookcase", 80, 28, 202), X: 0.12 + 0.14, Z: 6.0, Rot: 90})
	if len(ws) != 0 {
		t.Fatalf("want no warnings, got %+v", ws)
	}
}

func TestRotate(t *testing.T) {
	// turned 90°, a 0.8 wide, 0.28 deep box spans 0.28 in x and 0.8 in z
	p := rect(0, 0, 0.8, 0.28, 90)
	xlo, xhi := p.project(P{1, 0})
	zlo, zhi := p.project(P{0, 1})
	if math.Abs(xhi-xlo-0.28) > 1e-9 || math.Abs(zhi-zlo-0.8) > 1e-9 {
		t.Fatalf("x %.3f z %.3f", xhi-xlo, zhi-zlo)
	}
}

func TestCollision(t *testing.T) {
	ws := check(
		Item{ID: "a", Product: prod("table", 120, 80, 75), X: 6.0, Z: 4.0},
		Item{ID: "b", Product: prod("drawers", 50, 50, 90), X: 6.5, Z: 4.3},
	)
	w, ok := kinds(ws, "a")["collide"]
	if !ok || w.With != "b" {
		t.Fatalf("want a collision of a with b, got %+v", ws)
	}
}

func TestTouchingIsNotCollision(t *testing.T) {
	ws := check(
		Item{ID: "a", Product: prod("drawers", 80, 48, 100), X: 6.0, Z: 5.0},
		Item{ID: "b", Product: prod("drawers", 80, 48, 100), X: 6.8, Z: 5.0},
	)
	if _, ok := kinds(ws, "a")["collide"]; ok {
		t.Fatalf("side by side is not a collision: %+v", ws)
	}
}

func TestIntoWall(t *testing.T) {
	// a 2 m sofa pushed 10 cm into the living room's east wall (inner face at 8.28)
	ws := check(Item{ID: "s", Product: prod("sofa", 200, 90, 80), X: 8.28 - 0.45 + 0.10, Z: 6.0, Rot: 90})
	w, ok := kinds(ws, "s")["wall"]
	if !ok || w.With != "e" {
		t.Fatalf("want into wall e, got %+v", ws)
	}
	if !strings.Contains(w.Text, "10 cm") {
		t.Fatalf("depth: %s", w.Text)
	}
}

func TestAisleBetweenItems(t *testing.T) {
	// bed and wardrobe 60 cm apart: too narrow to walk
	ws := check(
		Item{ID: "bed", Product: prod("bed", 160, 200, 100), X: 1.5, Z: 5.9},
		Item{ID: "wr", Product: prod("wardrobe", 150, 58, 200), X: 1.5, Z: 5.9 + 1.0 + 0.6 + 0.29, Rot: 180},
	)
	w, ok := kinds(ws, "bed")["aisle"]
	if !ok || w.With != "wr" || !strings.Contains(w.Text, "60 cm") {
		t.Fatalf("want a 60 cm aisle warning, got %+v", ws)
	}
}

func TestWideAisleIsFine(t *testing.T) {
	ws := check(
		Item{ID: "bed", Product: prod("bed", 160, 200, 100), X: 1.5, Z: 5.9},
		Item{ID: "wr", Product: prod("wardrobe", 150, 58, 200), X: 1.5, Z: 5.9 + 1.0 + 0.9 + 0.29, Rot: 180},
	)
	for _, w := range ws {
		if w.Kind == "aisle" && w.With == "wr" {
			t.Fatalf("90 cm is enough: %+v", w)
		}
	}
}

func TestAisleToWall(t *testing.T) {
	// a desk 50 cm from the second bedroom's west wall, facing it along its length
	ws := check(Item{ID: "desk", Product: prod("desk", 120, 60, 75), X: 0.12 + 0.5 + 0.3, Z: 1.6, Rot: 90})
	w, ok := kinds(ws, "desk")["aisle"]
	if !ok || !strings.Contains(w.Text, "离墙只有 50 cm") {
		t.Fatalf("want 50 cm to wall, got %+v", ws)
	}
}

func TestGapThroughWallIsNotAisle(t *testing.T) {
	// two cabinets on either side of the master bedroom's east wall, 20 cm off it each
	ws := check(
		Item{ID: "in", Product: prod("drawers", 80, 40, 80), X: 3.54 - 0.2 - 0.2, Z: 6.0, Rot: 90},
		Item{ID: "out", Product: prod("drawers", 80, 40, 80), X: 3.66 + 0.2 + 0.2, Z: 6.0, Rot: 90},
	)
	for _, w := range ws {
		if w.Kind == "aisle" && (w.With == "out" || w.With == "in") {
			t.Fatalf("a wall between them: %+v", w)
		}
	}
}

func TestBlocksDoorSwing(t *testing.T) {
	// right inside the master bedroom door, where its leaf swings
	ws := check(Item{ID: "c", Product: prod("chair", 50, 50, 90), X: 3.1, Z: 5.0})
	w, ok := kinds(ws, "c")["door"]
	if !ok || w.With != "master" || !strings.Contains(w.Text, "主卧门") {
		t.Fatalf("want master door blocked, got %+v", ws)
	}
}

func TestSwingArcSide(t *testing.T) {
	h := home.Sample()
	for _, d := range h.Doors {
		arc := SwingArc(h, d)
		if d.Kind != "swing" {
			if arc != nil {
				t.Fatalf("%s is not a swing door", d.ID)
			}
			continue
		}
		// the far point of the arc lies in the room the door opens into
		mid := arc[len(arc)/2]
		r := h.RoomAt(mid)
		want := map[string]string{"entry": "entry", "second": "second", "bath": "bath", "master": "master"}[d.ID]
		if r == nil || r.ID != want {
			t.Fatalf("%s opens into %v, want %s", d.ID, r, want)
		}
	}
}

func TestBlocksWindow(t *testing.T) {
	tall := check(Item{ID: "w", Product: prod("wardrobe", 120, 50, 200), X: 1.8, Z: 8.28 - 0.25})
	w, ok := kinds(tall, "w")["window"]
	if !ok || !strings.Contains(w.Text, "主卧") || !strings.Contains(w.Text, "50 cm") {
		t.Fatalf("want master window blocked, got %+v", tall)
	}
	low := check(Item{ID: "b", Product: prod("tvbench", 120, 40, 40), X: 1.8, Z: 8.28 - 0.2})
	if _, ok := kinds(low, "b")["window"]; ok {
		t.Fatalf("a 40 cm bench under a 50 cm sill blocks nothing: %+v", low)
	}
}

func TestOutside(t *testing.T) {
	ws := check(Item{ID: "x", Product: prod("chair", 50, 50, 90), X: 12, Z: 3})
	if _, ok := kinds(ws, "x")["outside"]; !ok {
		t.Fatalf("want outside, got %+v", ws)
	}
}

func TestEntryDoor(t *testing.T) {
	// the front door is 96 cm, about 90 cm clear
	big := prod("box", 120, 100, 100)
	ws := check(Item{ID: "big", Product: big, X: 6, Z: 5})
	if w, ok := kinds(ws, "big")["entry"]; !ok || w.Level != "warn" || !strings.Contains(w.Text, "整件") {
		t.Fatalf("want a whole-piece warning, got %+v", ws)
	}
	// flat-packed: the boxes go in, the assembled bed would not
	bed := prod("bed", 176, 211, 100)
	bed.Packages = []ikea.Package{{W: 60, H: 20, L: 210, N: 1}, {W: 40, H: 10, L: 180, N: 1}}
	ws = check(Item{ID: "bed", Product: bed, X: 1.8, Z: 6.3})
	if w, ok := kinds(ws, "bed")["entry"]; !ok || w.Level != "info" {
		t.Fatalf("want an assemble-inside note, got %+v", ws)
	}
	// a package too big is a warning
	bed.Packages = append(bed.Packages, ikea.Package{W: 100, H: 95, L: 200, N: 1})
	ws = check(Item{ID: "bed", Product: bed, X: 1.8, Z: 6.3})
	if w, ok := kinds(ws, "bed")["entry"]; !ok || w.Level != "warn" || !strings.Contains(w.Text, "第 3 个包装") {
		t.Fatalf("want package 3 warning, got %+v", ws)
	}
	// a sofa 83 cm deep goes through on its back
	ws = check(Item{ID: "sofa", Product: prod("sofa", 228, 95, 83), X: 6, Z: 6})
	if _, ok := kinds(ws, "sofa")["entry"]; ok {
		t.Fatalf("an 83 cm sofa fits a 90 cm door: %+v", ws)
	}
}

func TestRugIgnored(t *testing.T) {
	ws := check(
		Item{ID: "rug", Product: prod("rug", 200, 300, 1), X: 6, Z: 6},
		Item{ID: "sofa", Product: prod("sofa", 228, 95, 83), X: 6, Z: 6},
	)
	if _, ok := kinds(ws, "rug")["collide"]; ok {
		t.Fatalf("a sofa on a rug is fine: %+v", ws)
	}
}

func TestDiningSet(t *testing.T) {
	// a table with chairs tucked under it on both sides: no collisions, no aisle across the table
	table := prod("table", 140, 80, 75)
	chair := prod("chair", 45, 52, 85)
	ws := check(
		Item{ID: "t", Product: table, X: 6.4, Z: 3.55},
		Item{ID: "c1", Product: chair, X: 6.05, Z: 3.0},
		Item{ID: "c2", Product: chair, X: 6.05, Z: 4.1, Rot: 180},
	)
	for _, w := range ws {
		if w.Kind == "collide" || (w.Kind == "aisle" && w.With != "") {
			t.Fatalf("dining set: %+v", w)
		}
	}
	// two sofas still collide
	ws = check(Item{ID: "a", Product: prod("sofa", 200, 90, 80), X: 6, Z: 6}, Item{ID: "b", Product: prod("sofa", 200, 90, 80), X: 6.5, Z: 6.2})
	if _, ok := kinds(ws, "a")["collide"]; !ok {
		t.Fatalf("sofas overlap: %+v", ws)
	}
}

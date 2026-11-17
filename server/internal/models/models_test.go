package models

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"hometwin/internal/ikea"
)

func TestSpecFor(t *testing.T) {
	cases := []struct {
		name string
		p    ikea.Product
		opt  string
		want int
		mat  string
	}{
		{"BILLY shelves from height", ikea.Product{Type: "bookcase", Name: "BILLY Bookcase - white 80x28x202 cm", W: 80, D: 28, H: 202, ColorHex: "#f2f1ec"}, "shelves", 4, "painted"},
		{"low BILLY", ikea.Product{Type: "bookcase", Name: "BILLY 毕利 书架", W: 40, D: 28, H: 106}, "shelves", 2, "painted"},
		{"KIVIK seats from width", ikea.Product{Type: "sofa", Name: "KIVIK Sofa - Tibbleby beige/gray", W: 228, D: 94.9, H: 82.9}, "seats", 3, "fabric"},
		{"loveseat", ikea.Product{Type: "sofa", Name: "KLIPPAN Loveseat", W: 180, D: 88, H: 66}, "seats", 2, "fabric"},
		{"sectional named", ikea.Product{Type: "sofa", Name: "KIVIK Sectional, 4-seat", W: 318, D: 95, H: 83}, "seats", 4, "fabric"},
		{"MALM 六斗 from Chinese name", ikea.Product{Type: "drawers", Name: "MALM 马尔姆 六斗抽屉柜 80x123 厘米", W: 80, D: 48, H: 123}, "drawers", 6, "painted"},
		{"chest of 4 drawers", ikea.Product{Type: "drawers", Name: "MALM Chest of 4 drawers - high-gloss white", W: 80, D: 48, H: 100}, "drawers", 4, "painted"},
		{"BRIMNES 3 doors", ikea.Product{Type: "wardrobe", Name: "BRIMNES Wardrobe with 3 doors - white 117x50x190 cm", W: 117, D: 50, H: 190}, "doors", 3, "painted"},
		{"双门衣柜", ikea.Product{Type: "wardrobe", Name: "BRIMNES 百灵 双门衣柜", W: 78, D: 50, H: 190}, "doors", 2, "painted"},
		{"wardrobe doors by width", ikea.Product{Type: "wardrobe", Name: "PAX Wardrobe", W: 150, D: 58, H: 236}, "doors", 3, "painted"},
		{"veneer bed is wood", ikea.Product{Type: "bed", Name: "MALM Bed frame - dark-brown veneer Queen", W: 168, D: 211, H: 100}, "", 0, "wood"},
		{"POÄNG birch legs", ikea.Product{Type: "armchair", Name: "POÄNG Armchair - birch veneer/Knisa light beige", W: 68, D: 82, H: 100}, "", 0, "fabric"},
	}
	for _, c := range cases {
		s := SpecFor(c.p)
		if c.opt != "" && s.Options[c.opt] != c.want {
			t.Errorf("%s: %s = %v, want %d", c.name, c.opt, s.Options[c.opt], c.want)
		}
		if s.Material != c.mat {
			t.Errorf("%s: material %s, want %s", c.name, s.Material, c.mat)
		}
	}
	p := SpecFor(ikea.Product{Type: "armchair", Name: "POÄNG Armchair - birch veneer", W: 68, D: 82, H: 100,
		Measures: []ikea.Measure{{Key: "seatHeight", CM: 41.9}, {Key: "maxLoad", KG: 110}}})
	if p.Options["legs"] != "wood" || p.Measures["seatHeight"] != 41.9 {
		t.Errorf("POÄNG: %+v", p)
	}
	if _, ok := p.Measures["maxLoad"]; ok {
		t.Error("a load in kg is not a size")
	}
}

func TestKey(t *testing.T) {
	a := Spec{Type: "sofa", W: 228, D: 94.9, H: 82.9, Color: "#CDBFA8", Material: "fabric", Options: map[string]any{"seats": 3, "legs": "metal"}, Measures: map[string]float64{"seatHeight": 45.1, "seatDepth": 60}}
	b := Spec{Type: "sofa", W: 228.01, D: 94.9, H: 82.9, Color: "#cdbfa8", Material: "fabric", Options: map[string]any{"legs": "metal", "seats": 3}, Measures: map[string]float64{"seatDepth": 60, "seatHeight": 45.1}}
	if Key(a) != Key(b) {
		t.Fatalf("same spec, different keys: %s %s", Key(a), Key(b))
	}
	if !keyRe.MatchString(Key(a)) || !strings.HasPrefix(Key(a), "sofa-") {
		t.Fatalf("key shape: %s", Key(a))
	}
	b.W = 200
	if Key(a) == Key(b) {
		t.Fatal("a different width must change the key")
	}
}

func TestUnavailable(t *testing.T) {
	t.Setenv("BLENDER", "")
	b := New("", t.TempDir())
	if b.Available() {
		t.Fatal("no Blender configured")
	}
	if _, err := b.Build(context.Background(), Spec{Type: "box", W: 10, D: 10, H: 10}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("want ErrUnavailable, got %v", err)
	}
	mux := http.NewServeMux()
	mux.Handle("/api/models", b.Handler())
	mux.Handle("/api/models/", b.Handler())
	srv := httptest.NewServer(mux)
	defer srv.Close()
	body := `{"type":"bookcase","name":"BILLY","w":80,"d":28,"h":202,"colorHex":"#f2f1ec"}`
	res, err := http.Post(srv.URL+"/api/models", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var a Answer
	json.NewDecoder(res.Body).Decode(&a)
	res.Body.Close()
	if res.StatusCode != 200 || a.Available || a.Ready || a.Spec.Options["shelves"] == nil {
		t.Fatalf("status %d answer %+v", res.StatusCode, a)
	}
	res, _ = http.Get(srv.URL + a.URL)
	if res.StatusCode != 404 {
		t.Fatalf("GET unbuilt: %d", res.StatusCode)
	}
	res, _ = http.Get(srv.URL + "/api/models/../../etc/passwd")
	if res.StatusCode == 200 {
		t.Fatal("path escape")
	}
	res, _ = http.Post(srv.URL+"/api/models", "application/json", strings.NewReader(`{"spec":{"type":"box","w":0,"d":1,"h":1}}`))
	if res.StatusCode != 400 {
		t.Fatalf("zero width: %d", res.StatusCode)
	}
}

// glbBox reads a GLB's bounding box (metres, glTF axes) from its nodes and POSITION accessors.
func glbBox(t *testing.T, path string) (min, max [3]float64) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data[:4]) != "glTF" {
		t.Fatal("not a GLB")
	}
	n := binary.LittleEndian.Uint32(data[12:16])
	var doc struct {
		Nodes []struct {
			Mesh        *int      `json:"mesh"`
			Translation []float64 `json:"translation"`
			Scale       []float64 `json:"scale"`
			Rotation    []float64 `json:"rotation"`
			Children    []int     `json:"children"`
		} `json:"nodes"`
		Meshes []struct {
			Primitives []struct {
				Attributes map[string]int `json:"attributes"`
			} `json:"primitives"`
		} `json:"meshes"`
		Accessors []struct {
			Min []float64 `json:"min"`
			Max []float64 `json:"max"`
		} `json:"accessors"`
	}
	if err := json.Unmarshal(data[20:20+n], &doc); err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		min[i], max[i] = math.Inf(1), math.Inf(-1)
	}
	for _, nd := range doc.Nodes {
		if nd.Mesh == nil {
			continue
		}
		if len(nd.Rotation) == 4 && math.Abs(nd.Rotation[3]) < 0.9999 {
			t.Fatalf("node is rotated: %v", nd.Rotation)
		}
		tr, sc := []float64{0, 0, 0}, []float64{1, 1, 1}
		if len(nd.Translation) == 3 {
			tr = nd.Translation
		}
		if len(nd.Scale) == 3 {
			sc = nd.Scale
		}
		for _, p := range doc.Meshes[*nd.Mesh].Primitives {
			acc := doc.Accessors[p.Attributes["POSITION"]]
			for i := range 3 {
				min[i] = math.Min(min[i], acc.Min[i]*sc[i]+tr[i])
				max[i] = math.Max(max[i], acc.Max[i]*sc[i]+tr[i])
			}
		}
	}
	return
}

func TestBuildWithBlender(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	bin := os.Getenv("BLENDER")
	if bin == "" {
		bin = "/Applications/Blender.app/Contents/MacOS/Blender"
	}
	if _, err := os.Stat(bin); err != nil {
		t.Skip("no Blender")
	}
	b := New(bin, t.TempDir())
	specs := []Spec{
		{Type: "bookcase", W: 80, D: 28, H: 202, Color: "#f2f1ec", Material: "painted"},
		{Type: "sofa", W: 228, D: 95, H: 83, Color: "#cdbfa8", Material: "fabric", Measures: map[string]float64{"seatHeight": 45}},
		{Type: "bed", W: 168, D: 211, H: 100, Color: "#5a3b28", Material: "wood"},
		{Type: "desk", W: 105, D: 50, H: 75, Color: "#f2f1ec", Material: "painted"},
		{Type: "armchair", W: 68, D: 82, H: 100, Color: "#cdbfa8", Material: "fabric", Options: map[string]any{"legs": "wood"}},
		{Type: "wardrobe", W: 117, D: 50, H: 190, Color: "#f2f1ec", Material: "painted", Options: map[string]any{"doors": 3}},
		{Type: "drawers", W: 80, D: 48, H: 123, Color: "#f2f1ec", Material: "painted", Options: map[string]any{"drawers": 6}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	type res struct {
		s    Spec
		path string
		err  error
	}
	out := make(chan res, len(specs))
	for _, s := range specs {
		go func() {
			p, err := b.Build(ctx, s)
			out <- res{s, p, err}
		}()
	}
	for range specs {
		r := <-out
		if r.err != nil {
			t.Errorf("%s: %v", r.s.Type, r.err)
			continue
		}
		lo, hi := glbBox(t, r.path)
		size := [3]float64{(hi[0] - lo[0]) * 100, (hi[1] - lo[1]) * 100, (hi[2] - lo[2]) * 100}
		want := [3]float64{r.s.W, r.s.H, r.s.D} // glTF: x width, y height, z depth
		for i := range 3 {
			if math.Abs(size[i]-want[i]) > 1 {
				t.Errorf("%s: size %.1f×%.1f×%.1f cm (W×H×D), want %.0f×%.0f×%.0f", r.s.Type, size[0], size[1], size[2], want[0], want[1], want[2])
				break
			}
		}
		if math.Abs(lo[1]) > 0.005 || math.Abs(lo[0]+hi[0]) > 0.01 || math.Abs(lo[2]+hi[2]) > 0.01 {
			t.Errorf("%s: origin is not the floor centre: %v %v", r.s.Type, lo, hi)
		}
		t.Logf("%s: %.1f×%.1f×%.1f cm", r.s.Type, size[0], size[2], size[1])
	}
	// built once, then cached
	start := time.Now()
	if _, err := b.Build(ctx, specs[0]); err != nil || time.Since(start) > time.Second {
		t.Fatalf("cached build: %v in %s", err, time.Since(start))
	}
}

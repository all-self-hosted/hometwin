package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"hometwin/internal/home"
)

var rooms = []home.Room{{ID: "bedA", Name: "卧室A"}, {ID: "bed", Name: "卧室"}, {ID: "living", Name: "客厅"}}

func TestUnderstandWithoutModel(t *testing.T) {
	c := &Catalog{}
	cases := []struct{ text, query, room string }{
		{"卧室A 放一个床头柜", "bedside table", "bedA"},
		{"客厅加一盆绿植", "houseplant", "living"},
		{"卧室里放个衣柜", "wardrobe", "bed"},
		{"upright piano", "upright piano", ""},
		{"客厅来一个沙发", "sofa", "living"},
	}
	for _, tc := range cases {
		in, err := c.Understand(context.Background(), tc.text, rooms)
		if err != nil {
			t.Fatalf("%s: %v", tc.text, err)
		}
		if in.Query != tc.query || in.Room != tc.room {
			t.Errorf("%s: got %+v, want %q in %q", tc.text, in, tc.query, tc.room)
		}
	}
	if _, err := c.Understand(context.Background(), "随便来点什么", rooms); err == nil {
		t.Error("a sentence with no furniture in it should not be understood")
	}
}

func fake(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	nightstand := map[string]any{
		"id": "bedside-table-3f2b80", "title": "Bedside Table", "free": true, "triangles": 302,
		"page": "https://polyfork.dev/asset/bedside-table-3f2b80", "thumbnail": "https://polyfork.dev/t.png",
		"size_m": map[string]float64{"x": 0.5, "y": 0.55, "z": 0.45},
		"credit": "Bedside Table by Polyfork (https://polyfork.dev/asset/bedside-table-3f2b80), CC BY 4.0",
	}
	mux.HandleFunc("GET /api/assets", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("free") != "1" {
			t.Error("search should ask for free models only")
		}
		json.NewEncoder(w).Encode(map[string]any{"assets": []any{
			nightstand,
			map[string]any{"id": "hd-only-a1b2c3", "title": "Fancy", "free": false, "size_m": map[string]float64{"x": 1, "y": 1, "z": 1}},
		}})
	})
	mux.HandleFunc("GET /api/assets/{id}", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(nightstand)
	})
	mux.HandleFunc("GET /cdn/{file}", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("glTF\x02\x00\x00\x00rest"))
	})
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func TestSearchAndProduct(t *testing.T) {
	s := fake(t)
	c := New(t.TempDir(), nil)
	c.Base = s.URL
	hits, err := c.Search(context.Background(), "bedside table", 12)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Type != "drawers" || hits[0].W != 50 || hits[0].D != 45 || hits[0].H != 55 {
		t.Fatalf("hits: %+v", hits)
	}
	p, err := c.Product(context.Background(), "bedside-table-3f2b80")
	if err != nil {
		t.Fatal(err)
	}
	if p.Source != "polyfork" || p.Model != "/api/catalog/model/bedside-table-3f2b80.glb" || p.Credit == "" || p.W != 50 {
		t.Fatalf("product: %+v", p)
	}
	if b, err := os.ReadFile(c.File("bedside-table-3f2b80")); err != nil || string(b[:4]) != "glTF" {
		t.Fatalf("model not kept: %v", err)
	}
	if _, err := c.Product(context.Background(), "../etc/passwd"); err == nil {
		t.Error("a bad id should be refused")
	}
}

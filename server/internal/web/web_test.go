package web

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"hometwin/internal/automate"
	"hometwin/internal/devices"
	"hometwin/internal/home"
	"hometwin/internal/insight"
	"hometwin/internal/layout"
	"hometwin/internal/ontology"
	"hometwin/internal/predict"
)

func server(t *testing.T) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	h := home.Sample()
	store, _ := layout.NewStore(dir)
	hub, _ := devices.NewHub(h.ID, dir, nil)
	sim := devices.NewSim(h)
	hub.Use(sim)
	auto, _ := automate.New(hub, dir)
	onto, err := ontology.Open(context.Background(), dir+"/home.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { onto.Close() })
	if err := onto.Sync(context.Background(), h, layout.Layout{Home: h.ID}, hub.List()); err != nil {
		t.Fatal(err)
	}
	s := &Server{Onto: onto, Home: h, Layouts: store, Devices: hub, Sim: sim, Auto: auto, Insight: insight.New(nil),
		Estimate: predict.NewEstimator(h), Static: fstest.MapFS{"index.html": {Data: []byte("<html>twin</html>")}},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), HTTP: http.DefaultClient}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s.Wire(ctx)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func call(t *testing.T, ts *httptest.Server, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, ts.URL+path, rd)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	b, _ := io.ReadAll(res.Body)
	json.Unmarshal(b, &out)
	return res.StatusCode, out
}

func TestCommandResponses(t *testing.T) {
	ts := server(t)
	if code, _ := call(t, ts, "POST", "/api/devices/light-living/command", map[string]any{"patch": map[string]any{"on": true}}); code != 200 {
		t.Fatal(code)
	}
	code, out := call(t, ts, "POST", "/api/devices/lock-entry/command", map[string]any{"patch": map[string]any{"locked": false}})
	if code != 409 || !strings.Contains(out["confirm"].(string), "确定要打开") {
		t.Fatalf("%d %v", code, out)
	}
	if code, _ := call(t, ts, "POST", "/api/devices/lock-entry/command", map[string]any{"patch": map[string]any{"locked": false}, "confirm": true}); code != 200 {
		t.Fatal(code)
	}
	code, out = call(t, ts, "POST", "/api/devices/ac-living/command", map[string]any{"patch": map[string]any{"target": 12}})
	if code != 422 || !strings.Contains(out["error"].(string), "16～30") {
		t.Fatalf("%d %v", code, out)
	}
}

func TestLayoutSaveAndCheck(t *testing.T) {
	ts := server(t)
	item := map[string]any{"id": "c", "x": 3.1, "z": 5.0, "rot": 0, "product": map[string]any{"name": "椅子", "type": "chair", "w": 50, "d": 50, "h": 90}}
	code, out := call(t, ts, "PUT", "/api/layout", map[string]any{"items": []any{item}, "base": 0})
	if code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	ws := out["warnings"].([]any)
	if len(ws) == 0 || ws[0].(map[string]any)["kind"] != "door" {
		t.Fatalf("want the master door warning: %v", ws)
	}
	// saving on an old revision is refused
	if code, _ := call(t, ts, "PUT", "/api/layout", map[string]any{"items": []any{}, "base": 0}); code != 409 {
		t.Fatalf("stale save: %d", code)
	}
	code, out = call(t, ts, "POST", "/api/layout/check", map[string]any{"items": []any{item}})
	if code != 200 || len(out["warnings"].([]any)) == 0 {
		t.Fatalf("%d %v", code, out)
	}
}

func TestImportFromPage(t *testing.T) {
	f, err := os.Open("../ikea/testdata/billy.html.gz")
	if err != nil {
		t.Skip("fixture missing")
	}
	zr, _ := gzip.NewReader(f)
	page, _ := io.ReadAll(zr)
	shop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(page) }))
	defer shop.Close()
	ts := server(t)
	code, out := call(t, ts, "POST", "/api/furniture/import", map[string]any{"url": shop.URL + "/us/en/p/billy-bookcase-white-20522046/"})
	if code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	p := out["product"].(map[string]any)
	if p["type"] != "bookcase" || p["w"].(float64) < 79 || p["w"].(float64) > 81 {
		t.Fatalf("%v", p)
	}
	code, out = call(t, ts, "POST", "/api/furniture/manual", map[string]any{"name": "旧书桌", "w": 120, "d": 60, "h": 75})
	if code != 200 || out["product"].(map[string]any)["type"] != "desk" {
		t.Fatalf("%d %v", code, out)
	}
}

func TestPredictAndAsk(t *testing.T) {
	ts := server(t)
	call(t, ts, "POST", "/api/devices/ac-master/command", map[string]any{"patch": map[string]any{"on": true, "mode": "cool", "target": 22}})
	code, out := call(t, ts, "GET", "/api/predict/ac-master", nil)
	if code != 200 || out["eta"] == nil || len(out["curve"].([]any)) < 10 {
		t.Fatalf("%d %v", code, out)
	}
	code, out = call(t, ts, "GET", "/api/graph/ask?q=客厅有哪些设备", nil)
	if code != 200 || !strings.Contains(out["text"].(string), "客厅空调") {
		t.Fatalf("%d %v", code, out)
	}
	if code, out := call(t, ts, "GET", "/api/twin", nil); code != 200 || len(out["levels"].([]any)) != 5 {
		t.Fatalf("%d %v", code, out)
	}
}

func TestStaticFallsBackToIndex(t *testing.T) {
	ts := server(t)
	res, _ := http.Get(ts.URL + "/some/page")
	b, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(b), "twin") {
		t.Fatal(string(b))
	}
	if code, _ := call(t, ts, "GET", "/api/nope", nil); code != 404 {
		t.Fatal(code)
	}
}

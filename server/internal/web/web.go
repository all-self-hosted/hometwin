// Package web serves the twin: the front end, a JSON API and one WebSocket that keeps every
// open page in step (devices, layout, hints, automations).
package web

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"math"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"hometwin/internal/automate"
	"hometwin/internal/catalog"
	"hometwin/internal/devices"
	"hometwin/internal/home"
	"hometwin/internal/ikea"
	"hometwin/internal/insight"
	"hometwin/internal/layout"
	"hometwin/internal/ontology"
	"hometwin/internal/predict"
	"hometwin/internal/twin"
)

type Server struct {
	Home     home.Home
	Layouts  *layout.Store
	Devices  *devices.Hub
	Sim      *devices.Sim
	Insight  *insight.Engine
	Auto     *automate.Engine
	Estimate *predict.Estimator
	// Onto is the home's knowledge graph and ontology (CortexDB).
	Onto *ontology.Store
	// Models answers /api/models (generated furniture models); nil means none.
	Models http.Handler
	// HA answers /api/ha/ (接入 Home Assistant); nil means none.
	HA http.Handler
	// Assistant answers /api/assistant/ (the 家庭助手); nil means none.
	Assistant http.Handler
	// Floorplan answers /api/floorplan (导入户型图); nil means none.
	Floorplan http.Handler
	// Scan answers /api/scan (导入 VR 看房); nil means none.
	Scan http.Handler
	// VRImport turns on importing VR scans (POST /api/scan); scanned homes already made
	// are shown either way.
	VRImport bool
	// Catalog finds ready 3D models (Polyfork) from a sentence; nil means none.
	Catalog *catalog.Catalog
	Static  fs.FS
	Log     *slog.Logger
	HTTP    *http.Client
	// MQTT is the broker's address, shown to people ("" when off).
	MQTT string

	mu      sync.Mutex
	clients map[*client]struct{}
	resync  chan struct{}
	lastV   string
}

type client struct {
	send chan []byte
}

// Wire connects the parts: every change reaches the browsers, the hints and the automations.
func (s *Server) Wire(ctx context.Context) {
	s.clients = map[*client]struct{}{}
	reactions := make(chan devices.Event, 256)
	s.Devices.Listen(func(e devices.Event) {
		s.broadcast(e)
		if e.Type == "device" && e.Device != nil && e.Device.Kind == devices.Sensor {
			if t, ok := e.Device.State["temp"].(float64); ok {
				s.Estimate.Observe(e.Device.Room, t, time.Now())
			}
		}
		select {
		case reactions <- e:
		default:
		}
	})
	s.Insight.Listen(func(all []insight.Hint, started *insight.Hint) {
		s.broadcast(map[string]any{"type": "hints", "hints": all, "started": started})
		if started != nil {
			go s.Auto.OnHint(ctx, *started)
		}
	})
	s.resync = make(chan struct{}, 1)
	go s.keepGraph(ctx)
	s.Auto.Listen(func() {
		s.broadcast(map[string]any{"type": "automations", "automations": s.Auto.Automations(), "proposals": s.Auto.Proposals()})
	})
	// the sensors' starting values seed the estimator
	for _, d := range s.Devices.List() {
		if t, ok := d.State["temp"].(float64); ok && d.Kind == devices.Sensor {
			s.Estimate.Observe(d.Room, t, time.Now())
		}
	}
	go func() {
		tick := time.NewTicker(5 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case e := <-reactions:
				s.Insight.Observe(e)
				s.Auto.OnDevice(ctx, e)
				if e.Type == "removed" {
					s.Resync()
				}
			case <-tick.C:
				s.Insight.Tick()
				s.Auto.Tick(ctx)
			}
		}
	}()
}

// Resync asks for the knowledge graph to be rebuilt from the home, the layout and the devices
// (it runs once for a burst of changes).
func (s *Server) Resync() {
	if s.resync == nil {
		return
	}
	select {
	case s.resync <- struct{}{}:
	default:
	}
}

// keepGraph rebuilds the knowledge graph after changes, and checks the live constraints on the
// devices' readings every few seconds.
func (s *Server) keepGraph(ctx context.Context) {
	if s.Onto == nil {
		return
	}
	s.Resync()
	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.resync:
			time.Sleep(400 * time.Millisecond) // let a burst of changes settle
			l, _ := s.Layouts.Layout(s.Home.ID)
			if err := s.Onto.Sync(ctx, s.Home, l, s.Devices.List()); err != nil {
				s.Log.Warn("knowledge graph", "err", err)
			}
			s.Onto.Observe(s.Devices.List(), time.Now())
			s.sendConstraints(ctx)
		case <-tick.C:
			s.Onto.Observe(s.Devices.List(), time.Now())
			s.sendConstraints(ctx)
		}
	}
}

// sendConstraints tells the pages what the ontology's constraints find, when that changed.
func (s *Server) sendConstraints(ctx context.Context) {
	vs, err := s.Onto.Check(ctx)
	if err != nil {
		return
	}
	b, _ := json.Marshal(vs)
	s.mu.Lock()
	same := string(b) == s.lastV
	s.lastV = string(b)
	s.mu.Unlock()
	if !same {
		s.broadcast(map[string]any{"type": "constraints", "constraints": vs})
	}
}

func (s *Server) broadcast(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for c := range s.clients {
		select {
		case c.send <- b:
		default: // a slow page loses updates rather than holding up the rest
		}
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/home", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, s.Home) })
	mux.HandleFunc("GET /api/twin", s.twin)
	mux.HandleFunc("GET /api/features", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]bool{"vrImport": s.VRImport && s.Scan != nil})
	})
	mux.HandleFunc("GET /api/layout", s.getLayout)
	mux.HandleFunc("PUT /api/layout", s.putLayout)
	mux.HandleFunc("POST /api/layout/check", s.checkLayout)
	mux.HandleFunc("POST /api/furniture/import", s.importFurniture)
	mux.HandleFunc("POST /api/furniture/manual", s.manualFurniture)
	mux.HandleFunc("GET /api/library", s.library)
	mux.HandleFunc("DELETE /api/library/{n}", s.dropLibrary)
	if s.Catalog != nil {
		mux.HandleFunc("POST /api/catalog/find", s.findModels)
		mux.HandleFunc("POST /api/catalog/add", s.addModel)
		mux.HandleFunc("GET /api/catalog/model/{file}", s.catalogModel)
	}
	mux.HandleFunc("GET /api/devices", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, s.Devices.List()) })
	mux.HandleFunc("POST /api/devices", s.putDevice)
	mux.HandleFunc("PUT /api/devices/{id}", s.putDevice)
	mux.HandleFunc("DELETE /api/devices/{id}", s.dropDevice)
	mux.HandleFunc("POST /api/devices/{id}/command", s.command)
	mux.HandleFunc("GET /api/predict/{id}", s.predict)
	mux.HandleFunc("GET /api/hints", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, s.Insight.Active()) })
	mux.HandleFunc("GET /api/automations", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"automations": s.Auto.Automations(), "proposals": s.Auto.Proposals()})
	})
	mux.HandleFunc("PUT /api/automations/{id}", s.setAutomation)
	mux.HandleFunc("POST /api/automations/{id}/run", s.runAutomation)
	mux.HandleFunc("POST /api/proposals/{id}/{decision}", s.decide)
	mux.HandleFunc("GET /api/graph", s.graph)
	mux.HandleFunc("GET /api/graph/ask", s.ask)
	mux.HandleFunc("GET /api/graph/explain", s.explain)
	mux.HandleFunc("GET /api/constraints", s.constraints)
	mux.HandleFunc("POST /api/sim/{name}", s.scenario)
	mux.HandleFunc("GET /api/ws", s.ws)
	models := s.Models
	if models == nil {
		models = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 200, map[string]any{"available": false})
		})
	}
	if s.HA != nil {
		mux.Handle("/api/ha/", s.HA)
	}
	if s.Assistant != nil {
		mux.Handle("/api/assistant/", s.Assistant)
	}
	if s.Floorplan != nil {
		mux.Handle("/api/floorplan", s.Floorplan)
		mux.Handle("/api/floorplan/", s.Floorplan)
	}
	if s.Scan != nil {
		mux.HandleFunc("/api/scan", func(w http.ResponseWriter, r *http.Request) {
			if !s.VRImport {
				fail(w, 403, "VR 导入没有开启（启动时加 -vr-import）")
				return
			}
			s.Scan.ServeHTTP(w, r)
		})
		mux.Handle("/api/scan/", s.Scan)
	}
	mux.Handle("/api/models", models)
	mux.Handle("/api/models/", models)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { fail(w, 404, "没有这个接口") })
	mux.Handle("/", s.static())
	return mux
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func read(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 4<<20))
	if err := dec.Decode(v); err != nil {
		return errors.New("请求的内容不是有效的 JSON")
	}
	return nil
}

func tab(r *http.Request) string { return r.Header.Get("X-Tab") }

func (s *Server) twin(w http.ResponseWriter, r *http.Request) {
	l, _ := s.Layouts.Layout(s.Home.ID)
	points := 0
	for _, d := range s.Devices.List() {
		points += len(d.State)
	}
	writeJSON(w, 200, map[string]any{
		"goals":  twin.Goals,
		"levels": twin.Levels,
		"model": map[string]int{
			"rooms": len(s.Home.Rooms), "walls": len(s.Home.Walls), "doors": len(s.Home.Doors), "windows": len(s.Home.Windows),
			"furniture": len(l.Items), "devices": len(s.Devices.List()), "points": points,
		},
		"mqtt": s.MQTT,
	})
}

func (s *Server) getLayout(w http.ResponseWriter, r *http.Request) {
	l, err := s.Layouts.Layout(s.Home.ID)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"layout": l, "warnings": layout.Check(s.Home, l)})
}

func (s *Server) putLayout(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Items []layout.Item `json:"items"`
		Base  int           `json:"base"`
	}
	if err := read(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	for _, it := range body.Items {
		if it.ID == "" || it.Product.W <= 0 || it.Product.D <= 0 || it.Product.H <= 0 {
			fail(w, 400, "每件家具要有 id 和尺寸")
			return
		}
	}
	l, err := s.Layouts.Save(layout.Layout{Home: s.Home.ID, Items: body.Items}, body.Base)
	if errors.Is(err, layout.ErrStale) {
		writeJSON(w, 409, map[string]any{"error": err.Error(), "layout": l})
		return
	}
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.broadcast(map[string]any{"type": "layout", "layout": l, "by": tab(r)})
	s.Resync()
	writeJSON(w, 200, map[string]any{"layout": l, "warnings": layout.Check(s.Home, l)})
}

func (s *Server) checkLayout(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Items []layout.Item `json:"items"`
	}
	if err := read(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"warnings": layout.Check(s.Home, layout.Layout{Home: s.Home.ID, Items: body.Items})})
}

func (s *Server) importFurniture(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL string `json:"url"`
	}
	if err := read(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	p, err := ikea.Fetch(ctx, s.HTTP, strings.TrimSpace(body.URL))
	if err != nil {
		s.Log.Info("import failed", "url", body.URL, "err", err)
		fail(w, 422, err.Error())
		return
	}
	lib, err := s.Layouts.AddToLibrary(s.Home.ID, p)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"product": p, "library": lib})
}

func (s *Server) manualFurniture(w http.ResponseWriter, r *http.Request) {
	var p ikea.Product
	if err := read(r, &p); err != nil {
		fail(w, 400, err.Error())
		return
	}
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		fail(w, 400, "给它起个名字")
		return
	}
	for _, v := range []float64{p.W, p.D, p.H} {
		if v < 1 || v > 600 || math.IsNaN(v) {
			fail(w, 400, "宽、深、高要在 1～600 cm 之间")
			return
		}
	}
	if p.Type == "" {
		p.Type = ikea.DetectType(p.Name)
	}
	p.Source, p.DimsFrom, p.URL = "manual", "manual", ""
	p.TypeLabel = ikea.TypeLabel(p.Type)
	if p.ColorHex == "" {
		p.ColorHex = ikea.ColorHex(p.Color, p.Type)
	}
	lib, err := s.Layouts.AddToLibrary(s.Home.ID, p)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"product": p, "library": lib})
}

func (s *Server) library(w http.ResponseWriter, r *http.Request) {
	lib, err := s.Layouts.Library(s.Home.ID)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, lib)
}

func (s *Server) dropLibrary(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil {
		fail(w, 400, "要第几件？")
		return
	}
	lib, err := s.Layouts.RemoveFromLibrary(s.Home.ID, n)
	if err != nil {
		fail(w, 404, err.Error())
		return
	}
	writeJSON(w, 200, lib)
}

func (s *Server) putDevice(w http.ResponseWriter, r *http.Request) {
	var d devices.Device
	if err := read(r, &d); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if id := r.PathValue("id"); id != "" {
		d.ID = id
	}
	if d.Name == "" {
		fail(w, 400, "设备要有名字")
		return
	}
	if rm := s.Home.RoomAt(home.P{d.X, d.Z}); rm != nil && d.Room == "" {
		d.Room = rm.ID
	}
	out, err := s.Devices.Put(d, tab(r))
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	s.Resync()
	writeJSON(w, 200, out)
}

func (s *Server) dropDevice(w http.ResponseWriter, r *http.Request) {
	if err := s.Devices.Remove(r.PathValue("id"), tab(r)); err != nil {
		fail(w, 404, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) command(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Patch   map[string]any `json:"patch"`
		Confirm bool           `json:"confirm"`
	}
	if err := read(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	d, err := s.Devices.Command(r.Context(), r.PathValue("id"), body.Patch, body.Confirm, tab(r))
	var nc *devices.NeedConfirm
	var rf *devices.Refused
	switch {
	case errors.As(err, &nc):
		writeJSON(w, 409, map[string]string{"confirm": nc.Text})
	case errors.As(err, &rf):
		fail(w, 422, rf.Text)
	case errors.Is(err, devices.ErrNotFound):
		fail(w, 404, err.Error())
	case err != nil:
		fail(w, 502, err.Error())
	default:
		writeJSON(w, 200, d)
	}
}

// predict answers the AC card: where the room is, how long the AC takes to the target, and
// the curve for another target when asked for (?target=22).
func (s *Server) predict(w http.ResponseWriter, r *http.Request) {
	d, ok := s.Devices.Get(r.PathValue("id"))
	if !ok || d.Kind != devices.AC {
		fail(w, 404, "没有这台空调")
		return
	}
	model, ok := s.Estimate.Model(d.Room)
	if !ok {
		fail(w, 404, "这台空调不在任何房间里")
		return
	}
	t, outdoor, ok := s.Estimate.Temp(d.Room)
	if !ok {
		if c, ok2 := d.State["current"].(float64); ok2 {
			t = c
		} else {
			fail(w, 503, "这个房间还没有温度读数")
			return
		}
	}
	mode, _ := d.State["mode"].(string)
	target, _ := d.State["target"].(float64)
	if v, err := strconv.ParseFloat(r.URL.Query().Get("target"), 64); err == nil {
		target = v
	}
	if m := r.URL.Query().Get("mode"); m != "" {
		mode = m
	}
	out := map[string]any{"room": d.Room, "now": math.Round(t*10) / 10, "outdoor": math.Round(outdoor*10) / 10, "target": target, "mode": mode}
	speed := 1.0
	if s.Sim != nil && s.Sim.Speed > 0 {
		speed = s.Sim.Speed
	}
	if eta, ok := model.ETA(t, target, outdoor, mode); ok {
		out["eta"] = math.Round(eta / speed)
	} else {
		out["eta"] = nil
	}
	horizon := 3600.0
	pts := model.Simulate(t, outdoor, true, mode, target, horizon, 120)
	for i := range pts {
		pts[i].S = math.Round(pts[i].S / speed)
	}
	out["curve"] = pts
	writeJSON(w, 200, out)
}

func (s *Server) setAutomation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Level   automate.Level `json:"level"`
		Enabled bool           `json:"enabled"`
	}
	if err := read(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	a, err := s.Auto.SetLevel(r.PathValue("id"), body.Level, body.Enabled)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, a)
}

func (s *Server) runAutomation(w http.ResponseWriter, r *http.Request) {
	p, err := s.Auto.Run(r.Context(), r.PathValue("id"), "手动运行")
	if err != nil {
		fail(w, 404, err.Error())
		return
	}
	writeJSON(w, 200, p)
}

func (s *Server) decide(w http.ResponseWriter, r *http.Request) {
	dec := r.PathValue("decision")
	if dec != "approve" && dec != "reject" {
		fail(w, 404, "只能 approve 或 reject")
		return
	}
	who := r.Header.Get("X-Name")
	if who == "" {
		who = "家里人"
	}
	p, err := s.Auto.Decide(r.Context(), r.PathValue("id"), dec == "approve", who)
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, p)
}

func (s *Server) graph(w http.ResponseWriter, r *http.Request) {
	if s.Onto == nil {
		fail(w, 503, "知识图谱没有打开")
		return
	}
	if r.URL.Query().Get("format") == "jsonld" {
		ld, err := s.Onto.JSONLD(r.Context())
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/ld+json; charset=utf-8")
		json.NewEncoder(w).Encode(ld)
		return
	}
	nodes, edges, err := s.Onto.Graph(r.Context())
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"nodes": nodes, "edges": edges, "examples": ontology.Examples})
}

// Ask answers a question about the home from its knowledge graph.
func (s *Server) Ask(ctx context.Context, q string) (ontology.Answer, error) {
	if s.Onto == nil {
		return ontology.Answer{}, errors.New("知识图谱没有打开")
	}
	return s.Onto.Ask(ctx, q)
}

func (s *Server) ask(w http.ResponseWriter, r *http.Request) {
	a, err := s.Ask(r.Context(), r.URL.Query().Get("q"))
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, a)
}

// explain says why an inferred fact holds: ?s=device:ac-living&p=affects&o=room:living
func (s *Server) explain(w http.ResponseWriter, r *http.Request) {
	if s.Onto == nil {
		fail(w, 503, "知识图谱没有打开")
		return
	}
	q := r.URL.Query()
	text, err := s.Onto.Explain(r.Context(), q.Get("s"), q.Get("p"), q.Get("o"))
	if err != nil {
		fail(w, 404, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"text": text})
}

func (s *Server) constraints(w http.ResponseWriter, r *http.Request) {
	if s.Onto == nil {
		writeJSON(w, 200, []any{})
		return
	}
	vs, err := s.Onto.Check(r.Context())
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, vs)
}

func (s *Server) scenario(w http.ResponseWriter, r *http.Request) {
	if s.Sim == nil {
		fail(w, 404, "没有模拟的家")
		return
	}
	if err := s.Sim.Scenario(s.Devices, r.PathValue("name")); err != nil {
		fail(w, 404, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) ws(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionContextTakeover})
	if err != nil {
		return
	}
	defer c.CloseNow()
	ctx := c.CloseRead(r.Context())
	cl := &client{send: make(chan []byte, 256)}
	l, _ := s.Layouts.Layout(s.Home.ID)
	snap, _ := json.Marshal(map[string]any{
		"type": "snapshot", "devices": s.Devices.List(), "layout": l, "hints": s.Insight.Active(),
		"automations": s.Auto.Automations(), "proposals": s.Auto.Proposals(),
	})
	s.mu.Lock()
	s.clients[cl] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.clients, cl)
		s.mu.Unlock()
	}()
	if err := c.Write(ctx, websocket.MessageText, snap); err != nil {
		return
	}
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case b := <-cl.send:
			wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := c.Write(wctx, websocket.MessageText, b)
			cancel()
			if err != nil {
				return
			}
		case <-ping.C:
			if err := c.Ping(ctx); err != nil {
				return
			}
		}
	}
}

// static serves the built front end; any path that is not a file gets index.html.
func (s *Server) static() http.Handler {
	files := http.FileServerFS(s.Static)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(s.Static, p); err != nil {
			p = "index.html"
			r = r.Clone(r.Context())
			r.URL.Path = "/"
		}
		if strings.HasPrefix(p, "assets/") || strings.HasPrefix(p, "draco/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}

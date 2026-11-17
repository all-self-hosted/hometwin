package ontology

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hometwin/internal/devices"
	"hometwin/internal/home"
	"hometwin/internal/ikea"
	"hometwin/internal/layout"
)

func sampleLayout() layout.Layout {
	billy := ikea.Product{Name: "BILLY Bookcase", Family: "BILLY", Type: "bookcase", TypeLabel: "书柜", W: 80, D: 28, H: 202, Article: "205.220.46"}
	kivik := ikea.Product{Name: "KIVIK Sofa", Family: "KIVIK", Type: "sofa", TypeLabel: "沙发", W: 228, D: 95, H: 83, Article: "394.405.93"}
	return layout.Layout{Home: "chengdu-89", Items: []layout.Item{
		// against the master bedroom's west wall, back to it
		{ID: "billy", Product: billy, X: 0.12 + 0.14, Z: 6.0, Rot: 90},
		// back against the living room's west wall (the master bedroom's east wall)
		{ID: "kivik", Product: kivik, X: 3.66 + 0.475, Z: 6.2, Rot: 90},
	}}
}

func open(t *testing.T, h home.Home, l layout.Layout, ds []devices.Device) (*Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "home.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	start := time.Now()
	if err := s.Sync(ctx, h, l, ds); err != nil {
		t.Fatal(err)
	}
	ex, inf := s.Stats()
	t.Logf("sync: %d given, %d inferred, %v (%s)", ex, inf, time.Since(start).Round(time.Millisecond), SyncTiming)
	return s, ctx
}

func sample(t *testing.T) (*Store, context.Context) {
	ds := devices.Defaults("chengdu-89")
	for i := range ds {
		if ds[i].ID == "light-living" {
			ds[i].State["on"] = true
		}
	}
	return open(t, home.Sample(), sampleLayout(), ds)
}

func (s *Store) has(ctx context.Context, sub, rel, obj string) bool {
	return len(s.find(ctx, ref(thing(sub)), ref(ht(rel)), ref(thing(obj)))) > 0
}

func TestReasoning(t *testing.T) {
	s, ctx := sample(t)
	// subclasses: a 卧室 is a 房间 is a 空间, and a RealEstateCore Room
	for _, c := range []string{"Bedroom", "Room", "Space"} {
		if !s.isA(ctx, "room:master", c) {
			t.Errorf("主卧 should be a %s", c)
		}
	}
	if len(s.find(ctx, ref(thing("room:master")), &iriType, ref(rec("Room")))) == 0 {
		t.Error("主卧 should be a rec:Room")
	}
	if !s.isA(ctx, "item:billy", "Storage") || !s.isA(ctx, "item:billy", "Furniture") {
		t.Error("BILLY is a 书柜, so 柜类 and 家具")
	}
	if !s.isA(ctx, "device:light-living", "Device") {
		t.Error("a 照明 is a 设备")
	}
	checks := [][3]string{
		// transitive 位于: BILLY in the master bedroom in the home
		{"item:billy", "locatedIn", "home:chengdu-89"},
		{"item:billy", "against", "wallface:w@master"},
		{"item:kivik", "against", "wallface:master-e@living"},
		// devices: subproperty, then the chain
		{"device:ac-living", "locatedIn", "room:living"},
		{"device:ac-living", "homeDevice", "home:chengdu-89"},
		{"device:ac-living", "affects", "room:living"},
		{"device:lock-entry", "affects", "room:entry"},
		{"device:curtain-living", "affects", "room:balcony"},
		{"room:living", "controlledBy", "device:light-living"},
		// doors: 通往 both ways
		{"room:master", "leadsTo", "room:living"},
		{"room:living", "leadsTo", "room:master"},
		{"room:entry", "leadsTo", "outside"},
		{"room:master", "adjacentTo", "room:bath"},
		{"device:sensor-bath", "measures", "room:bath"},
		{"item:billy", "modelOf", "product:205.220.46"},
	}
	for _, c := range checks {
		if !s.has(ctx, c[0], c[1], c[2]) {
			t.Errorf("missing %s %s %s", c[0], c[1], c[2])
		}
	}
	if s.has(ctx, "room:master", "leadsTo", "room:second") {
		t.Error("主卧 and 次卧 share no door")
	}
}

func TestExplain(t *testing.T) {
	s, ctx := sample(t)
	why, err := s.Explain(ctx, "客厅空调", "能影响", "客餐厅")
	if err != nil {
		t.Fatal(err)
	}
	t.Log(why)
	if !strings.Contains(why, "客厅空调 能影响 客餐厅") || !strings.Contains(why, "因为") || !strings.Contains(why, "控制") {
		t.Fatalf("explain: %s", why)
	}
	why, _ = s.Explain(ctx, "item:billy", "位于", "home:chengdu-89")
	t.Log(why)
	if !strings.Contains(why, "BILLY 书柜 位于 主卧") || !strings.Contains(why, "主卧 位于") {
		t.Fatalf("explain: %s", why)
	}
	why, _ = s.Explain(ctx, "主卧", "是", "空间")
	t.Log(why)
	if !strings.Contains(why, "卧室 是一种 房间") {
		t.Fatalf("explain: %s", why)
	}
	why, _ = s.Explain(ctx, "item:billy", "位于", "主卧")
	if !strings.Contains(why, "给定的事实") {
		t.Fatalf("explain given: %s", why)
	}
}

func TestStructuralConstraints(t *testing.T) {
	s, ctx := sample(t)
	vs, _ := s.Check(ctx)
	for _, v := range vs {
		if v.Kind == "structural" {
			t.Fatalf("the sample passes SHACL, got %+v", v)
		}
	}
	// without the balcony's light, and with the second bedroom's door gone
	h := home.Sample()
	var doors []home.Door
	for _, d := range h.Doors {
		if d.ID != "second" {
			doors = append(doors, d)
		}
	}
	h.Doors = doors
	var ds []devices.Device
	for _, d := range devices.Defaults("chengdu-89") {
		if d.ID != "light-balcony" {
			ds = append(ds, d)
		}
	}
	s2, ctx2 := open(t, h, sampleLayout(), ds)
	vs, _ = s2.Check(ctx2)
	got := map[string]Violation{}
	for _, v := range vs {
		if v.Kind == "structural" {
			got[v.Constraint+"/"+v.Node] = v
		}
	}
	light, ok := got["房间要有照明/room:balcony"]
	if !ok || !strings.Contains(light.Text, "阳台没有照明") || !strings.Contains(light.Why, "约束「房间要有照明」") {
		t.Fatalf("no-light: %+v", vs)
	}
	door, ok := got["卧室要有门/room:second"]
	if !ok || !strings.Contains(door.Text, "次卧没有门") {
		t.Fatalf("no-door: %+v", vs)
	}
	if len(got) != 2 {
		t.Fatalf("exactly two broken: %+v", got)
	}
	a, _ := s2.Ask(ctx2, "哪些房间没有照明")
	if !strings.Contains(a.Text, "阳台") {
		t.Fatalf("ask: %s", a.Text)
	}
}

func TestLiveHumidity(t *testing.T) {
	s, _ := sample(t)
	ds := devices.Defaults("chengdu-89")
	bath := func(rh float64) []devices.Device {
		for i := range ds {
			if ds[i].ID == "sensor-bath" {
				ds[i].State["humidity"] = rh
			}
		}
		return ds
	}
	t0 := time.Date(2026, 10, 17, 20, 0, 0, 0, time.Local)
	if vs := s.Observe(bath(85), t0); len(vs) != 0 {
		t.Fatalf("just went over: %+v", vs)
	}
	if vs := s.Observe(bath(86), t0.Add(90*time.Second)); len(vs) != 0 {
		t.Fatalf("90 s is not 2 min: %+v", vs)
	}
	vs := s.Observe(bath(88), t0.Add(125*time.Second))
	if len(vs) != 1 || vs[0].Constraint != "卫生间要通风" || !strings.Contains(vs[0].Text, "卫生间湿度 88%") || !strings.Contains(vs[0].Why, "卫生间温湿度 测量 卫生间") {
		t.Fatalf("after 2 min: %+v", vs)
	}
	// back under: the clock starts again
	s.Observe(bath(70), t0.Add(130*time.Second))
	if vs := s.Observe(bath(85), t0.Add(140*time.Second)); len(vs) != 0 {
		t.Fatalf("restarted: %+v", vs)
	}
	// the living room's sensor is not in a bathroom
	for i := range ds {
		if ds[i].ID == "sensor-living" {
			ds[i].State["humidity"] = 95.0
		}
	}
	for _, v := range s.Observe(ds, t0.Add(600*time.Second)) {
		if v.Node == "device:sensor-living" {
			t.Fatalf("living room has no ventilation rule: %+v", v)
		}
	}
}

func TestFridgeRule(t *testing.T) {
	ds := devices.Defaults("chengdu-89")
	ds = append(ds, devices.Device{ID: "fridge-temp", Kind: devices.Sensor, Name: "冰箱冷藏温度", Room: "kitchen", X: 6, Y: 1, Z: 0.5, Source: "sim", State: map[string]any{"temp": 4.0}})
	s, _ := open(t, home.Sample(), sampleLayout(), ds)
	ds[len(ds)-1].State = map[string]any{"temp": 11.0}
	t0 := time.Now()
	s.Observe(ds, t0)
	vs := s.Observe(ds, t0.Add(6*time.Minute))
	if len(vs) != 1 || vs[0].Constraint != "冷藏温度" || !strings.Contains(vs[0].Text, "冰箱冷藏温度温度 11 ℃") {
		t.Fatalf("%+v", vs)
	}
}

func TestLayoutConstraints(t *testing.T) {
	l := sampleLayout()
	// a chair right inside the master bedroom door, where its leaf swings
	l.Items = append(l.Items, layout.Item{ID: "c", Product: ikea.Product{Name: "椅子", Type: "chair", TypeLabel: "椅子", W: 50, D: 50, H: 90}, X: 3.1, Z: 5.0})
	s, ctx := open(t, home.Sample(), l, devices.Defaults("chengdu-89"))
	vs, _ := s.Check(ctx)
	found := false
	for _, v := range vs {
		if v.Kind == "layout" && v.Node == "item:c" && v.Constraint == "门的开启范围不能挡" && strings.Contains(v.Text, "主卧门") {
			found = true
		}
	}
	if !found {
		t.Fatalf("door swing: %+v", vs)
	}
	a, _ := s.Ask(ctx, "有什么问题")
	if !strings.Contains(a.Text, "不满足约束") || len(a.Rows) == 0 {
		t.Fatalf("issues: %+v", a)
	}
}

func TestAsk(t *testing.T) {
	s, ctx := sample(t)
	s.Observe(devices.Defaults("chengdu-89"), time.Now())
	// the light was on when synced; Observe above replaced the states with the defaults (off)
	ds := devices.Defaults("chengdu-89")
	for i := range ds {
		if ds[i].ID == "light-living" {
			ds[i].State["on"] = true
		}
	}
	s.Observe(ds, time.Now())
	cases := map[string][]string{
		"客厅有哪些设备":     {"客餐厅有", "客厅空调", "客厅主灯", "扫地机器人"},
		"主卧有哪些家具？":    {"主卧有 1 件家具", "BILLY 书柜"},
		"书柜靠哪面墙":      {"BILLY 书柜靠着主卧西墙（背靠）"},
		"KIVIK靠哪面墙":   {"客餐厅西墙"},
		"主卧连着哪些房间":    {"客餐厅（经主卧门）"},
		"哪些灯开着":       {"1 盏", "客厅主灯"},
		"卫生间多少度":      {"卫生间现在", "℃"},
		"入户门锁管什么":     {"锁住入户门"},
		"沙发在哪":        {"KIVIK 沙发在客餐厅"},
		"阳台有什么家具":     {"阳台还没有家具"},
		"客厅空调能影响哪些房间": {"客厅空调能影响 客餐厅"},
		"窗帘能影响哪些房间":   {"阳台"},
		"哪些房间没有照明":    {"每个房间都有照明"},
		"今天天气怎么样":     {"答不上来"},
	}
	for q, want := range cases {
		a, err := s.Ask(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s → %s", q, a.Text)
		for _, w := range want {
			if !strings.Contains(a.Text, w) {
				t.Errorf("%s → %s (want %q)", q, a.Text, w)
			}
		}
	}
	a, _ := s.Ask(ctx, "客厅空调能影响哪些房间")
	if len(a.Rows) == 0 || a.Rows[0].Detail == "" || a.Nodes[0] != "device:ac-living" {
		t.Fatalf("affects rows should explain: %+v", a)
	}
}

func TestGraphAndJSONLD(t *testing.T) {
	s, ctx := sample(t)
	nodes, edges, err := s.Graph(ctx)
	if err != nil || len(nodes) < 50 || len(edges) < 50 {
		t.Fatalf("%d nodes %d edges %v", len(nodes), len(edges), err)
	}
	inferred := 0
	for _, e := range edges {
		if e.Props["inferred"] == true {
			inferred++
		}
	}
	if inferred == 0 {
		t.Fatal("no inferred edges")
	}
	ld, _ := s.JSONLD(ctx)
	for _, o := range ld["@graph"].([]map[string]any) {
		if o["@id"] == "ht:thing/room:master" {
			types := strings.Join(o["@type"].([]string), " ")
			if !strings.Contains(types, "ht:Bedroom") || !strings.Contains(types, "rec:Room") {
				t.Fatalf("types %s", types)
			}
			return
		}
	}
	t.Fatal("主卧 not exported")
}

func TestSyncIsIdempotentAndFollowsChanges(t *testing.T) {
	s, ctx := sample(t)
	ex1, _ := s.Stats()
	if err := s.Sync(ctx, home.Sample(), sampleLayout(), devices.Defaults("chengdu-89")); err != nil {
		t.Fatal(err)
	}
	if ex2, _ := s.Stats(); ex2 != ex1 {
		t.Fatalf("same input, %d then %d facts", ex1, ex2)
	}
	l := sampleLayout()
	l.Items = l.Items[:1]
	start := time.Now()
	if err := s.Sync(ctx, home.Sample(), l, devices.Defaults("chengdu-89")); err != nil {
		t.Fatal(err)
	}
	t.Logf("resync after a change: %v", time.Since(start).Round(time.Millisecond))
	t.Logf("%s", SyncTiming)
	a, _ := s.Ask(ctx, "沙发在哪")
	if strings.Contains(a.Text, "KIVIK") {
		t.Fatalf("the sofa was taken out: %s", a.Text)
	}
	// and what was inferred from it went with it
	if s.has(ctx, "item:kivik", "locatedIn", "home:chengdu-89") || s.has(ctx, "item:kivik", "locatedIn", "room:living") {
		t.Fatal("stale inference about the sofa")
	}
	if !s.has(ctx, "item:billy", "locatedIn", "home:chengdu-89") {
		t.Fatal("BILLY still in the home")
	}
}

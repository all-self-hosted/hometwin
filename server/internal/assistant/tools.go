package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"hometwin/internal/automate"
	"hometwin/internal/devices"
	"hometwin/internal/home"
	"hometwin/internal/ikea"
	"hometwin/internal/layout"
)

// tool is one thing the assistant can do in the home.
type tool struct {
	name, desc string
	params     map[string]any
	// label says in a few words what a call did, for the chat ("查了 客餐厅 的设备").
	label func(a args) string
	run   func(ctx context.Context, a args) (any, error)
}

type args map[string]any

func (a args) str(k string) string {
	switch v := a[k].(type) {
	case string:
		return strings.TrimSpace(v)
	case nil:
		return ""
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

// num reads a number the model may send as a number or a string.
func (a args) num(k string) (float64, bool) {
	switch v := a[k].(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimRight(v, "%℃°度")), 64)
		return f, err == nil
	}
	return 0, false
}

func (a args) boolean(k string) (bool, bool) {
	switch v := a[k].(type) {
	case bool:
		return v, true
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "on", "开", "是", "1":
			return true, true
		case "false", "off", "关", "否", "0":
			return false, true
		}
	}
	return false, false
}

func obj(props map[string]any, required ...string) map[string]any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{"type": "object", "properties": props, "required": required}
}

func prop(typ, desc string) map[string]any { return map[string]any{"type": typ, "description": desc} }

func enum(desc string, values ...string) map[string]any {
	return map[string]any{"type": "string", "enum": values, "description": desc}
}

var roomAliases = map[string]string{"客厅": "living", "餐厅": "living", "客餐厅": "living", "大卧室": "master", "主卧室": "master", "小卧室": "second", "次卧室": "second", "厕所": "bath", "洗手间": "bath", "浴室": "bath", "门口": "entry"}

// room finds a room by id, name or a common way of saying it.
func (t *Tools) room(s string) (home.Room, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return home.Room{}, false
	}
	if id, ok := roomAliases[s]; ok {
		s = id
	}
	for _, r := range t.d.Home.Rooms {
		if r.ID == s || r.Name == s {
			return r, true
		}
	}
	for _, r := range t.d.Home.Rooms {
		if strings.Contains(s, r.Name) || strings.Contains(r.Name, s) {
			return r, true
		}
	}
	for alias, id := range roomAliases {
		if strings.Contains(s, alias) {
			for _, r := range t.d.Home.Rooms {
				if r.ID == id {
					return r, true
				}
			}
		}
	}
	return home.Room{}, false
}

func (t *Tools) roomName(id string) string {
	if id == "outdoors" {
		return "室外"
	}
	for _, r := range t.d.Home.Rooms {
		if r.ID == id {
			return r.Name
		}
	}
	return id
}

// device finds a device by id or name.
func (t *Tools) device(s string) (devices.Device, error) {
	s = strings.TrimSpace(s)
	all := t.d.Devices.List()
	for _, d := range all {
		if d.ID == s || d.Name == s {
			return d, nil
		}
	}
	var hits []devices.Device
	for _, d := range all {
		if s != "" && (strings.Contains(d.Name, s) || strings.Contains(s, d.Name)) {
			hits = append(hits, d)
		}
	}
	if len(hits) == 1 {
		return hits[0], nil
	}
	names := make([]string, 0, len(all))
	for _, d := range all {
		names = append(names, fmt.Sprintf("%s（%s）", d.Name, d.ID))
	}
	if len(hits) > 1 {
		names = names[:0]
		for _, d := range hits {
			names = append(names, fmt.Sprintf("%s（%s）", d.Name, d.ID))
		}
		return devices.Device{}, fmt.Errorf("「%s」能对上好几个设备：%s，请用 id", s, strings.Join(names, "、"))
	}
	return devices.Device{}, fmt.Errorf("没有「%s」这个设备，有：%s", s, strings.Join(names, "、"))
}

var kindName = map[string]string{devices.Light: "灯", devices.AC: "空调", devices.Lock: "门锁", devices.Curtain: "窗帘", devices.Vacuum: "扫地机器人", devices.Sensor: "温湿度传感器"}

// state says in words what a device is doing.
func state(d devices.Device) string {
	s := d.State
	num := func(k string) float64 { v, _ := s[k].(float64); return v }
	switch d.Kind {
	case devices.Light:
		if s["on"] == true {
			return fmt.Sprintf("开着，亮度 %.0f%%，色温 %.0fK", num("brightness"), num("ct"))
		}
		return "关着"
	case devices.AC:
		mode := map[string]string{"cool": "制冷", "heat": "制热", "dry": "除湿", "fan": "送风", "auto": "自动"}[fmt.Sprint(s["mode"])]
		cur := ""
		if _, ok := s["current"]; ok {
			cur = fmt.Sprintf("，室内 %.1f ℃", num("current"))
		}
		if s["on"] == true {
			return fmt.Sprintf("开着，%s %.1f ℃%s", mode, num("target"), cur)
		}
		return "关着" + cur
	case devices.Lock:
		out := "已上锁"
		if s["locked"] != true {
			out = "没锁"
		}
		if s["open"] == true {
			out += "，门开着"
		}
		return out
	case devices.Curtain:
		return fmt.Sprintf("拉开 %.0f%%", num("position"))
	case devices.Vacuum:
		ph := map[string]string{"docked": "在座上", "cleaning": "在打扫", "returning": "在回座", "paused": "暂停"}[fmt.Sprint(s["phase"])]
		return fmt.Sprintf("%s，电量 %.0f%%", ph, num("battery"))
	case devices.Sensor:
		return fmt.Sprintf("%.1f ℃，湿度 %.0f%%", num("temp"), num("humidity"))
	}
	return ""
}

func (t *Tools) sensorIn(room string) (devices.Device, bool) {
	for _, d := range t.d.Devices.List() {
		if d.Kind == devices.Sensor && d.Room == room {
			return d, true
		}
	}
	return devices.Device{}, false
}

func (t *Tools) itemsIn(room string, l layout.Layout) []layout.Item {
	var out []layout.Item
	for _, it := range l.Items {
		if r := t.d.Home.RoomAt(home.P{it.X, it.Z}); r != nil && (room == "" || r.ID == room) {
			out = append(out, it)
		}
	}
	return out
}

// wallOf says which wall of its room an item stands against ("北墙"), or "".
func (t *Tools) wallOf(it layout.Item) string {
	r := t.d.Home.RoomAt(home.P{it.X, it.Z})
	if r == nil {
		return ""
	}
	xs, zs := []float64{}, []float64{}
	for _, p := range r.Poly {
		xs = append(xs, p[0])
		zs = append(zs, p[1])
	}
	// half-extents of the turned footprint
	rad := it.Rot * math.Pi / 180
	hw := math.Abs(math.Cos(rad))*it.Product.W/200 + math.Abs(math.Sin(rad))*it.Product.D/200
	hd := math.Abs(math.Sin(rad))*it.Product.W/200 + math.Abs(math.Cos(rad))*it.Product.D/200
	minX, maxX, minZ, maxZ := slicesMin(xs), slicesMax(xs), slicesMin(zs), slicesMax(zs)
	gaps := map[string]float64{"西墙": it.X - hw - minX, "东墙": maxX - (it.X + hw), "北墙": it.Z - hd - minZ, "南墙": maxZ - (it.Z + hd)}
	best, bestGap := "", 0.2
	for name, g := range gaps {
		if g < bestGap {
			best, bestGap = name, g
		}
	}
	return best
}

func slicesMin(v []float64) float64 {
	m := math.Inf(1)
	for _, x := range v {
		m = math.Min(m, x)
	}
	return m
}
func slicesMax(v []float64) float64 {
	m := math.Inf(-1)
	for _, x := range v {
		m = math.Max(m, x)
	}
	return m
}

func sizeText(p ikea.Product) string { return fmt.Sprintf("%.0f×%.0f×%.0f cm", p.W, p.D, p.H) }

// product finds a product: a link (imported when it is not in the library yet), or a name,
// series or kind of something in the library.
func (t *Tools) product(ctx context.Context, s string) (ikea.Product, error) {
	s = strings.TrimSpace(s)
	lib, err := t.d.Layouts.Library(t.d.Home.ID)
	if err != nil {
		return ikea.Product{}, err
	}
	if id, ok := strings.CutPrefix(s, "polyfork:"); ok {
		for _, p := range lib {
			if p.Source == "polyfork" && strings.HasSuffix(p.URL, "/"+id) {
				return p, nil
			}
		}
		return t.addModel(ctx, id)
	}
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		for _, p := range lib {
			if p.URL == s {
				return p, nil
			}
		}
		return t.importURL(ctx, s)
	}
	low := strings.ToLower(s)
	for _, p := range lib {
		if strings.EqualFold(p.Family, s) || p.Name == s {
			return p, nil
		}
	}
	for _, p := range lib {
		if strings.Contains(strings.ToLower(p.Name), low) || strings.Contains(low, strings.ToLower(p.Family)) && p.Family != "" || strings.Contains(s, p.TypeLabel) && p.TypeLabel != "" {
			return p, nil
		}
	}
	if len(lib) == 0 {
		return ikea.Product{}, errors.New("家具库是空的：先给一个宜家商品链接")
	}
	names := []string{}
	for _, p := range lib {
		names = append(names, layout.Label(p))
	}
	return ikea.Product{}, fmt.Errorf("家具库里没有「%s」，有：%s；也可以直接给商品链接", s, strings.Join(names, "、"))
}

// addModel downloads a model from the catalogue and puts it in the library.
func (t *Tools) addModel(ctx context.Context, id string) (ikea.Product, error) {
	if t.d.Catalog == nil {
		return ikea.Product{}, errors.New("没有接模型库")
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	p, err := t.d.Catalog.Product(ctx, id)
	if err != nil {
		return p, err
	}
	if _, err := t.d.Layouts.AddToLibrary(t.d.Home.ID, p); err != nil {
		return p, err
	}
	return p, nil
}

func (t *Tools) importURL(ctx context.Context, url string) (ikea.Product, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	p, err := ikea.Fetch(ctx, t.d.HTTP, url)
	if err != nil {
		return p, err
	}
	if _, err := t.d.Layouts.AddToLibrary(t.d.Home.ID, p); err != nil {
		return p, err
	}
	return p, nil
}

func productInfo(p ikea.Product) map[string]any {
	out := map[string]any{"名称": layout.Label(p), "商品名": p.Name, "尺寸": sizeText(p), "类型": p.TypeLabel}
	if p.Price > 0 {
		out["价格"] = fmt.Sprintf("%g %s", p.Price, p.Currency)
	}
	if len(p.Packages) > 0 {
		out["包装数"] = len(p.Packages)
	}
	for _, m := range p.Measures {
		if m.Key == "seatHeight" {
			out["座高"] = fmt.Sprintf("%.0f cm", m.CM)
		}
	}
	return out
}

// Spot is where a piece would stand.
type Spot struct {
	X, Z, Rot float64
	Room      string
	Warnings  []layout.Warning
}

const candidateID = "_candidate"

// warningsFor checks the layout with p standing at (x, z, rot) and keeps what concerns it.
func (t *Tools) warningsFor(l layout.Layout, p ikea.Product, x, z, rot float64) []layout.Warning {
	items := append(append([]layout.Item{}, l.Items...), layout.Item{ID: candidateID, Product: p, X: x, Z: z, Rot: rot})
	var out []layout.Warning
	for _, w := range layout.Check(t.d.Home, layout.Layout{Home: t.d.Home.ID, Items: items}) {
		if w.Item == candidateID || w.With == candidateID {
			out = append(out, w)
		}
	}
	return out
}

func serious(ws []layout.Warning) int {
	n := 0
	for _, w := range ws {
		if w.Level == "warn" {
			n++
		}
	}
	return n
}

// findSpot tries places in a room with the piece's back against a wall, from the middle of
// the longest walls outward, and returns the first one without a warning (or the one with the
// fewest).
func (t *Tools) findSpot(l layout.Layout, p ikea.Product, r home.Room) Spot {
	type edge struct {
		a, b home.P
		len  float64
	}
	var edges []edge
	for i := range r.Poly {
		a, b := r.Poly[i], r.Poly[(i+1)%len(r.Poly)]
		edges = append(edges, edge{a, b, math.Hypot(b[0]-a[0], b[1]-a[1])})
	}
	sort.SliceStable(edges, func(i, j int) bool { return edges[i].len > edges[j].len })
	best := Spot{Room: r.ID}
	bestN := math.MaxInt
	W, D := p.W/100, p.D/100
	for _, e := range edges {
		if e.len < W+0.05 {
			continue
		}
		ux, uz := (e.b[0]-e.a[0])/e.len, (e.b[1]-e.a[1])/e.len
		nx, nz := -uz, ux
		mid := home.P{(e.a[0] + e.b[0]) / 2, (e.a[1] + e.b[1]) / 2}
		if !home.Inside(r.Poly, home.P{mid[0] + nx*0.3, mid[1] + nz*0.3}) {
			nx, nz = -nx, -nz
		}
		// the piece faces into the room: its front, local +z, along the inward normal
		rot := math.Round(math.Atan2(nx, nz)*180/math.Pi*100) / 100
		half := (e.len - W) / 2
		for _, inset := range []float64{0.065, 0.125} {
			for k := 0; ; k++ {
				off := float64((k+1)/2) * 0.2
				if k%2 == 1 {
					off = -off
				}
				if math.Abs(off) > half+1e-9 {
					break
				}
				x := mid[0] + ux*off + nx*(inset+D/2)
				z := mid[1] + uz*off + nz*(inset+D/2)
				if rr := t.d.Home.RoomAt(home.P{x, z}); rr == nil || rr.ID != r.ID {
					continue
				}
				ws := t.warningsFor(l, p, x, z, rot)
				n := serious(ws)
				if n < bestN {
					best = Spot{X: math.Round(x*1000) / 1000, Z: math.Round(z*1000) / 1000, Rot: rot, Room: r.ID, Warnings: ws}
					bestN = n
				}
				if n == 0 {
					return best
				}
			}
		}
	}
	return best
}

func warnTexts(ws []layout.Warning) []string {
	out := []string{}
	for _, w := range ws {
		out = append(out, w.Text)
	}
	return out
}

// patchFrom collects the settings a set_device call asks for.
func patchFrom(a args) map[string]any {
	p := map[string]any{}
	for _, k := range []string{"on", "locked"} {
		if v, ok := a.boolean(k); ok {
			p[k] = v
		}
	}
	for _, k := range []string{"brightness", "ct", "target", "position"} {
		if v, ok := a.num(k); ok {
			p[k] = v
		}
	}
	for _, k := range []string{"mode", "fan", "phase"} {
		if v := a.str(k); v != "" {
			p[k] = v
		}
	}
	return p
}

// Tools are the assistant's tools over one home.
type Tools struct {
	d     Deps
	emit  func(ctx context.Context, e Event)
	tools []tool
}

func newTools(d Deps, emit func(ctx context.Context, e Event)) *Tools {
	t := &Tools{d: d, emit: emit}
	t.tools = t.build()
	return t
}

// call runs one tool by name, for the agent and for tests.
func (t *Tools) call(ctx context.Context, name string, a map[string]any) (any, error) {
	for _, tl := range t.tools {
		if tl.name == name {
			return tl.run(ctx, args(a))
		}
	}
	return nil, fmt.Errorf("没有工具 %s", name)
}

func (t *Tools) names() []string {
	out := make([]string, 0, len(t.tools))
	for _, tl := range t.tools {
		out = append(out, tl.name)
	}
	return out
}

func (t *Tools) labelOf(name string, a map[string]any) string {
	for _, tl := range t.tools {
		if tl.name == name && tl.label != nil {
			return tl.label(args(a))
		}
	}
	return name
}

func (t *Tools) build() []tool {
	roomOr := func(a args, all string) string {
		if r, ok := t.room(a.str("room")); ok {
			return r.Name
		}
		return all
	}
	devProps := map[string]any{
		"device":     prop("string", "设备的 id 或名字，比如 light-living 或 客厅主灯"),
		"on":         prop("boolean", "灯、空调：开 true / 关 false"),
		"brightness": prop("number", "灯的亮度 1～100（%）"),
		"ct":         prop("number", "灯的色温 2700～6500（K），2700 暖黄、4000 自然、6500 日光"),
		"mode":       enum("空调模式", "cool", "heat", "dry", "fan", "auto"),
		"target":     prop("number", "空调设定温度 16～30（℃）"),
		"fan":        enum("空调风速", "auto", "low", "mid", "high"),
		"locked":     prop("boolean", "门锁：上锁 true / 开锁 false（开锁一定要家里人确认）"),
		"position":   prop("number", "窗帘拉开的比例 0～100（%），0 是全拉上"),
		"phase":      enum("扫地机器人：cleaning 开始打扫、paused 暂停、returning 回座充电", "cleaning", "paused", "returning"),
	}
	placeProps := map[string]any{
		"product": prop("string", "家具：宜家商品链接、家具库里的名字/系列（如 KIVIK、BILLY 书柜），或 find_models 找到的 polyfork:<id>"),
		"room":    prop("string", "放进哪个房间（id 或名字，如 客厅、主卧）"),
		"x":       prop("number", "可选：中心点 x（米，向东）"),
		"z":       prop("number", "可选：中心点 z（米，向南）"),
		"rot":     prop("number", "可选：转角（度），0 时正面朝南"),
	}

	return []tool{
		{name: "list_rooms", desc: "列出家里的房间：面积、温度和湿度、有几个设备几件家具。",
			params: obj(map[string]any{}),
			label:  func(args) string { return "看了所有房间" },
			run: func(context.Context, args) (any, error) {
				l, err := t.d.Layouts.Layout(t.d.Home.ID)
				if err != nil {
					return nil, err
				}
				devs := t.d.Devices.List()
				out := []map[string]any{}
				for _, r := range t.d.Home.Rooms {
					m := map[string]any{"id": r.ID, "名字": r.Name, "面积": fmt.Sprintf("%.1f ㎡", home.Area(r.Poly)), "家具": len(t.itemsIn(r.ID, l))}
					n := 0
					for _, d := range devs {
						if d.Room == r.ID {
							n++
						}
					}
					m["设备"] = n
					if s, ok := t.sensorIn(r.ID); ok {
						m["温湿度"] = state(s)
					}
					out = append(out, m)
				}
				return map[string]any{"家": t.d.Home.Name, "房间": out}, nil
			}},
		{name: "list_furniture", desc: "列出家具：名字、尺寸、在哪个房间、靠哪面墙。不给房间就列全部。",
			params: obj(map[string]any{"room": prop("string", "可选：房间 id 或名字")}),
			label:  func(a args) string { return "查了" + roomOr(a, "全屋") + "的家具" },
			run: func(_ context.Context, a args) (any, error) {
				l, err := t.d.Layouts.Layout(t.d.Home.ID)
				if err != nil {
					return nil, err
				}
				room := ""
				if s := a.str("room"); s != "" {
					r, ok := t.room(s)
					if !ok {
						return nil, fmt.Errorf("没有「%s」这个房间", s)
					}
					room = r.ID
				}
				out := []map[string]any{}
				for _, it := range t.itemsIn(room, l) {
					m := map[string]any{"id": it.ID, "名字": layout.Label(it.Product), "尺寸": sizeText(it.Product), "房间": t.roomName(t.d.Home.RoomAt(home.P{it.X, it.Z}).ID), "位置": fmt.Sprintf("x %.2f, z %.2f, 转 %.0f°", it.X, it.Z, it.Rot)}
					if w := t.wallOf(it); w != "" {
						m["靠"] = w
					}
					out = append(out, m)
				}
				if len(out) == 0 {
					return "这里还没有家具", nil
				}
				return out, nil
			}},
		{name: "list_devices", desc: "列出设备和它们现在的状态。可按房间或种类筛。",
			params: obj(map[string]any{"room": prop("string", "可选：房间 id 或名字"), "kind": enum("可选：种类", "light", "ac", "lock", "curtain", "vacuum", "sensor")}),
			label:  func(a args) string { return "查了" + roomOr(a, "全屋") + "的设备" },
			run: func(_ context.Context, a args) (any, error) {
				room := ""
				if s := a.str("room"); s != "" {
					r, ok := t.room(s)
					if !ok {
						return nil, fmt.Errorf("没有「%s」这个房间", s)
					}
					room = r.ID
				}
				out := []map[string]any{}
				for _, d := range t.d.Devices.List() {
					if (room != "" && d.Room != room) || (a.str("kind") != "" && d.Kind != a.str("kind")) {
						continue
					}
					out = append(out, map[string]any{"id": d.ID, "名字": d.Name, "种类": kindName[d.Kind], "房间": t.roomName(d.Room), "状态": state(d)})
				}
				return out, nil
			}},
		{name: "get_device", desc: "一个设备的全部状态（原始字段）、所在房间和 MQTT 主题。",
			params: obj(map[string]any{"device": prop("string", "设备 id 或名字")}, "device"),
			label:  func(a args) string { return "看了" + t.deviceName(a.str("device")) },
			run: func(_ context.Context, a args) (any, error) {
				d, err := t.device(a.str("device"))
				if err != nil {
					return nil, err
				}
				return map[string]any{"id": d.ID, "名字": d.Name, "种类": kindName[d.Kind], "房间": t.roomName(d.Room), "状态": state(d), "字段": d.State, "主题": d.Topic, "来源": d.Source}, nil
			}},
		{name: "read_sensors", desc: "各房间（和室外）的温度、湿度读数。",
			params: obj(map[string]any{"room": prop("string", "可选：房间 id 或名字")}),
			label:  func(a args) string { return "读了" + roomOr(a, "各房间") + "的温湿度" },
			run: func(_ context.Context, a args) (any, error) {
				room := ""
				if r, ok := t.room(a.str("room")); ok {
					room = r.ID
				}
				out := []map[string]any{}
				for _, d := range t.d.Devices.List() {
					if d.Kind != devices.Sensor || (room != "" && d.Room != room) {
						continue
					}
					out = append(out, map[string]any{"房间": t.roomName(d.Room), "温度": d.State["temp"], "湿度": d.State["humidity"], "传感器": d.Name})
				}
				return out, nil
			}},
		{name: "set_device", desc: "改一个设备：开关灯、调亮度和色温、开空调和设温度、拉窗帘、扫地机器人开始/暂停/回座、门锁上锁。每次改动都过家里的规则；开锁这类要家里人确认的不会直接执行，会变成一条等确认的请求。",
			params: obj(devProps, "device"),
			label: func(a args) string {
				if d, err := t.device(a.str("device")); err == nil {
					return automate.Describe(d, patchFrom(a))
				}
				return "操作了" + t.deviceName(a.str("device"))
			},
			run: func(ctx context.Context, a args) (any, error) {
				d, err := t.device(a.str("device"))
				if err != nil {
					return nil, err
				}
				patch := patchFrom(a)
				if len(patch) == 0 {
					return nil, errors.New("没有说要改什么")
				}
				desc := automate.Describe(d, patch)
				err = devices.Check(d, patch, false)
				var nc *devices.NeedConfirm
				if errors.As(err, &nc) {
					p := t.d.Auto.Ask(d.ID, patch, "家庭助手", nc.Text)
					t.emit(ctx, Event{Kind: "approval", Text: desc, ID: p.ID})
					return map[string]any{"结果": "没有执行，等家里人确认", "原因": nc.Text, "确认在哪": "自动化面板里的「等你确认」", "请求": desc}, nil
				}
				if err != nil {
					return nil, err
				}
				nd, err := t.d.Devices.Command(ctx, d.ID, patch, false, "家庭助手")
				if err != nil {
					return nil, err
				}
				return map[string]any{"结果": "已执行：" + desc, "现在": state(nd)}, nil
			}},
		{name: "list_library", desc: "家具库里的家具（导入过的宜家商品和手动填写的）：名字、尺寸、价格。",
			params: obj(map[string]any{}),
			label:  func(args) string { return "看了家具库" },
			run: func(context.Context, args) (any, error) {
				lib, err := t.d.Layouts.Library(t.d.Home.ID)
				if err != nil {
					return nil, err
				}
				out := []map[string]any{}
				for _, p := range lib {
					m := productInfo(p)
					if p.URL != "" {
						m["链接"] = p.URL
					}
					out = append(out, m)
				}
				return out, nil
			}},
		{name: "import_furniture", desc: "读取一个宜家商品链接：名字、真实尺寸、价格、包装，并放进家具库。",
			params: obj(map[string]any{"url": prop("string", "商品页链接")}, "url"),
			label:  func(a args) string { return "读取了商品页" },
			run: func(ctx context.Context, a args) (any, error) {
				p, err := t.importURL(ctx, a.str("url"))
				if err != nil {
					return nil, err
				}
				return productInfo(p), nil
			}},
		{name: "find_models", desc: "在 3D 模型库（Polyfork，免费、真实尺寸）里找家具和摆件的模型：钢琴、绿植、床头柜、落地灯……用英文关键词搜。返回的 id 写成 polyfork:<id> 交给 check_fit / propose_place。家具库和宜家都没有的东西用它找。",
			params: obj(map[string]any{"query": prop("string", "英文关键词，1 到 3 个词，如 bedside table、upright piano、houseplant")}, "query"),
			label:  func(a args) string { return "在模型库里找了 " + a.str("query") },
			run: func(ctx context.Context, a args) (any, error) {
				if t.d.Catalog == nil {
					return nil, errors.New("没有接模型库")
				}
				ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
				defer cancel()
				hits, err := t.d.Catalog.Search(ctx, a.str("query"), 8)
				if err != nil {
					return nil, err
				}
				out := []map[string]any{}
				for _, h := range hits {
					out = append(out, map[string]any{"id": "polyfork:" + h.ID, "名称": h.Title, "尺寸": fmt.Sprintf("%.0f×%.0f×%.0f cm", h.W, h.D, h.H), "类型": h.TypeLabel})
				}
				if len(out) == 0 {
					return "没找到，换个英文说法试试", nil
				}
				return out, nil
			}},
		{name: "check_fit", desc: "这件家具放得下吗：给了位置就检查那里，只给房间就在房间里靠墙找一个合适的地方。检查碰撞、过道（至少 80 cm）、挡门、挡窗、能不能搬进入户门。",
			params: obj(placeProps, "product"),
			label:  func(a args) string { return "检查了 " + roomOr(a, "") + "能不能放" },
			run: func(ctx context.Context, a args) (any, error) {
				p, err := t.product(ctx, a.str("product"))
				if err != nil {
					return nil, err
				}
				sp, err := t.spot(a, p)
				if err != nil {
					return nil, err
				}
				res := map[string]any{"家具": layout.Label(p), "尺寸": sizeText(p), "房间": t.roomName(sp.Room), "位置": fmt.Sprintf("x %.2f, z %.2f, 转 %.0f°", sp.X, sp.Z, sp.Rot)}
				if serious(sp.Warnings) == 0 {
					res["结论"] = "放得下"
				} else {
					res["结论"] = "有问题"
				}
				if len(sp.Warnings) > 0 {
					res["问题"] = warnTexts(sp.Warnings)
				}
				return res, nil
			}},
		{name: "propose_place", desc: "提出把一件家具放到某个位置（给链接会先导入）。不会直接放：页面上会出现一张「放进去」的卡片，家里人点了才放。只给房间时自动靠墙找位置。",
			params: obj(placeProps, "product"),
			label:  func(a args) string { return "提出放进" + roomOr(a, "房间") },
			run: func(ctx context.Context, a args) (any, error) {
				p, err := t.product(ctx, a.str("product"))
				if err != nil {
					return nil, err
				}
				sp, err := t.spot(a, p)
				if err != nil {
					return nil, err
				}
				text := fmt.Sprintf("把 %s（%s）放进%s", layout.Label(p), sizeText(p), t.roomName(sp.Room))
				pl := Place{Product: p, X: sp.X, Z: sp.Z, Rot: sp.Rot, Room: sp.Room, Text: text, Warnings: warnTexts(sp.Warnings)}
				t.emit(ctx, Event{Kind: "place", Place: &pl})
				res := map[string]any{"结果": "已经把卡片交给家里人确认，点「放进去」才会放", "位置": fmt.Sprintf("x %.2f, z %.2f, 转 %.0f°", sp.X, sp.Z, sp.Rot)}
				if len(sp.Warnings) > 0 {
					res["要说的问题"] = warnTexts(sp.Warnings)
				}
				return res, nil
			}},
		{name: "layout_warnings", desc: "现在的布置有哪些问题（碰撞、过道太窄、挡门挡窗、进不了门）。",
			params: obj(map[string]any{}),
			label:  func(args) string { return "检查了现在的布置" },
			run: func(context.Context, args) (any, error) {
				l, err := t.d.Layouts.Layout(t.d.Home.ID)
				if err != nil {
					return nil, err
				}
				names := map[string]string{}
				for _, it := range l.Items {
					names[it.ID] = layout.Label(it.Product)
				}
				out := []string{}
				for _, w := range layout.Check(t.d.Home, l) {
					out = append(out, names[w.Item]+"："+w.Text)
				}
				if len(out) == 0 {
					return "没有发现问题", nil
				}
				return out, nil
			}},
		{name: "ask_home", desc: "问家的知识图谱：哪个房间有什么、什么靠哪面墙、哪个设备控制什么、房间之间怎么连。用一句中文问。",
			params: obj(map[string]any{"question": prop("string", "问题，比如 书柜靠哪面墙")}, "question"),
			label:  func(a args) string { return "查了知识图谱：" + a.str("question") },
			run: func(ctx context.Context, a args) (any, error) {
				if t.d.Knowledge == nil {
					return nil, errors.New("知识图谱没有接上，用 list_furniture、list_devices 代替")
				}
				text, nodes, err := t.d.Knowledge.Ask(ctx, a.str("question"))
				if err != nil {
					return nil, err
				}
				if len(nodes) > 0 {
					t.emit(ctx, Event{Kind: "highlight", Nodes: nodes})
				}
				return text, nil
			}},
		{name: "predict_temperature", desc: "按房间的热模型预计空调多久能把房间降到（或升到）某个温度，以及一小时里温度怎么走。",
			params: obj(map[string]any{"room": prop("string", "房间（或空调的名字）"), "target": prop("number", "可选：目标温度 ℃，不给就用空调现在的设定"), "mode": enum("可选：cool 制冷 / heat 制热，不给就用空调现在的模式", "cool", "heat")}, "room"),
			label:  func(a args) string { return "算了" + roomOr(a, a.str("room")) + "的到温时间" },
			run:    func(_ context.Context, a args) (any, error) { return t.predict(a) }},
		{name: "list_automations", desc: "家里的自动化：什么时候做什么、授权等级（只建议 / 有护栏 / 自主），和等家里人确认的请求。",
			params: obj(map[string]any{}),
			label:  func(args) string { return "看了自动化" },
			run: func(context.Context, args) (any, error) {
				level := map[automate.Level]string{automate.Advise: "只建议", automate.Guarded: "有护栏", automate.Auto: "自主"}
				as := []map[string]any{}
				for _, au := range t.d.Auto.Automations() {
					as = append(as, map[string]any{"id": au.ID, "名字": au.Name, "触发": au.When, "等级": level[au.Level], "启用": au.Enabled, "手动运行": au.Trigger.Kind == "manual"})
				}
				pend := []map[string]any{}
				for _, p := range t.d.Auto.Proposals() {
					if p.Status == "pending" {
						pend = append(pend, map[string]any{"名字": p.Name, "要做": p.Steps, "原因": p.Reason})
					}
				}
				return map[string]any{"自动化": as, "等确认": pend}, nil
			}},
		{name: "run_automation", desc: "运行一条自动化（按它自己的授权等级：只建议的会变成等确认，开锁一定等确认）。",
			params: obj(map[string]any{"automation": prop("string", "自动化的 id 或名字")}, "automation"),
			label:  func(a args) string { return "运行了 " + a.str("automation") },
			run: func(ctx context.Context, a args) (any, error) {
				want := a.str("automation")
				id := ""
				for _, au := range t.d.Auto.Automations() {
					if au.ID == want || au.Name == want || strings.Contains(au.Name, want) && want != "" {
						id = au.ID
						break
					}
				}
				if id == "" {
					return nil, fmt.Errorf("没有「%s」这条自动化", want)
				}
				p, err := t.d.Auto.Run(ctx, id, "家庭助手运行")
				if err != nil {
					return nil, err
				}
				out := map[string]any{"自动化": p.Name, "要做": p.Steps}
				switch p.Status {
				case "done":
					out["结果"] = "已执行"
				case "pending":
					out["结果"] = "等家里人确认（在自动化面板）"
					out["原因"] = p.Reason
					t.emit(ctx, Event{Kind: "approval", Text: p.Name, ID: p.ID})
				default:
					out["结果"] = "没成功"
					out["原因"] = p.Reason
				}
				return out, nil
			}},
	}
}

// spot is where check_fit and propose_place look: the position given, or a free one in the
// room given.
func (t *Tools) spot(a args, p ikea.Product) (Spot, error) {
	l, err := t.d.Layouts.Layout(t.d.Home.ID)
	if err != nil {
		return Spot{}, err
	}
	x, okx := a.num("x")
	z, okz := a.num("z")
	if okx && okz {
		rot, _ := a.num("rot")
		r := t.d.Home.RoomAt(home.P{x, z})
		room := ""
		if r != nil {
			room = r.ID
		}
		return Spot{X: x, Z: z, Rot: rot, Room: room, Warnings: t.warningsFor(l, p, x, z, rot)}, nil
	}
	r, ok := t.room(a.str("room"))
	if !ok {
		names := []string{}
		for _, rr := range t.d.Home.Rooms {
			names = append(names, rr.Name)
		}
		return Spot{}, fmt.Errorf("要说放进哪个房间：%s", strings.Join(names, "、"))
	}
	return t.findSpot(l, p, r), nil
}

func (t *Tools) predict(a args) (any, error) {
	if t.d.Estimate == nil {
		return nil, errors.New("没有房间热模型")
	}
	q := a.str("room")
	var ac devices.Device
	found := false
	if r, ok := t.room(q); ok {
		for _, d := range t.d.Devices.List() {
			if d.Kind == devices.AC && d.Room == r.ID {
				ac, found = d, true
			}
		}
		if !found {
			return nil, fmt.Errorf("%s没有空调", r.Name)
		}
	} else if d, err := t.device(q); err == nil && d.Kind == devices.AC {
		ac, found = d, true
	}
	if !found {
		return nil, fmt.Errorf("没找到「%s」的空调", q)
	}
	model, ok := t.d.Estimate.Model(ac.Room)
	if !ok {
		return nil, errors.New("这台空调不在任何房间里")
	}
	now, outdoor, ok := t.d.Estimate.Temp(ac.Room)
	if !ok {
		c, ok2 := ac.State["current"].(float64)
		if !ok2 {
			return nil, errors.New("这个房间还没有温度读数")
		}
		now = c
	}
	mode, _ := ac.State["mode"].(string)
	if m := a.str("mode"); m != "" {
		mode = m
	}
	target, _ := ac.State["target"].(float64)
	if v, ok := a.num("target"); ok {
		target = v
	}
	if mode != "cool" && mode != "heat" {
		mode = "cool"
		if target > now {
			mode = "heat"
		}
	}
	out := map[string]any{"房间": t.roomName(ac.Room), "空调": ac.Name, "现在": fmt.Sprintf("%.1f ℃", now), "室外": fmt.Sprintf("%.1f ℃", outdoor), "目标": fmt.Sprintf("%.1f ℃", target), "模式": map[string]string{"cool": "制冷", "heat": "制热"}[mode], "空调开着": ac.State["on"] == true}
	if eta, ok := model.ETA(now, target, outdoor, mode); ok {
		if eta == 0 {
			out["预计"] = "已经到了"
		} else {
			out["预计"] = fmt.Sprintf("约 %.0f 分钟", math.Max(1, math.Round(eta/60)))
		}
		out["秒"] = math.Round(eta)
	} else {
		out["预计"] = "到不了：空调和室外温度在别的温度上平衡"
	}
	pts := model.Simulate(now, outdoor, true, mode, target, 3600, 600)
	curve := []string{}
	for _, p := range pts {
		curve = append(curve, fmt.Sprintf("%.0f 分钟 %.1f ℃", p.S/60, p.T))
	}
	out["一小时里"] = curve
	return out, nil
}

// deviceName is a device's name for the step labels, or what was asked for when unknown.
func (t *Tools) deviceName(id string) string {
	if d, err := t.device(id); err == nil {
		return d.Name
	}
	return id
}

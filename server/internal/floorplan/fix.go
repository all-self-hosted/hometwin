package floorplan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	agentpkg "github.com/liliang-cn/agent-go/v3/pkg/agent"
	"github.com/liliang-cn/agent-go/v3/pkg/domain"

	"hometwin/internal/home"
)

// Fixer is the agent that goes through what the checks find in a draft and fixes what it can:
// it looks again at the part of the plan in question (a crop of the picture put to the vision
// model) and changes the draft with tools; every change is checked again straight away.
type Fixer struct {
	llm    domain.Generator // runs the agent
	vision domain.Generator // answers questions about crops of the plan
	dir    string
	// build makes the agent-go service (tests use a throwaway home).
	build func(b *agentpkg.Builder) (*agentpkg.Service, error)

	mu  sync.Mutex
	svc *agentpkg.Service
}

// NewFixer keeps the agent's state in dir.
func NewFixer(llm, vision domain.Generator, dir string) *Fixer {
	f := &Fixer{llm: llm, vision: vision, dir: dir}
	f.build = func(b *agentpkg.Builder) (*agentpkg.Service, error) {
		return b.WithDBPath(filepath.Join(dir, "agentgo.db")).Build()
	}
	return f
}

func (f *Fixer) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.svc != nil {
		err := f.svc.Close()
		f.svc = nil
		return err
	}
	return nil
}

// session is one draft being fixed.
type session struct {
	plan     *Plan
	img      image.Image
	id, name string
	vision   domain.Generator
	log      func(string)

	mu    sync.Mutex
	draft Draft
}

func (s *session) redo() Draft {
	s.draft = s.plan.Draft(s.id, s.name)
	return s.draft
}

type sessionKey struct{}

const fixPrompt = `你在校对一张户型图的自动识别结果（草稿）。图上印的数字（尺寸、面积）和房间名是对的；草稿里的位置是从图上量出来的，可能有错。

坐标：米，x 向东，z 向南，原点是户型的左上角（西北角）。房间的轮廓沿墙的中线，只有横平竖直的边。

工具：
- draft：看草稿现在的样子（房间轮廓、算出面积和图上面积、门窗、问题）。
- look：把图上一块区域（米）放大了问看图的模型，比如「这扇门在哪面墙上，连着哪两个房间」「这个房间南边的墙在哪」。改之前先看。
- set_room：改一个房间的轮廓（和类型）。相邻的房间共用边：挪一条边时，把另一边的房间也一起改，不要留缝、不要重叠。
- set_door：改一扇门连着的房间、大致位置、类型、宽度；门会落到那附近墙上真正的开口里。
- confirm_door：图上这扇门就在现在的位置（是读图时把它连着的房间说错了），认下它现在连着的房间。
- remove_door、add_window、remove_window。

规则：
- 只处理列出来的问题，一个一个来；每次改完看返回的问题有没有少。
- 面积差 8% 以内不用管。图上面积和算出来的差得多时，先 look 那个房间，找出哪条边放错了：墙几乎总在「尺寸线」上（draft 里给了 x、z 两组），先看相邻房间——一条边放错，往往两边的房间一个大一个小。挪边只挪到尺寸线或图上看得到的墙上，不要为了凑面积把边放到没有墙的地方。
- 门：图上开口在哪就在哪，读图时说的「连着哪两个房间」可能错。look 确认后：门现在的位置对，就 confirm_door；门放错了地方，就 set_door 把它挪到图上的开口那里。
- 改不了或者不该改的就留着，最后用一两句中文说你改了什么、还剩什么。`

func (f *Fixer) service() (*agentpkg.Service, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.svc != nil {
		return f.svc, nil
	}
	svc, err := f.build(agentpkg.New("floorplan").WithLLM(f.llm).WithSystemPrompt(fixPrompt))
	if err != nil {
		return nil, err
	}
	for _, t := range fixTools() {
		run := t.run
		svc.AddToolWithMetadata(t.name, t.desc, t.params, func(ctx context.Context, m map[string]any) (any, error) {
			s, ok := ctx.Value(sessionKey{}).(*session)
			if !ok {
				return nil, errors.New("没有在校对的草稿")
			}
			res, err := run(ctx, s, m)
			if err != nil {
				return map[string]any{"错误": err.Error()}, nil
			}
			return res, nil
		}, agentpkg.ToolMetadata{ReadOnly: t.name == "draft" || t.name == "look"})
	}
	f.svc = svc
	return svc, nil
}

// Fix works on the plan until the issues are gone or rounds run out; log hears each step.
// It returns the last draft.
func (f *Fixer) Fix(ctx context.Context, p *Plan, img image.Image, id, name string, rounds int, log func(string)) (Draft, error) {
	s := &session{plan: p, img: img, id: id, name: name, vision: f.vision, log: log}
	d := s.redo()
	svc, err := f.service()
	if err != nil {
		return d, err
	}
	ctx = context.WithValue(ctx, sessionKey{}, s)
	for round := range rounds {
		s.mu.Lock()
		todo := warnings(s.draft)
		goal := summary(s.draft)
		s.mu.Unlock()
		if len(todo) == 0 {
			break
		}
		lead := "草稿有这些问题，请逐条对照图核对：图上确实不一样的就改，图上就是这样的就认下："
		if round > 0 {
			lead = "还剩这些问题，再看一次（上一轮没改动的不用再看）："
		}
		goal = fmt.Sprintf("%s\n- %s\n\n草稿：\n%s", lead, strings.Join(todo, "\n- "), goal)
		// the issues are things to check, not orders: no requested-action contract on them
		ch, err := svc.RunStreamWithOptions(ctx, goal, agentpkg.WithSessionID(fmt.Sprintf("%s-%d", id, round)), agentpkg.WithMaxTurns(14),
			agentpkg.WithoutMemoryAutoStore(), agentpkg.WithConstraintExtraction(false))
		if err != nil {
			return s.draft, err
		}
		for ev := range ch {
			switch ev.Type {
			case agentpkg.EventTypeToolCall:
				if l := label(ev.ToolName, ev.ToolArgs); l != "" {
					log(l)
				}
			case agentpkg.EventTypeComplete:
				if t := strings.TrimSpace(ev.Content); t != "" {
					log("校对：" + t)
				}
			case agentpkg.EventTypeError:
				log("校对出错：" + ev.Content)
			}
		}
		if ctx.Err() != nil {
			break
		}
		// another round only when this one changed something
		s.mu.Lock()
		same := strings.Join(warnings(s.draft), "\n") == strings.Join(todo, "\n")
		s.mu.Unlock()
		if same {
			break
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.draft, ctx.Err()
}

func warnings(d Draft) []string {
	var out []string
	for _, is := range d.Issues {
		if is.Level == "warn" {
			out = append(out, is.Text)
		}
	}
	return out
}

// summary is the draft as the agent reads it.
func summary(d Draft) string {
	b, _ := json.Marshal(view(d))
	return string(b)
}

func view(d Draft) map[string]any {
	var rooms []map[string]any
	for i, r := range d.Home.Rooms {
		a := d.Areas[i]
		rooms = append(rooms, map[string]any{"name": r.Name, "kind": r.Kind, "poly": r.Poly, "图上面积": a.Printed, "算出面积": a.Computed})
	}
	var doors []map[string]any
	for _, dr := range d.Home.Doors {
		w, _ := d.Home.Wall(dr.Wall)
		c := w.At(dr.At)
		doors = append(doors, map[string]any{"id": dr.ID, "name": dr.Name, "at": home.P{round2(c[0]), round2(c[1])}, "width": dr.Width, "kind": dr.Kind, "joins": joins(d.Home, dr)})
	}
	var windows []map[string]any
	for _, wn := range d.Home.Windows {
		w, _ := d.Home.Wall(wn.Wall)
		a, b := w.At(wn.At-wn.Width/2), w.At(wn.At+wn.Width/2)
		windows = append(windows, map[string]any{"id": wn.ID, "from": home.P{round2(a[0]), round2(a[1])}, "to": home.P{round2(b[0]), round2(b[1])}})
	}
	var issues []string
	for _, is := range d.Issues {
		issues = append(issues, is.Text)
	}
	return map[string]any{"rooms": rooms, "doors": doors, "windows": windows, "issues": issues, "面积口径": d.Convention,
		"尺寸线": map[string]any{"x": lines(d.Frame.X), "z": lines(d.Frame.Z)}}
}

// joins are the two places a door is between, "outside" for none.
func joins(h home.Home, dr home.Door) []string {
	w, _ := h.Wall(dr.Wall)
	c, n := w.At(dr.At), w.Normal()
	var out []string
	for _, s := range []float64{0.3, -0.3} {
		name := "outside"
		if r := h.RoomAt(home.P{c[0] + n[0]*s, c[1] + n[1]*s}); r != nil {
			name = r.Name
		}
		out = append(out, name)
	}
	return out
}

// lines are the grid lines' positions in metres (where the plan's dimension chains and wall
// strokes put walls).
func lines(marks [][2]float64) []float64 {
	out := make([]float64, len(marks))
	for i, m := range marks {
		out[i] = m[0]
	}
	return out
}

type fixTool struct {
	name, desc string
	params     map[string]any
	run        func(ctx context.Context, s *session, a map[string]any) (any, error)
}

func obj(props map[string]any, required ...string) map[string]any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{"type": "object", "properties": props, "required": required}
}

var (
	numP   = map[string]any{"type": "number"}
	strP   = map[string]any{"type": "string"}
	pointP = map[string]any{"type": "array", "items": numP, "description": "[x, z] 米"}
	polyP  = map[string]any{"type": "array", "items": pointP, "description": "轮廓的角，按顺序，横平竖直"}
	namesP = map[string]any{"type": "array", "items": strP, "description": "两个房间名，户外写 outside"}
)

func fixTools() []fixTool {
	return []fixTool{
		{"draft", "看草稿现在的样子：房间（轮廓、图上面积、算出面积）、门（连着哪两个房间）、窗和问题。", obj(nil),
			func(_ context.Context, s *session, _ map[string]any) (any, error) {
				s.mu.Lock()
				defer s.mu.Unlock()
				return view(s.draft), nil
			}},
		{"look", "放大图上的一块区域（米）问看图的模型一个问题，回答是文字。", obj(map[string]any{
			"x0": numP, "z0": numP, "x1": numP, "z1": numP, "question": strP}, "x0", "z0", "x1", "z1", "question"),
			func(ctx context.Context, s *session, a map[string]any) (any, error) {
				x0, z0, x1, z1 := num(a, "x0"), num(a, "z0"), num(a, "x1"), num(a, "z1")
				q := str(a, "question")
				if q == "" {
					return nil, errors.New("要问什么？")
				}
				return s.look(ctx, x0, z0, x1, z1, q)
			}},
		{"set_room", "改一个房间的轮廓（米，横平竖直，按顺序的角），可以顺便改类型 kind（living|bedroom|kitchen|bath|balcony|entry）。", obj(map[string]any{
			"name": strP, "poly": polyP, "kind": strP}, "name", "poly"),
			func(_ context.Context, s *session, a map[string]any) (any, error) {
				s.mu.Lock()
				defer s.mu.Unlock()
				name := str(a, "name")
				i := s.plan.room(name)
				if i < 0 {
					return nil, fmt.Errorf("没有叫 %s 的房间", name)
				}
				poly, err := points(a["poly"])
				if err != nil {
					return nil, err
				}
				for j := range poly {
					poly[j] = home.P{round10(poly[j][0] * 1000), round10(poly[j][1] * 1000)}
				}
				poly = rectilinear(poly)
				if len(poly) < 4 || home.Area(poly) < 0.5e6 {
					return nil, errors.New("轮廓至少要 4 个角、0.5 m² 以上")
				}
				s.plan.Rooms[i].Poly = poly
				if k := str(a, "kind"); kinds[k] {
					s.plan.Rooms[i].Kind = k
				}
				return result(s.redo()), nil
			}},
		{"set_door", "改一扇门：between 连着的两个房间，x/z 大致位置（米），kind（swing|sliding|opening），width（米，可不填）。门会落到附近墙上真正的开口里。", obj(map[string]any{
			"id": strP, "between": namesP, "x": numP, "z": numP, "kind": strP, "width": numP}, "id"),
			func(_ context.Context, s *session, a map[string]any) (any, error) {
				s.mu.Lock()
				defer s.mu.Unlock()
				i, err := s.door(str(a, "id"))
				if err != nil {
					return nil, err
				}
				d := &s.plan.Doors[i]
				if b, err := names(a["between"]); err == nil && len(b) == 2 {
					d.Between = b
				}
				if x, ok := a["x"]; ok && x != nil {
					d.At[0] = num(a, "x") * 1000
				}
				if z, ok := a["z"]; ok && z != nil {
					d.At[1] = num(a, "z") * 1000
				}
				if k := str(a, "kind"); k == "swing" || k == "sliding" || k == "opening" {
					d.Kind = k
				}
				if w := num(a, "width"); w > 0 {
					d.Width = w * 1000
				}
				return result(s.redo()), nil
			}},
		{"confirm_door", "图上这扇门就在现在的位置：认下它现在连着的两个房间（读图时说错了的时候用）。", obj(map[string]any{"id": strP, "reason": strP}, "id"),
			func(_ context.Context, s *session, a map[string]any) (any, error) {
				s.mu.Lock()
				defer s.mu.Unlock()
				i, err := s.door(str(a, "id"))
				if err != nil {
					return nil, err
				}
				for _, dr := range s.draft.Home.Doors {
					if dr.ID == str(a, "id") {
						s.plan.Doors[i].Between = joins(s.draft.Home, dr)
						return result(s.redo()), nil
					}
				}
				return nil, fmt.Errorf("门 %s 没放进草稿里，用 set_door 给它找位置", str(a, "id"))
			}},
		{"remove_door", "去掉一扇门（图上其实没有的）。", obj(map[string]any{"id": strP}, "id"),
			func(_ context.Context, s *session, a map[string]any) (any, error) {
				s.mu.Lock()
				defer s.mu.Unlock()
				i, err := s.door(str(a, "id"))
				if err != nil {
					return nil, err
				}
				s.plan.Doors[i].Between = nil
				s.plan.Doors[i].Kind = "removed"
				return result(s.redo()), nil
			}},
		{"add_window", "在一个房间的外墙上加一扇窗：from/to 是窗两端（米），会落到附近外墙真正的开口里。", obj(map[string]any{
			"room": strP, "from": pointP, "to": pointP}, "room", "from", "to"),
			func(_ context.Context, s *session, a map[string]any) (any, error) {
				s.mu.Lock()
				defer s.mu.Unlock()
				ps, err := points([]any{a["from"], a["to"]})
				if err != nil {
					return nil, err
				}
				room := str(a, "room")
				if s.plan.room(room) < 0 {
					return nil, fmt.Errorf("没有叫 %s 的房间", room)
				}
				s.plan.Windows = append(s.plan.Windows, PWindow{Room: room, A: home.P{ps[0][0] * 1000, ps[0][1] * 1000}, B: home.P{ps[1][0] * 1000, ps[1][1] * 1000}})
				return result(s.redo()), nil
			}},
		{"remove_window", "去掉一扇窗。", obj(map[string]any{"id": strP}, "id"),
			func(_ context.Context, s *session, a map[string]any) (any, error) {
				s.mu.Lock()
				defer s.mu.Unlock()
				var i int
				if _, err := fmt.Sscanf(str(a, "id"), "n%d", &i); err != nil || i < 1 || i > len(s.plan.Windows) {
					return nil, errors.New("没有这扇窗")
				}
				s.plan.Windows[i-1].Room = ""
				return result(s.redo()), nil
			}},
	}
}

// result is what a change answers: the issues left and the areas.
func result(d Draft) map[string]any {
	var areas []string
	for _, a := range d.Areas {
		if a.Printed > 0 {
			areas = append(areas, fmt.Sprintf("%s %.2f/%.1f", a.Name, a.Computed, a.Printed))
		}
	}
	var issues []string
	for _, is := range d.Issues {
		issues = append(issues, is.Text)
	}
	return map[string]any{"问题": issues, "面积（算出/图上）": areas}
}

func label(tool string, a map[string]any) string {
	switch tool {
	case "look":
		return fmt.Sprintf("看图上 (%.1f, %.1f)–(%.1f, %.1f)：%s", num(a, "x0"), num(a, "z0"), num(a, "x1"), num(a, "z1"), str(a, "question"))
	case "set_room":
		return "改了" + str(a, "name") + "的轮廓"
	case "set_door":
		return "改了门 " + str(a, "id")
	case "confirm_door":
		return "认下门 " + str(a, "id") + " 现在的位置"
	case "remove_door":
		return "去掉了门 " + str(a, "id")
	case "add_window":
		return "给" + str(a, "room") + "加了窗"
	case "remove_window":
		return "去掉了窗 " + str(a, "id")
	}
	return ""
}

func (p *Plan) room(name string) int {
	for i, r := range p.Rooms {
		if r.Name == name {
			return i
		}
	}
	return -1
}

func (s *session) door(id string) (int, error) {
	var i int
	if _, err := fmt.Sscanf(id, "d%d", &i); err != nil || i < 1 || i > len(s.plan.Doors) {
		return 0, fmt.Errorf("没有门 %s", id)
	}
	return i - 1, nil
}

// look crops the plan around a region (metres, with half a metre around it), enlarges it and
// asks the vision model.
func (s *session) look(ctx context.Context, x0, z0, x1, z1 float64, q string) (any, error) {
	if s.vision == nil {
		return nil, errors.New("没有看图的模型")
	}
	s.mu.Lock()
	a := s.plan.px(home.P{(math.Min(x0, x1) - 0.5) * 1000, (math.Min(z0, z1) - 0.5) * 1000})
	b := s.plan.px(home.P{(math.Max(x0, x1) + 0.5) * 1000, (math.Max(z0, z1) + 0.5) * 1000})
	rooms := make([]string, len(s.plan.Rooms))
	for i, r := range s.plan.Rooms {
		rooms[i] = r.Name
	}
	s.mu.Unlock()
	rect := image.Rect(int(a[0]), int(a[1]), int(b[0])+1, int(b[1])+1).Intersect(s.img.Bounds())
	if rect.Dx() < 8 || rect.Dy() < 8 {
		return nil, errors.New("这块区域不在图上")
	}
	img := enlarge(crop(s.img, rect), 768)
	prompt := fmt.Sprintf("这是一张户型图的局部放大（北在上）。整套户型的房间有：%s。门画成一段弧线加一条门扇，窗画成墙上的细双线，墙是黑色粗线。\n问题：%s\n用中文简短回答，只说图上看得到的。", strings.Join(rooms, "、"), q)
	ans, err := ask(ctx, s.vision, img, prompt, false)
	if err != nil {
		return nil, err
	}
	return map[string]any{"回答": strings.TrimSpace(ans)}, nil
}

// enlarge scales a small crop up (nearest pixel) so its longer side is at least size.
func enlarge(img image.Image, size int) image.Image {
	b := img.Bounds()
	k := int(math.Ceil(float64(size) / float64(max(b.Dx(), b.Dy()))))
	if k <= 1 {
		return img
	}
	out := image.NewRGBA(image.Rect(0, 0, b.Dx()*k, b.Dy()*k))
	for y := 0; y < b.Dy()*k; y++ {
		for x := 0; x < b.Dx()*k; x++ {
			out.Set(x, y, img.At(b.Min.X+x/k, b.Min.Y+y/k))
		}
	}
	return out
}

func num(a map[string]any, k string) float64 {
	switch v := a[k].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case json.Number:
		f, _ := v.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return f
	}
	return 0
}

func str(a map[string]any, k string) string {
	if v, ok := a[k].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func points(v any) ([]home.P, error) {
	list, ok := v.([]any)
	if !ok {
		return nil, errors.New("点要写成 [[x, z], …]")
	}
	out := make([]home.P, 0, len(list))
	for _, it := range list {
		pt, ok := it.([]any)
		if !ok || len(pt) != 2 {
			return nil, errors.New("每个点是 [x, z]")
		}
		x, ok1 := pt[0].(float64)
		z, ok2 := pt[1].(float64)
		if !ok1 || !ok2 {
			return nil, errors.New("坐标要是数字")
		}
		out = append(out, home.P{x, z})
	}
	return out, nil
}

func names(v any) ([]string, error) {
	list, ok := v.([]any)
	if !ok {
		return nil, errors.New("要两个房间名")
	}
	var out []string
	for _, it := range list {
		if s, ok := it.(string); ok {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out, nil
}

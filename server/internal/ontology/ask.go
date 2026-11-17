package ontology

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Answer is the reply to a question about the home.
type Answer struct {
	Text string `json:"text"`
	// Nodes are the things the answer is about, for the scene to point at.
	Nodes []string `json:"nodes"`
	Rows  []Row    `json:"rows,omitempty"`
}

type Row struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Detail string `json:"detail,omitempty"`
}

// Examples are questions the graph answers, for the UI.
var Examples = []string{"客厅有哪些设备", "主卧有哪些家具", "书柜靠哪面墙", "客厅空调能影响哪些房间", "主卧连着哪些房间", "哪些灯开着", "卫生间多少度", "入户门锁管什么", "有什么问题"}

var aliases = map[string]string{"客厅": "客餐厅", "餐厅": "客餐厅", "大卧室": "主卧", "小卧室": "次卧", "厕所": "卫生间", "洗手间": "卫生间", "门口": "玄关"}

var (
	reHas     = regexp.MustCompile(`(.+?)(?:里|里面)?有(?:哪些|什么|几个)(设备|家具|东西)?`)
	reAgainst = regexp.MustCompile(`(.+?)靠(?:哪|那)(?:面|堵)?墙`)
	reWhere   = regexp.MustCompile(`(.+?)在哪`)
	reConnect = regexp.MustCompile(`(.+?)(?:连着|通向|通到|通往|能去|挨着)`)
	reLightOn = regexp.MustCompile(`(?:哪些|哪个|什么)灯(?:开着|亮着|没关)`)
	reTemp    = regexp.MustCompile(`(.+?)(?:多少度|温度|多热|多冷|湿度|潮)`)
	reControl = regexp.MustCompile(`(.+?)(?:管|控制|负责)(?:什么|哪)`)
	reAffect  = regexp.MustCompile(`(.+?)(?:能|会|可以)?影响(?:哪些|哪个|什么)`)
	reNoLight = regexp.MustCompile(`(?:哪些|哪个)房间没(?:有)?(?:照明|灯)`)
	reIssues  = regexp.MustCompile(`(?:有什么|有哪些|有没有)(?:问题|不对|毛病|违反)|哪里不对|检查一下`)
)

var deviceClasses = []string{"Light", "AirConditioner", "DoorLock", "Curtain", "RobotVacuum", "Sensor", "FridgeSensor"}

func isDeviceClass(c string) bool {
	for _, x := range deviceClasses {
		if x == c {
			return true
		}
	}
	return false
}

// kind of a node by its most specific class: room, furniture, device, other
func (s *Store) kind(id string) string {
	switch {
	case strings.HasPrefix(id, "room:"):
		return "Room"
	case strings.HasPrefix(id, "item:"):
		return "Furniture"
	case strings.HasPrefix(id, "device:") && !strings.Contains(id, "/"):
		return "Device"
	}
	return ""
}

// find is the thing a question names: the longest name (or alias, series name, kind label) of
// one of the kinds wanted ("" any) it contains.
func (s *Store) findNamed(q string, kinds ...string) (string, bool) {
	q = strings.ToLower(q)
	// the question as asked, and with everyday names for rooms put in ("客厅" is the 客餐厅);
	// a device's own name may use the everyday one ("客厅空调")
	alt := q
	for a, r := range aliases {
		if strings.Contains(alt, a) && !strings.Contains(alt, r) {
			alt = strings.ReplaceAll(alt, a, r)
		}
	}
	best, bestLen := "", 0
	for id, n := range s.fs.names {
		k := s.kind(id)
		if k == "" {
			continue
		}
		if len(kinds) > 0 {
			ok := false
			for _, w := range kinds {
				ok = ok || w == k
			}
			if !ok {
				continue
			}
		}
		names := []string{strings.ToLower(n)}
		if k == "Device" {
			// "窗帘" is the first curtain
			names = append(names, classLabel(s.fs.types[id]))
		}
		if k == "Furniture" {
			names = append(names, strings.Fields(strings.ToLower(n))...)
			if tl, ok := s.fs.props[id]["typeLabel"].(string); ok {
				names = append(names, tl)
			}
			names = append(names, classLabel(s.fs.types[id]))
		}
		for _, nm := range names {
			if nm != "" && (strings.Contains(q, nm) || strings.Contains(alt, nm)) && (len(nm) > bestLen || len(nm) == bestLen && id < best) {
				best, bestLen = id, len(nm)
			}
		}
	}
	return best, bestLen > 0
}

func (s *Store) state(id string) map[string]any {
	if d, ok := s.devs[strings.TrimPrefix(id, "device:")]; ok {
		return d.State
	}
	return nil
}

func (s *Store) detail(id string) string {
	switch s.kind(id) {
	case "Furniture":
		v, _ := s.fs.props[id]["size"].(string)
		return v
	case "Device":
		return describeState(s.fs.types[id], s.state(id))
	}
	return ""
}

func describeState(class string, st map[string]any) string {
	if st == nil {
		return ""
	}
	on := st["on"] == true
	switch class {
	case "Light":
		if on {
			return fmt.Sprintf("开着，亮度 %v%%", st["brightness"])
		}
		return "关着"
	case "AirConditioner":
		if on {
			return fmt.Sprintf("开着，%v ℃", st["target"])
		}
		return "关着"
	case "DoorLock":
		if st["locked"] == true {
			return "已上锁"
		}
		return "没锁"
	case "Curtain":
		return fmt.Sprintf("拉开 %v%%", st["position"])
	case "RobotVacuum":
		return map[string]string{"docked": "在座上充电", "cleaning": "在打扫", "returning": "在回座", "paused": "暂停了"}[fmt.Sprint(st["phase"])]
	case "Sensor", "FridgeSensor":
		return fmt.Sprintf("%v ℃，湿度 %v%%", st["temp"], st["humidity"])
	}
	return ""
}

// in is what stands directly in a room (devices and furniture), picked by kind.
func (s *Store) in(ctx context.Context, room string, kind string) []Row {
	var rows []Row
	for _, id := range s.subjects(ctx, "locatedIn", room) {
		if s.kind(id) != kind && !(kind == "" && (s.kind(id) == "Device" || s.kind(id) == "Furniture")) {
			continue
		}
		// only what is in this room itself, not in a room of it
		rows = append(rows, Row{ID: id, Name: s.name(id), Detail: s.detail(id)})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows
}

func ids(rows []Row) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.ID)
	}
	return out
}

func names(rows []Row) string {
	parts := make([]string, 0, len(rows))
	for _, r := range rows {
		parts = append(parts, r.Name)
	}
	return strings.Join(parts, "、")
}

// Ask answers a question about the home in plain words, by walking the knowledge graph.
func (s *Store) Ask(ctx context.Context, q string) (Answer, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return Answer{Text: "家的知识图谱还没有加载。"}, nil
	}
	q = strings.TrimSpace(strings.TrimRight(q, "？?。."))
	if q == "" {
		return Answer{Text: "问点什么吧，比如：" + strings.Join(Examples[:3], "、")}, nil
	}
	if reIssues.MatchString(q) {
		return s.issues(), nil
	}
	if reNoLight.MatchString(q) {
		var rows []Row
		for _, v := range s.structural {
			if v.Constraint == "房间要有照明" {
				rows = append(rows, Row{ID: v.Node, Name: v.Name, Detail: v.Text})
			}
		}
		if len(rows) == 0 {
			return Answer{Text: "每个房间都有照明（本体的「房间要有照明」约束都满足）。"}, nil
		}
		return Answer{Text: fmt.Sprintf("%d 个房间没有照明：%s。", len(rows), names(rows)), Nodes: ids(rows), Rows: rows}, nil
	}
	if reLightOn.MatchString(q) {
		var rows []Row
		for _, id := range s.ofClass(ctx, "Light") {
			if st := s.state(id); st != nil && st["on"] == true {
				rows = append(rows, Row{ID: id, Name: s.name(id), Detail: s.detail(id)})
			}
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
		if len(rows) == 0 {
			return Answer{Text: "现在一盏灯都没开。"}, nil
		}
		return Answer{Text: fmt.Sprintf("开着的灯有 %d 盏：%s。", len(rows), names(rows)), Nodes: ids(rows), Rows: rows}, nil
	}
	if m := reAgainst.FindStringSubmatch(q); m != nil {
		id, ok := s.findNamed(m[1], "Furniture")
		if !ok {
			return Answer{Text: "家里还没有放「" + m[1] + "」。"}, nil
		}
		var walls []string
		back := map[string]bool{}
		for _, w := range s.objects(ctx, id, "backAgainst") {
			back[w] = true
		}
		for _, w := range s.objects(ctx, id, "against") {
			t := s.name(w)
			if back[w] {
				t += "（背靠）"
			}
			walls = append(walls, t)
		}
		sort.Strings(walls)
		if len(walls) == 0 {
			return Answer{Text: s.name(id) + "没有靠墙放。", Nodes: []string{id}}, nil
		}
		return Answer{Text: s.name(id) + "靠着" + strings.Join(walls, "和") + "。", Nodes: []string{id}}, nil
	}
	if m := reAffect.FindStringSubmatch(q); m != nil {
		if id, ok := s.findNamed(m[1], "Device"); ok {
			var rows []Row
			for _, r := range s.objects(ctx, id, "affects") {
				if s.isA(ctx, r, "Room") {
					why, _ := s.explainLocked(ctx, id, "affects", r)
					rows = append(rows, Row{ID: r, Name: s.name(r), Detail: why})
				}
			}
			sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
			if len(rows) == 0 {
				return Answer{Text: s.name(id) + "不影响哪个房间。", Nodes: []string{id}}, nil
			}
			return Answer{Text: fmt.Sprintf("%s能影响 %s。", s.name(id), names(rows)), Nodes: append([]string{id}, ids(rows)...), Rows: rows}, nil
		}
	}
	if m := reControl.FindStringSubmatch(q); m != nil {
		if id, ok := s.findNamed(m[1], "Device"); ok {
			var what []string
			nodes := []string{id}
			for _, rel := range []string{"illuminates", "cleans", "secures", "covers", "measures"} {
				for _, o := range s.objects(ctx, id, rel) {
					verb := map[string]string{"illuminates": "照亮", "cleans": "打扫", "secures": "锁住", "covers": "遮住", "measures": "测量"}[rel]
					what = append(what, verb+s.name(o))
					nodes = append(nodes, o)
				}
			}
			if s.fs.types[id] == "AirConditioner" {
				for _, o := range s.objects(ctx, id, "controls") {
					what = append(what, "调节"+s.name(o))
					nodes = append(nodes, o)
				}
			}
			if len(what) == 0 {
				return Answer{Text: s.name(id) + "不控制别的东西。", Nodes: nodes}, nil
			}
			return Answer{Text: s.name(id) + "：" + strings.Join(what, "，") + "。", Nodes: nodes}, nil
		}
	}
	if m := reConnect.FindStringSubmatch(q); m != nil {
		if r, ok := s.findNamed(m[1], "Room"); ok {
			var rows []Row
			for _, o := range s.objects(ctx, r, "leadsTo") {
				// through which door: one that connects both
				via := ""
				for _, d := range s.objects(ctx, r, "hasDoor") {
					for _, x := range s.objects(ctx, d, "connects") {
						if x == o {
							via = s.name(d)
						}
					}
				}
				rows = append(rows, Row{ID: o, Name: s.name(o), Detail: "经" + via})
			}
			sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
			if len(rows) == 0 {
				return Answer{Text: s.name(r) + "没有门通向别的房间（可能是敞开相连的）。", Nodes: []string{r}}, nil
			}
			parts := []string{}
			for _, row := range rows {
				parts = append(parts, row.Name+"（"+row.Detail+"）")
			}
			return Answer{Text: s.name(r) + "通向 " + strings.Join(parts, "、") + "。", Nodes: append([]string{r}, ids(rows)...), Rows: rows}, nil
		}
	}
	if m := reTemp.FindStringSubmatch(q); m != nil {
		if r, ok := s.findNamed(m[1], "Room"); ok {
			for _, sid := range s.subjects(ctx, "measures", r) {
				return Answer{Text: fmt.Sprintf("%s现在 %s（%s）。", s.name(r), describeState("Sensor", s.state(sid)), s.name(sid)), Nodes: []string{r, sid}}, nil
			}
			return Answer{Text: s.name(r) + "没有温湿度传感器。", Nodes: []string{r}}, nil
		}
	}
	if m := reHas.FindStringSubmatch(q); m != nil {
		if r, ok := s.findNamed(m[1], "Room"); ok {
			kind := map[string]string{"设备": "Device", "家具": "Furniture"}[m[2]]
			rows := s.in(ctx, r, kind)
			what := map[string]string{"设备": "设备", "家具": "家具"}[m[2]]
			if what == "" {
				what = "家具和设备"
			}
			if len(rows) == 0 {
				return Answer{Text: s.name(r) + "还没有" + what + "。", Nodes: []string{r}}, nil
			}
			return Answer{Text: fmt.Sprintf("%s有 %d 件%s：%s。", s.name(r), len(rows), what, names(rows)), Nodes: append([]string{r}, ids(rows)...), Rows: rows}, nil
		}
	}
	if m := reWhere.FindStringSubmatch(q); m != nil {
		if id, ok := s.findNamed(m[1], "Furniture", "Device"); ok {
			if r := s.roomOf(ctx, id); r != "" {
				return Answer{Text: s.name(id) + "在" + s.name(r) + "。", Nodes: []string{id, r}}, nil
			}
		}
	}
	if id, ok := s.findNamed(q); ok {
		if s.kind(id) == "Room" {
			rows := s.in(ctx, id, "")
			return Answer{Text: fmt.Sprintf("%s有 %d 件家具和设备：%s。", s.name(id), len(rows), names(rows)), Nodes: append([]string{id}, ids(rows)...), Rows: rows}, nil
		}
		if r := s.roomOf(ctx, id); r != "" {
			d := s.detail(id)
			if d != "" {
				d = "，" + d
			}
			return Answer{Text: s.name(id) + "在" + s.name(r) + d + "。", Nodes: []string{id, r}}, nil
		}
	}
	return Answer{Text: "这个我还答不上来。可以这样问：" + strings.Join(Examples, "、") + "。"}, nil
}

// roomOf is the room (not the home) a thing is in.
func (s *Store) roomOf(ctx context.Context, id string) string {
	for _, r := range s.objects(ctx, id, "locatedIn") {
		if s.isA(ctx, r, "Room") || r == "outside" {
			return r
		}
	}
	return ""
}

func (s *Store) issues() Answer {
	all := append(append(append([]Violation{}, s.structural...), s.layoutV...), s.live...)
	if len(all) == 0 {
		return Answer{Text: "没发现问题：本体的约束（每个房间有照明、卧室有门、通道和门的开启范围、读数）都满足。"}
	}
	var rows []Row
	seen := map[string]bool{}
	for _, v := range all {
		if seen[v.Node+v.Text] {
			continue
		}
		seen[v.Node+v.Text] = true
		rows = append(rows, Row{ID: v.Node, Name: v.Constraint, Detail: v.Text})
	}
	return Answer{Text: fmt.Sprintf("有 %d 处不满足约束。", len(rows)), Nodes: ids(rows), Rows: rows}
}

// explainLocked is Explain for callers that hold s.mu.
func (s *Store) explainLocked(ctx context.Context, sub, rel, obj string) (string, bool) {
	ts := s.find(ctx, ref(thing(sub)), ref(ht(rel)), ref(thing(obj)))
	if len(ts) == 0 {
		return "", false
	}
	if !ts[0].Inferred {
		return "给定的事实", true
	}
	steps := s.why(ctx, ts[0].ID)
	return strings.Join(steps, "；"), true
}

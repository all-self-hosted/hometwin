// Package automate runs the home's automations at the level of authority each one is given:
//
//   - advise (只建议): the twin proposes, a person decides;
//   - guarded (有护栏): it acts when every action passes the device rules, otherwise it asks;
//   - auto (自主): it acts on its own.
//
// Whatever the level, an action the rules mark as needing a person (unlocking the front door)
// waits for one, and every run is logged with why it happened and who allowed it.
package automate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"hometwin/internal/devices"
	"hometwin/internal/insight"
)

type Level string

const (
	Advise  Level = "advise"
	Guarded Level = "guarded"
	Auto    Level = "auto"
)

type Action struct {
	Device string         `json:"device"`
	Patch  map[string]any `json:"patch"`
}

// Trigger is what starts an automation.
type Trigger struct {
	// Kind: manual, time ("23:00" in At), hint (a hint of HintKind, in Room if given), state
	// (Device's Field crosses Op Value).
	Kind     string `json:"kind"`
	At       string `json:"at,omitempty"`
	HintKind string `json:"hint,omitempty"`
	Room     string `json:"room,omitempty"`
	Device   string `json:"device,omitempty"`
	Field    string `json:"field,omitempty"`
	Op       string `json:"op,omitempty"` // = > <
	Value    any    `json:"value,omitempty"`
}

type Automation struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	When    string   `json:"when"` // the trigger for people
	Trigger Trigger  `json:"trigger"`
	Actions []Action `json:"actions"`
	Level   Level    `json:"level"`
	Enabled bool     `json:"enabled"`
}

// Proposal is one run of an automation that waits for a person, or that happened.
type Proposal struct {
	ID         string   `json:"id"`
	Automation string   `json:"automation"`
	Name       string   `json:"name"`
	Why        string   `json:"why"`
	Actions    []Action `json:"actions"`
	// Steps say for each action what it does, in words.
	Steps []string `json:"steps"`
	// Status: pending, done, rejected, failed.
	Status string `json:"status"`
	// Reason is why it waits for a person, or why it failed.
	Reason string `json:"reason,omitempty"`
	// By is who let it run: the automation's level, or a person.
	By string `json:"by,omitempty"`
	T  int64  `json:"t"`
}

type Engine struct {
	hub  *devices.Hub
	path string
	now  func() time.Time

	mu        sync.Mutex
	autos     []Automation
	proposals []Proposal // newest first, the last 50
	seq       int
	lastState map[string]bool
	lastDay   map[string]string
	onChange  []func()
}

// Defaults are the automations a home starts with.
func Defaults() []Automation {
	return []Automation{
		{ID: "welcome", Name: "回家开玄关灯", When: "入户门锁打开时", Level: Auto, Enabled: true,
			Trigger: Trigger{Kind: "state", Device: "lock-entry", Field: "locked", Op: "=", Value: false},
			Actions: []Action{{"light-entry", map[string]any{"on": true, "brightness": 80.0}}}},
		{ID: "too-hot", Name: "客厅太热开空调", When: "客厅超过 28 ℃ 时", Level: Guarded, Enabled: true,
			Trigger: Trigger{Kind: "state", Device: "sensor-living", Field: "temp", Op: ">", Value: 28.0},
			Actions: []Action{{"ac-living", map[string]any{"on": true, "mode": "cool", "target": 26.0}}}},
		{ID: "bedtime", Name: "睡前检查", When: "每天 23:00", Level: Advise, Enabled: true,
			Trigger: Trigger{Kind: "time", At: "23:00"},
			Actions: []Action{
				{"lock-entry", map[string]any{"locked": true}},
				{"light-living", map[string]any{"on": false}},
				{"light-dining", map[string]any{"on": false}},
				{"curtain-living", map[string]any{"position": 0.0}},
			}},
		{ID: "leave", Name: "出门", When: "手动运行", Level: Guarded, Enabled: true,
			Trigger: Trigger{Kind: "manual"},
			Actions: []Action{
				{"light-living", map[string]any{"on": false}}, {"light-dining", map[string]any{"on": false}},
				{"light-master", map[string]any{"on": false}}, {"light-second", map[string]any{"on": false}},
				{"light-kitchen", map[string]any{"on": false}}, {"light-entry", map[string]any{"on": false}},
				{"ac-living", map[string]any{"on": false}}, {"ac-master", map[string]any{"on": false}},
				{"lock-entry", map[string]any{"locked": true}},
				{"vacuum", map[string]any{"phase": "cleaning"}},
			}},
		{ID: "guest", Name: "给访客开门", When: "手动运行", Level: Auto, Enabled: true,
			Trigger: Trigger{Kind: "manual"},
			Actions: []Action{{"lock-entry", map[string]any{"locked": false}}, {"light-entry", map[string]any{"on": true}}}},
		{ID: "door-open", Name: "门开太久提醒", When: "入户门开着超过 2 分钟", Level: Advise, Enabled: true,
			Trigger: Trigger{Kind: "hint", HintKind: "door-open"},
			Actions: []Action{{"light-entry", map[string]any{"on": true}}}},
	}
}

func New(hub *devices.Hub, dir string) (*Engine, error) {
	e := &Engine{hub: hub, now: time.Now, lastState: map[string]bool{}, lastDay: map[string]string{}}
	if dir != "" {
		e.path = filepath.Join(dir, "automations-"+hub.Home+".json")
		b, err := os.ReadFile(e.path)
		switch {
		case err == nil:
			if err := json.Unmarshal(b, &e.autos); err != nil {
				return nil, fmt.Errorf("%s: %w", e.path, err)
			}
		case !errors.Is(err, os.ErrNotExist):
			return nil, err
		}
	}
	if e.autos == nil {
		e.autos = Defaults()
	}
	return e, nil
}

func (e *Engine) Listen(fn func()) {
	e.mu.Lock()
	e.onChange = append(e.onChange, fn)
	e.mu.Unlock()
}

func (e *Engine) changed() {
	e.mu.Lock()
	fns := slices.Clone(e.onChange)
	e.mu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

func (e *Engine) Automations() []Automation {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.autos)
}

func (e *Engine) Proposals() []Proposal {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]Proposal{}, e.proposals...)
}

// SetLevel changes how much an automation may do alone, or turns it on and off.
func (e *Engine) SetLevel(id string, level Level, enabled bool) (Automation, error) {
	if !slices.Contains([]Level{Advise, Guarded, Auto}, level) {
		return Automation{}, fmt.Errorf("等级只能是 advise、guarded 或 auto")
	}
	e.mu.Lock()
	var out Automation
	found := false
	for i := range e.autos {
		if e.autos[i].ID == id {
			e.autos[i].Level, e.autos[i].Enabled = level, enabled
			out, found = e.autos[i], true
		}
	}
	e.mu.Unlock()
	if !found {
		return out, fmt.Errorf("没有这个自动化")
	}
	e.save()
	e.changed()
	return out, nil
}

func (e *Engine) save() {
	if e.path == "" {
		return
	}
	e.mu.Lock()
	b, _ := json.MarshalIndent(e.autos, "", "  ")
	e.mu.Unlock()
	tmp := e.path + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		os.Rename(tmp, e.path)
	}
}

func (e *Engine) steps(as []Action) []string {
	out := make([]string, 0, len(as))
	for _, a := range as {
		d, _ := e.hub.Get(a.Device)
		out = append(out, Describe(d, a.Patch))
	}
	return out
}

// Describe says what a change does, in words: "客厅空调 制冷 26 ℃".
func Describe(d devices.Device, p map[string]any) string {
	name := d.Name
	if name == "" {
		name = d.ID
	}
	var parts []string
	if on, ok := p["on"].(bool); ok {
		parts = append(parts, map[bool]string{true: "打开", false: "关掉"}[on])
	}
	if m, ok := p["mode"].(string); ok {
		parts = append(parts, map[string]string{"cool": "制冷", "heat": "制热", "dry": "除湿", "fan": "送风", "auto": "自动"}[m])
	}
	if v, ok := p["target"].(float64); ok {
		parts = append(parts, strconv.FormatFloat(v, 'f', -1, 64)+" ℃")
	}
	if v, ok := p["brightness"].(float64); ok {
		parts = append(parts, fmt.Sprintf("亮度 %.0f%%", v))
	}
	if l, ok := p["locked"].(bool); ok {
		parts = append(parts, map[bool]string{true: "上锁", false: "开锁"}[l])
	}
	if v, ok := p["position"].(float64); ok {
		if v == 0 {
			parts = append(parts, "拉上")
		} else {
			parts = append(parts, fmt.Sprintf("拉开到 %.0f%%", v))
		}
	}
	if ph, ok := p["phase"].(string); ok {
		parts = append(parts, map[string]string{"cleaning": "开始打扫", "paused": "暂停", "returning": "回座充电"}[ph])
	}
	return name + " " + strings.Join(parts, " ")
}

// Run starts an automation now (a person pressed run, or its trigger fired).
func (e *Engine) Run(ctx context.Context, id, why string) (Proposal, error) {
	var a *Automation
	e.mu.Lock()
	for i := range e.autos {
		if e.autos[i].ID == id {
			c := e.autos[i]
			a = &c
		}
	}
	e.mu.Unlock()
	if a == nil {
		return Proposal{}, fmt.Errorf("没有这个自动化")
	}
	p := Proposal{Automation: a.ID, Name: a.Name, Why: why, Actions: a.Actions, Steps: e.steps(a.Actions), T: e.now().UnixMilli()}
	switch a.Level {
	case Advise:
		p.Status, p.Reason = "pending", "这条自动化只提建议，要你确认才会执行"
	default:
		// every action must pass the rules on its own before any of them is sent
		for _, act := range a.Actions {
			d, ok := e.hub.Get(act.Device)
			if !ok {
				p.Status, p.Reason = "failed", "找不到设备 "+act.Device
				break
			}
			err := devices.Check(d, act.Patch, false)
			var nc *devices.NeedConfirm
			if errors.As(err, &nc) {
				p.Status, p.Reason = "pending", nc.Text+"（开锁这类动作一定要人确认）"
				break
			}
			if err != nil {
				if a.Level == Guarded {
					p.Status, p.Reason = "pending", "规则不允许："+err.Error()
				} else {
					p.Status, p.Reason = "failed", err.Error()
				}
				break
			}
		}
		if p.Status == "" {
			p.By = map[Level]string{Guarded: "有护栏自动执行", Auto: "自主执行"}[a.Level]
			e.execute(ctx, &p, false)
		}
	}
	e.add(&p)
	return p, nil
}

func (e *Engine) execute(ctx context.Context, p *Proposal, confirm bool) {
	p.Status = "done"
	var failed []string
	for _, act := range p.Actions {
		if _, err := e.hub.Command(ctx, act.Device, act.Patch, confirm, "auto:"+p.Automation); err != nil {
			failed = append(failed, err.Error())
		}
	}
	if len(failed) > 0 {
		p.Status, p.Reason = "failed", strings.Join(failed, "；")
	}
}

func (e *Engine) add(p *Proposal) {
	e.mu.Lock()
	e.seq++
	p.ID = fmt.Sprintf("p%d", e.seq)
	e.proposals = append([]Proposal{*p}, e.proposals...)
	if len(e.proposals) > 50 {
		e.proposals = e.proposals[:50]
	}
	e.mu.Unlock()
	e.changed()
}

// Ask puts a change someone else asked for (an MQTT client) before a person.
func (e *Engine) Ask(device string, patch map[string]any, who, reason string) Proposal {
	acts := []Action{{device, patch}}
	p := Proposal{Automation: "", Name: who + " 的请求", Why: who + " 请求", Actions: acts, Steps: e.steps(acts), Status: "pending", Reason: reason, T: e.now().UnixMilli()}
	e.add(&p)
	return p
}

// Decide approves or rejects a pending proposal; approving runs it with the person's consent.
func (e *Engine) Decide(ctx context.Context, id string, approve bool, by string) (Proposal, error) {
	e.mu.Lock()
	i := slices.IndexFunc(e.proposals, func(p Proposal) bool { return p.ID == id })
	if i < 0 || e.proposals[i].Status != "pending" {
		e.mu.Unlock()
		return Proposal{}, fmt.Errorf("没有等待确认的这一条")
	}
	p := e.proposals[i]
	e.mu.Unlock()
	if approve {
		p.By = by + " 批准"
		e.execute(ctx, &p, true)
	} else {
		p.Status, p.By = "rejected", by+" 拒绝"
	}
	e.mu.Lock()
	if j := slices.IndexFunc(e.proposals, func(q Proposal) bool { return q.ID == id }); j >= 0 {
		e.proposals[j] = p
	}
	e.mu.Unlock()
	e.changed()
	return p, nil
}

// OnDevice fires state triggers when a device's field crosses into the condition.
func (e *Engine) OnDevice(ctx context.Context, ev devices.Event) {
	if ev.Type != "device" || ev.Device == nil || strings.HasPrefix(ev.By, "auto:") {
		return
	}
	d := ev.Device
	for _, a := range e.Automations() {
		t := a.Trigger
		if !a.Enabled || t.Kind != "state" || t.Device != d.ID {
			continue
		}
		v, ok := d.State[t.Field]
		if !ok {
			continue
		}
		hit := match(v, t.Op, t.Value)
		key := a.ID
		e.mu.Lock()
		was := e.lastState[key]
		e.lastState[key] = hit
		e.mu.Unlock()
		if hit && !was {
			e.Run(ctx, a.ID, fmt.Sprintf("%s：%s", a.When, Describe(*d, map[string]any{t.Field: v})))
		}
	}
}

func match(v any, op string, want any) bool {
	switch w := want.(type) {
	case bool:
		b, ok := v.(bool)
		return ok && op == "=" && b == w
	case float64:
		n, ok := v.(float64)
		if !ok {
			return false
		}
		switch op {
		case ">":
			return n > w
		case "<":
			return n < w
		case "=":
			return n == w
		}
	case string:
		s, ok := v.(string)
		return ok && op == "=" && s == w
	}
	return false
}

// OnHint fires hint triggers when a hint starts.
func (e *Engine) OnHint(ctx context.Context, h insight.Hint) {
	for _, a := range e.Automations() {
		t := a.Trigger
		if a.Enabled && t.Kind == "hint" && t.HintKind == h.Kind && (t.Room == "" || t.Room == h.Room) {
			e.Run(ctx, a.ID, h.Text)
		}
	}
}

// Tick fires time triggers; call it every few seconds.
func (e *Engine) Tick(ctx context.Context) {
	now := e.now()
	hm := now.Format("15:04")
	day := now.Format("2006-01-02")
	for _, a := range e.Automations() {
		if !a.Enabled || a.Trigger.Kind != "time" || a.Trigger.At != hm {
			continue
		}
		e.mu.Lock()
		done := e.lastDay[a.ID] == day
		e.lastDay[a.ID] = day
		e.mu.Unlock()
		if !done {
			e.Run(ctx, a.ID, a.When)
		}
	}
}

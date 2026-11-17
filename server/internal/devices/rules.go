package devices

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// NeedConfirm is returned for a change a person has to confirm (unlocking the front door).
type NeedConfirm struct{ Text string }

func (e *NeedConfirm) Error() string { return e.Text }

// Refused is returned for a change the rules do not allow.
type Refused struct{ Text string }

func (e *Refused) Error() string { return e.Text }

func refuse(f string, a ...any) error { return &Refused{fmt.Sprintf(f, a...)} }

type field struct {
	kind     string // bool, number, enum
	min, max float64
	values   []string
	label    string
}

var fields = map[string]map[string]field{
	Light: {
		"on":         {kind: "bool", label: "开关"},
		"brightness": {kind: "number", min: 1, max: 100, label: "亮度"},
		"ct":         {kind: "number", min: 2700, max: 6500, label: "色温"},
	},
	AC: {
		"on":     {kind: "bool", label: "开关"},
		"mode":   {kind: "enum", values: []string{"cool", "heat", "dry", "fan", "auto"}, label: "模式"},
		"target": {kind: "number", min: 16, max: 30, label: "温度"},
		"fan":    {kind: "enum", values: []string{"auto", "low", "mid", "high"}, label: "风速"},
	},
	Lock:    {"locked": {kind: "bool", label: "门锁"}},
	Curtain: {"position": {kind: "number", min: 0, max: 100, label: "开合"}},
	Vacuum:  {"phase": {kind: "enum", values: []string{"cleaning", "paused", "returning"}, label: "动作"}},
}

var units = map[string]string{"brightness": "%", "ct": "K", "target": " ℃", "position": "%"}

// Check is the policy every change passes before it reaches a device, whoever asks for it: a
// person in a browser, an MQTT client, an automation. Ranges are hard limits; unlocking the
// front door always needs a person to confirm, so no automation can do it alone.
func Check(d Device, patch map[string]any, confirm bool) error {
	if d.Kind == Sensor {
		return refuse("%s只能读数，不能设置", d.Name)
	}
	if len(patch) == 0 {
		return refuse("没有要改的内容")
	}
	allowed := fields[d.Kind]
	keys := make([]string, 0, len(patch))
	for k := range patch {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := patch[k]
		f, ok := allowed[k]
		if !ok {
			names := []string{}
			for n := range allowed {
				names = append(names, n)
			}
			sort.Strings(names)
			return refuse("%s没有「%s」可以设，能设的是 %s", d.Name, k, strings.Join(names, "、"))
		}
		switch f.kind {
		case "bool":
			if _, ok := v.(bool); !ok {
				return refuse("%s的%s要是 true 或 false", d.Name, f.label)
			}
		case "number":
			n, ok := num(v)
			if !ok {
				return refuse("%s的%s要是数字", d.Name, f.label)
			}
			if n < f.min || n > f.max {
				u := units[k]
				return refuse("%s的%s只能设 %g～%g%s，要的是 %g%s", d.Name, f.label, f.min, f.max, u, n, u)
			}
		case "enum":
			s, ok := v.(string)
			if !ok || !slices.Contains(f.values, s) {
				return refuse("%s的%s只能是 %s", d.Name, f.label, strings.Join(f.values, "、"))
			}
		}
	}
	if d.Kind == Lock {
		if locked, _ := patch["locked"].(bool); !locked && !confirm {
			return &NeedConfirm{fmt.Sprintf("确定要打开%s吗？开锁后门从外面就能推开。", d.Name)}
		}
	}
	if d.Kind == Vacuum && patch["phase"] == "cleaning" {
		if b, _ := num(d.State["battery"]); b < 20 {
			return refuse("%s电量只有 %.0f%%，先回座充电再扫", d.Name, b)
		}
	}
	return nil
}

func num(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

package ha

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"hometwin/internal/devices"
)

// KindOf is the HomeTwin kind for an HA entity, "" when the twin has no model for it.
func KindOf(entityID string, attrs map[string]any) string {
	domain, _, _ := strings.Cut(entityID, ".")
	switch domain {
	case "light":
		return devices.Light
	case "climate":
		return devices.AC
	case "lock":
		return devices.Lock
	case "cover":
		return devices.Curtain
	case "vacuum":
		return devices.Vacuum
	case "sensor":
		switch deviceClass(attrs) {
		case "temperature", "humidity":
			return devices.Sensor
		}
	}
	return ""
}

func deviceClass(attrs map[string]any) string {
	s, _ := attrs["device_class"].(string)
	return s
}

func num(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		return f, err == nil
	}
	return 0, false
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

// fromHA is the twin's state fields for an HA entity's state.
func fromHA(kind string, s State) map[string]any {
	a := s.Attributes
	out := map[string]any{}
	if s.State == "unavailable" || s.State == "unknown" {
		out["available"] = false
		return out
	}
	out["available"] = true
	switch kind {
	case devices.Light:
		out["on"] = s.State == "on"
		if b, ok := num(a["brightness"]); ok {
			out["brightness"] = math.Max(1, math.Round(b/255*100))
		}
		if k, ok := num(a["color_temp_kelvin"]); ok {
			out["ct"] = math.Round(k)
		}
	case devices.AC:
		out["on"] = s.State != "off"
		if m, ok := map[string]string{"cool": "cool", "heat": "heat", "dry": "dry", "fan_only": "fan", "heat_cool": "auto", "auto": "auto"}[s.State]; ok {
			out["mode"] = m
		}
		if v, ok := num(a["temperature"]); ok {
			out["target"] = v
		}
		if v, ok := num(a["current_temperature"]); ok {
			out["current"] = round1(v)
		}
		if f, ok := a["fan_mode"].(string); ok {
			out["fan"] = map[string]string{"auto": "auto", "low": "low", "medium": "mid", "mid": "mid", "high": "high", "on_low": "low", "on_high": "high"}[strings.ToLower(f)]
			if out["fan"] == "" {
				out["fan"] = "auto"
			}
		}
		if act, ok := a["hvac_action"].(string); ok {
			out["running"] = act == "cooling" || act == "heating" || act == "drying" || act == "fan"
		}
	case devices.Lock:
		out["locked"] = s.State == "locked"
		out["open"] = s.State == "open" || s.State == "opening"
		out["jammed"] = s.State == "jammed"
	case devices.Curtain:
		if v, ok := num(a["current_position"]); ok {
			out["position"], out["target"] = v, v
		} else if s.State == "open" || s.State == "opening" {
			out["position"], out["target"] = 100.0, 100.0
		} else if s.State == "closed" || s.State == "closing" {
			out["position"], out["target"] = 0.0, 0.0
		}
	case devices.Vacuum:
		ph := map[string]string{"cleaning": "cleaning", "paused": "paused", "returning": "returning", "docked": "docked", "idle": "docked", "error": "paused"}[s.State]
		if ph == "" {
			ph = "docked"
		}
		out["phase"] = ph
		out["error"] = s.State == "error"
		if v, ok := num(a["battery_level"]); ok {
			out["battery"] = v
		}
	case devices.Sensor:
		v, ok := num(s.State)
		if !ok {
			break
		}
		switch deviceClass(a) {
		case "temperature":
			if u, _ := a["unit_of_measurement"].(string); u == "°F" {
				v = (v - 32) * 5 / 9
			}
			out["temp"] = round1(v)
		case "humidity":
			out["humidity"] = math.Round(v)
		}
	}
	return out
}

// call is one service call.
type call struct {
	Domain, Service string
	Data            map[string]any
	Entity          string
}

// supports says whether a cover can be set to a position (supported_features bit 4).
func supportsPosition(attrs map[string]any) bool {
	if _, ok := attrs["current_position"]; ok {
		return true
	}
	f, _ := num(attrs["supported_features"])
	return int(f)&4 != 0
}

// calls turns a twin change into HA service calls, in order. attrs are the entity's current
// attributes (nil when unknown).
func calls(d devices.Device, patch map[string]any, attrs map[string]any) ([]call, error) {
	e := d.Entity
	if e == "" {
		return nil, fmt.Errorf("%s 没有对应的 Home Assistant 实体", d.Name)
	}
	domain, _, _ := strings.Cut(e, ".")
	one := func(svc string, data map[string]any) []call { return []call{{domain, svc, data, e}} }
	switch d.Kind {
	case devices.Light:
		if on, ok := patch["on"].(bool); ok && !on {
			return one("turn_off", nil), nil
		}
		data := map[string]any{}
		if v, ok := num(patch["brightness"]); ok {
			data["brightness_pct"] = v
		}
		if v, ok := num(patch["ct"]); ok {
			if lo, ok := num(attrs["min_color_temp_kelvin"]); ok && v < lo {
				v = lo
			}
			if hi, ok := num(attrs["max_color_temp_kelvin"]); ok && v > hi {
				v = hi
			}
			data["color_temp_kelvin"] = v
		}
		return one("turn_on", data), nil
	case devices.AC:
		if on, ok := patch["on"].(bool); ok && !on {
			return one("turn_off", nil), nil
		}
		var out []call
		if m, ok := patch["mode"].(string); ok {
			out = append(out, call{domain, "set_hvac_mode", map[string]any{"hvac_mode": map[string]string{"cool": "cool", "heat": "heat", "dry": "dry", "fan": "fan_only", "auto": "heat_cool"}[m]}, e})
		} else if on, ok := patch["on"].(bool); ok && on {
			out = append(out, call{domain, "turn_on", nil, e})
		}
		if v, ok := num(patch["target"]); ok {
			out = append(out, call{domain, "set_temperature", map[string]any{"temperature": v}, e})
		}
		if f, ok := patch["fan"].(string); ok {
			out = append(out, call{domain, "set_fan_mode", map[string]any{"fan_mode": map[string]string{"auto": "auto", "low": "low", "mid": "medium", "high": "high"}[f]}, e})
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("%s 的这个改动在 Home Assistant 里没有对应的服务", d.Name)
		}
		return out, nil
	case devices.Lock:
		if l, ok := patch["locked"].(bool); ok {
			if l {
				return one("lock", nil), nil
			}
			return one("unlock", nil), nil
		}
	case devices.Curtain:
		if v, ok := num(patch["position"]); ok {
			if supportsPosition(attrs) || attrs == nil {
				return one("set_cover_position", map[string]any{"position": v}), nil
			}
			if v >= 50 {
				return one("open_cover", nil), nil
			}
			return one("close_cover", nil), nil
		}
	case devices.Vacuum:
		switch patch["phase"] {
		case "cleaning":
			return one("start", nil), nil
		case "paused":
			return one("pause", nil), nil
		case "returning":
			return one("return_to_base", nil), nil
		}
	case devices.Sensor:
		return nil, fmt.Errorf("%s只能读数", d.Name)
	}
	return nil, fmt.Errorf("%s 的这个改动在 Home Assistant 里没有对应的服务", d.Name)
}

// expected is the state the twin shows until HA confirms the change.
func expected(kind string, patch map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range patch {
		out[k] = v
	}
	if kind == devices.Curtain {
		if v, ok := patch["position"]; ok {
			delete(out, "position")
			out["target"] = v
		}
	}
	return out
}

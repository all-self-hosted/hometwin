package devices

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"sync"
	"time"

	"hometwin/internal/home"
	"hometwin/internal/predict"
)

// Sim is a simulated home: every device answers at once, rooms warm and cool by the thermal
// model (with its own, slightly different time constants, so the twin's prediction has
// something to be wrong about), sensors read with a little noise, the robot cleans and
// docks, curtains travel. Speed runs its clock faster than the wall clock.
type Sim struct {
	Speed float64

	mu      sync.Mutex
	rooms   map[string]*air
	outdoor float64
	shower  float64 // seconds of shower left
	lastRep map[string]time.Time
}

type air struct {
	model predict.Room
	t, rh float64
	base  float64 // humidity the room settles at
}

func NewSim(h home.Home) *Sim {
	s := &Sim{Speed: 1, rooms: map[string]*air{}, lastRep: map[string]time.Time{}}
	for _, r := range h.Rooms {
		m := predict.RoomFor(home.Area(r.Poly))
		m.Tau *= 1.15
		base := 62.0
		if r.Kind == "bath" {
			base = 66
		}
		t := 25.4
		if r.Kind == "bath" || r.Kind == "bedroom" {
			t = 24.9
		}
		s.rooms[r.ID] = &air{model: m, t: t, rh: base, base: base}
	}
	s.outdoor = outdoorAt(time.Now())
	return s
}

// outdoorAt is a Chengdu October day: 17 ℃ before dawn, 25 ℃ mid-afternoon.
func outdoorAt(t time.Time) float64 {
	h := float64(t.Hour()) + float64(t.Minute())/60
	return 21 + 4*math.Sin(2*math.Pi*(h-9)/24)
}

func (s *Sim) Name() string { return "sim" }

func (s *Sim) Apply(_ context.Context, d Device, patch map[string]any) (map[string]any, error) {
	out := map[string]any{}
	for k, v := range patch {
		out[k] = v
	}
	switch d.Kind {
	case Curtain:
		// the curtain travels there in Run
		delete(out, "position")
		out["target"] = patch["position"]
	case Vacuum:
		if patch["phase"] == "cleaning" && d.State["phase"] != "paused" {
			out["progress"] = 0.0
		}
		if patch["phase"] == "returning" {
			out["back"] = 0.0
		}
	case Lock:
		if locked, _ := patch["locked"].(bool); locked && d.State["open"] == true {
			return nil, &Refused{"门还开着，关上门才能上锁"}
		}
	case AC:
		if t, ok := s.roomTemp(d.Room); ok {
			on, _ := mergeBool(d.State, patch, "on")
			mode := mergeStr(d.State, patch, "mode")
			target := mergeNum(d.State, patch, "target")
			out["running"] = on && predict.Running(t, target, mode)
		}
	}
	return out, nil
}

func mergeBool(st, p map[string]any, k string) (bool, bool) {
	if v, ok := p[k].(bool); ok {
		return v, true
	}
	v, ok := st[k].(bool)
	return v, ok
}
func mergeStr(st, p map[string]any, k string) string {
	if v, ok := p[k].(string); ok {
		return v
	}
	v, _ := st[k].(string)
	return v
}
func mergeNum(st, p map[string]any, k string) float64 {
	if v, ok := num(p[k]); ok {
		return v
	}
	v, _ := num(st[k])
	return v
}

func (s *Sim) roomTemp(room string) (float64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.rooms[room]
	if !ok {
		return 0, false
	}
	return a.t, true
}

// Scenario plays something that happens in a home, for trying the twin's hints.
func (s *Sim) Scenario(h *Hub, name string) error {
	switch name {
	case "shower":
		s.mu.Lock()
		s.shower = 240
		s.mu.Unlock()
	case "door-open", "door-close":
		for _, d := range h.List() {
			if d.Kind == Lock && d.Source == "sim" {
				open := name == "door-open"
				st := map[string]any{"open": open}
				if open {
					st["locked"] = false
				}
				h.Report(d.ID, st, "sim")
			}
		}
	case "hot":
		s.mu.Lock()
		for _, a := range s.rooms {
			a.t += 3
		}
		s.mu.Unlock()
	default:
		return fmt.Errorf("没有「%s」这个场景，能用的是 shower、door-open、door-close、hot", name)
	}
	return nil
}

// Run steps the simulated home twice a second.
func (s *Sim) Run(ctx context.Context, h *Hub) {
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	last := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick.C:
			dt := now.Sub(last).Seconds() * s.Speed
			last = now
			s.step(h, dt, now)
		}
	}
}

func (s *Sim) step(h *Hub, dt float64, now time.Time) {
	devs := h.List()
	s.mu.Lock()
	s.outdoor = outdoorAt(now)
	ac := map[string]Device{}
	for _, d := range devs {
		if d.Kind == AC && d.Source == "sim" {
			ac[d.Room] = d
		}
	}
	for id, a := range s.rooms {
		d, has := ac[id]
		on := has && d.State["on"] == true
		mode, _ := d.State["mode"].(string)
		target, _ := num(d.State["target"])
		a.t = a.model.Step(a.t, dt, s.outdoor, on, mode, target)
		// humidity settles toward the room's own level; cooling and drying take it out
		want := a.base
		if on && (mode == "cool" || mode == "dry") {
			want = 50
		}
		if id == "bath" && s.shower > 0 {
			want, a.t = 94, a.t+0.004*dt
		}
		k := 1 / 900.0
		if id == "bath" && s.shower > 0 {
			k = 1 / 50.0
		}
		a.rh += (want - a.rh) * math.Min(1, k*dt)
	}
	if s.shower > 0 {
		s.shower -= dt
	}
	rooms := map[string][2]float64{}
	for id, a := range s.rooms {
		rooms[id] = [2]float64{a.t, a.rh}
	}
	outdoor := s.outdoor
	s.mu.Unlock()

	for _, d := range devs {
		if d.Source != "sim" {
			continue
		}
		switch d.Kind {
		case Sensor:
			if now.Sub(s.lastRep[d.ID]) < time.Duration(float64(5*time.Second)/math.Max(1, s.Speed)) {
				continue
			}
			s.lastRep[d.ID] = now
			t, rh := outdoor, 70.0
			if r, ok := rooms[d.Room]; ok {
				t, rh = r[0], r[1]
			}
			h.Report(d.ID, map[string]any{
				"temp":     round1(t + rand.NormFloat64()*0.05),
				"humidity": math.Round(rh + rand.NormFloat64()*0.3),
			}, "sim")
		case AC:
			r, ok := rooms[d.Room]
			if !ok {
				continue
			}
			mode, _ := d.State["mode"].(string)
			target, _ := num(d.State["target"])
			h.Report(d.ID, map[string]any{
				"current": round1(r[0]),
				"running": d.State["on"] == true && predict.Running(r[0], target, mode),
			}, "sim")
		case Curtain:
			pos, _ := num(d.State["position"])
			target, _ := num(d.State["target"])
			if pos != target {
				step := 20 * dt
				if math.Abs(target-pos) <= step {
					pos = target
				} else {
					pos += math.Copysign(step, target-pos)
				}
				h.Report(d.ID, map[string]any{"position": math.Round(pos)}, "sim")
			}
		case Vacuum:
			s.vacuum(h, d, dt)
		}
	}
}

// Advance runs the simulated home forward by seconds of its own clock, starting at start
// (tests and replays use this instead of Run).
func (s *Sim) Advance(h *Hub, start time.Time, seconds float64) time.Time {
	now := start
	for t := 0.0; t < seconds; t += 0.5 {
		now = now.Add(500 * time.Millisecond)
		s.step(h, 0.5, now)
	}
	return now
}

// the robot cleans the whole home in about two minutes of its clock, then goes home
const cleanSeconds = 120.0
const returnSeconds = 12.0

func (s *Sim) vacuum(h *Hub, d Device, dt float64) {
	phase, _ := d.State["phase"].(string)
	prog, _ := num(d.State["progress"])
	bat, _ := num(d.State["battery"])
	st := map[string]any{}
	switch phase {
	case "cleaning":
		prog = math.Min(1, prog+dt/cleanSeconds)
		bat = math.Max(0, bat-dt*0.35)
		st["progress"], st["battery"] = round3(prog), round1(bat)
		if prog >= 1 || bat < 15 {
			st["phase"], st["back"] = "returning", 0.0
		}
	case "returning":
		back, _ := num(d.State["back"])
		back = math.Min(1, back+dt/returnSeconds)
		st["back"] = round3(back)
		if back >= 1 {
			st["phase"] = "docked"
		}
	case "docked":
		if bat < 100 {
			st["battery"] = round1(math.Min(100, bat+dt*0.8))
		}
	}
	if len(st) > 0 {
		h.Report(d.ID, st, "sim")
	}
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }
func round3(v float64) float64 { return math.Round(v*1e5) / 1e5 }

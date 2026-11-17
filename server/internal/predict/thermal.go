// Package predict holds the twin's models of how the home will behave: for now one lumped
// thermal model per room (Newton's law of cooling with an AC term), used to say how long a
// room takes to reach the temperature asked for, and to try a setting before using it.
package predict

import (
	"math"
	"sync"
	"time"

	"hometwin/internal/home"
)

// Room is the thermal model of one room:
//
//	dT/dt = (KH·(Supply − T)·running + KE·(Outdoor − T)) / Tau
//
// Tau grows with the room's floor area; KH is how hard the AC pulls, KE how much the outside
// leaks in.
type Room struct {
	Tau float64 // seconds
	KH  float64
	KE  float64
}

// Supply air temperature the AC blows in each mode.
var Supply = map[string]float64{"cool": 14, "heat": 40}

// RoomFor sizes a room's model by its floor area.
func RoomFor(area float64) Room {
	return Room{Tau: 1800 * math.Max(0.6, area/15), KH: 0.8, KE: 0.1}
}

// Rate is dT/dt in ℃ per second.
func (r Room) Rate(t, outdoor float64, mode string, running bool) float64 {
	rate := r.KE * (outdoor - t)
	if s, ok := Supply[mode]; ok && running {
		rate += r.KH * (s - t)
	}
	return rate / r.Tau
}

// Step moves T on by dt seconds with an AC that holds target in mode (on false: off).
func (r Room) Step(t, dt, outdoor float64, on bool, mode string, target float64) float64 {
	running := on && Running(t, target, mode)
	n := t + r.Rate(t, outdoor, mode, running)*dt
	// the thermostat stops at the target instead of overshooting it
	if running && mode == "cool" && n < target {
		n = target
	}
	if running && mode == "heat" && n > target {
		n = target
	}
	return n
}

// Running says whether the AC's compressor works at room temperature t.
func Running(t, target float64, mode string) bool {
	switch mode {
	case "cool":
		return t > target+0.05
	case "heat":
		return t < target-0.05
	}
	return false
}

// ETA is how many seconds the room takes from t to target with the AC running in mode, and
// false when it never gets there (the AC cannot pull past where it balances the outside).
func (r Room) ETA(t, target, outdoor float64, mode string) (float64, bool) {
	s, ok := Supply[mode]
	if !ok {
		return 0, false
	}
	if (mode == "cool" && t <= target) || (mode == "heat" && t >= target) {
		return 0, true
	}
	k := r.KH + r.KE
	eq := (r.KH*s + r.KE*outdoor) / k
	// T(t) = eq + (T0 − eq)·e^(−k t / Tau)
	ratio := (target - eq) / (t - eq)
	if ratio <= 0 || ratio >= 1 {
		return 0, false
	}
	return -r.Tau / k * math.Log(ratio), true
}

// Point is one step of a simulated run.
type Point struct {
	S float64 `json:"s"` // seconds from now
	T float64 `json:"t"` // ℃
}

// Simulate runs the room forward for horizon seconds, a point every step seconds: what the
// room would do with this setting, before it is used.
func (r Room) Simulate(t, outdoor float64, on bool, mode string, target, horizon, step float64) []Point {
	out := []Point{{0, t}}
	for s := step; s <= horizon+1e-9; s += step {
		for k := 0; k < 10; k++ {
			t = r.Step(t, step/10, outdoor, on, mode, target)
		}
		out = append(out, Point{s, math.Round(t*100) / 100})
	}
	return out
}

// Estimator keeps the twin's best guess of each room's temperature: the model runs forward
// between readings, and each reading pulls the guess toward what was measured (a simple form
// of data assimilation, so a noisy sensor does not make the prediction jump).
type Estimator struct {
	mu      sync.Mutex
	rooms   map[string]Room
	est     map[string]float64
	at      map[string]time.Time
	outdoor float64
	gain    float64
}

func NewEstimator(h home.Home) *Estimator {
	e := &Estimator{rooms: map[string]Room{}, est: map[string]float64{}, at: map[string]time.Time{}, outdoor: 22, gain: 0.35}
	for _, r := range h.Rooms {
		e.rooms[r.ID] = RoomFor(home.Area(r.Poly))
	}
	return e
}

func (e *Estimator) Model(room string) (Room, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	r, ok := e.rooms[room]
	return r, ok
}

// Observe takes a temperature reading of a room ("outdoors" for outside).
func (e *Estimator) Observe(room string, t float64, at time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if room == "outdoors" {
		e.outdoor = t
		return
	}
	prev, ok := e.est[room]
	if !ok {
		e.est[room] = t
	} else {
		e.est[room] = prev + e.gain*(t-prev)
	}
	e.at[room] = at
}

// Temp is the estimated temperature of a room and the outdoor temperature.
func (e *Estimator) Temp(room string) (t, outdoor float64, ok bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok = e.est[room]
	return t, e.outdoor, ok
}

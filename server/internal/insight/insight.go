// Package insight watches the device stream for things a person should know: a door left
// open, the front door unlocked for long, a reading that jumps away from its recent normal.
// Fixed rules catch what is wrong in itself (a door open for minutes); a rolling z-score
// catches what is only wrong against its own recent past (humidity 66% is fine, rising to 85%
// in a minute is not).
package insight

import (
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"hometwin/internal/devices"
)

// Hint is one thing worth knowing.
type Hint struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`  // door-open, unlocked, spike, vacuum-low
	Level  string `json:"level"` // info, warn
	Device string `json:"device"`
	Room   string `json:"room"`
	Text   string `json:"text"`
	// Since is when it started, unix ms.
	Since int64 `json:"since"`
}

var (
	// DoorOpenAfter is how long a door may stand open before it is mentioned.
	DoorOpenAfter = 2 * time.Minute
	// UnlockedAfter is how long the front door may stay unlocked (and shut).
	UnlockedAfter = 10 * time.Minute
	// Window is how many recent readings make a sensor's normal.
	Window = 36
	// Z is how many standard deviations away a reading must be to count as a jump.
	Z = 3.0
)

// jumps are the least change that counts, whatever the z-score says
var minJump = map[string]float64{"humidity": 6, "temp": 2}
var fieldName = map[string]string{"humidity": "湿度", "temp": "温度"}
var fieldUnit = map[string]string{"humidity": "%", "temp": " ℃"}

type series struct {
	vals []float64
	// base is the normal before a jump, while the jump lasts
	base float64
	jump bool
}

func (s *series) stats() (mean, std float64) {
	for _, v := range s.vals {
		mean += v
	}
	mean /= float64(len(s.vals))
	for _, v := range s.vals {
		std += (v - mean) * (v - mean)
	}
	return mean, math.Sqrt(std / float64(len(s.vals)))
}

type Engine struct {
	Rooms map[string]string // id → name
	now   func() time.Time

	mu       sync.Mutex
	hints    map[string]*Hint
	openAt   map[string]time.Time
	unlockAt map[string]time.Time
	series   map[string]*series
	names    map[string]devices.Device
	onChange []func([]Hint, *Hint)
}

func New(rooms map[string]string) *Engine {
	return &Engine{Rooms: rooms, now: time.Now, hints: map[string]*Hint{}, openAt: map[string]time.Time{}, unlockAt: map[string]time.Time{}, series: map[string]*series{}, names: map[string]devices.Device{}}
}

// Listen calls fn with the active hints whenever they change, and the hint that just started
// (nil when one ended).
func (e *Engine) Listen(fn func(all []Hint, started *Hint)) {
	e.mu.Lock()
	e.onChange = append(e.onChange, fn)
	e.mu.Unlock()
}

// Active is every hint in force, newest first.
func (e *Engine) Active() []Hint {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.active()
}

func (e *Engine) active() []Hint {
	out := make([]Hint, 0, len(e.hints))
	for _, h := range e.hints {
		out = append(out, *h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Since > out[j].Since })
	return out
}

// set puts a hint in force: started is the hint when it is new, changed whether anything
// differs from before.
func (e *Engine) set(h Hint) (started *Hint, changed bool) {
	if old, ok := e.hints[h.ID]; ok {
		h.Since = old.Since
		if *old == h {
			return nil, false
		}
		*old = h
		return nil, true
	}
	if h.Since == 0 {
		h.Since = e.now().UnixMilli()
	}
	e.hints[h.ID] = &h
	return &h, true
}

func (e *Engine) clear(id string) bool {
	if _, ok := e.hints[id]; !ok {
		return false
	}
	delete(e.hints, id)
	return true
}

func (e *Engine) room(id string) string {
	if n, ok := e.Rooms[id]; ok {
		return n
	}
	if id == "outdoors" {
		return "室外"
	}
	return id
}

// Observe takes a device change.
func (e *Engine) Observe(ev devices.Event) {
	if ev.Type != "device" || ev.Device == nil {
		return
	}
	d := *ev.Device
	e.mu.Lock()
	e.names[d.ID] = d
	changed := false
	var started *Hint
	now := e.now()
	switch d.Kind {
	case devices.Lock:
		if d.State["open"] == true {
			if _, ok := e.openAt[d.ID]; !ok {
				e.openAt[d.ID] = now
			}
		} else {
			delete(e.openAt, d.ID)
			changed = e.clear("door-open:"+d.ID) || changed
		}
		if d.State["locked"] == false && d.State["open"] != true {
			if _, ok := e.unlockAt[d.ID]; !ok {
				e.unlockAt[d.ID] = now
			}
		} else {
			delete(e.unlockAt, d.ID)
			changed = e.clear("unlocked:"+d.ID) || changed
		}
	case devices.Sensor:
		for _, f := range []string{"humidity", "temp"} {
			v, ok := d.State[f].(float64)
			if !ok {
				continue
			}
			key := d.ID + "/" + f
			s := e.series[key]
			if s == nil {
				s = &series{}
				e.series[key] = s
			}
			id := "spike:" + key
			if s.jump {
				if math.Abs(v-s.base) < minJump[f]/2 {
					s.jump = false
					changed = e.clear(id) || changed
				}
			} else if len(s.vals) >= 12 {
				mean, std := s.stats()
				std = math.Max(std, 0.3)
				if (v-mean)/std > Z && v-mean >= minJump[f] {
					s.jump, s.base = true, mean
					h := Hint{ID: id, Kind: "spike", Level: "warn", Device: d.ID, Room: d.Room,
						Text: fmt.Sprintf("%s%s突然升高：%.0f%s → %.0f%s", e.room(d.Room), fieldName[f], mean, fieldUnit[f], v, fieldUnit[f])}
					if st, _ := e.set(h); st != nil {
						started, changed = st, true
					}
				}
			}
			if s.jump {
				// keep the jump's text current
				if h := e.hints[id]; h != nil {
					changed = true
					h.Text = fmt.Sprintf("%s%s突然升高：%.0f%s → %.0f%s", e.room(d.Room), fieldName[f], s.base, fieldUnit[f], v, fieldUnit[f])
				}
			} else {
				s.vals = append(s.vals, v)
				if len(s.vals) > Window {
					s.vals = s.vals[1:]
				}
			}
		}
	case devices.Vacuum:
		b, _ := d.State["battery"].(float64)
		if d.State["phase"] == "cleaning" && b < 20 {
			if st, _ := e.set(Hint{ID: "vacuum-low:" + d.ID, Kind: "vacuum-low", Level: "info", Device: d.ID, Room: d.Room, Text: fmt.Sprintf("%s电量 %.0f%%，扫完这一段就回去充电", d.Name, b)}); st != nil {
				started, changed = st, true
			}
		} else {
			changed = e.clear("vacuum-low:"+d.ID) || changed
		}
	}
	st, ch := e.tick(now)
	if st != nil {
		started = st
	}
	changed = changed || ch
	e.emit(changed, started)
}

// Tick looks at the rules that depend on how long something has lasted; call it now and then.
func (e *Engine) Tick() {
	e.mu.Lock()
	st, ch := e.tick(e.now())
	e.emit(ch, st)
}

// emit unlocks e.mu.
func (e *Engine) emit(changed bool, started *Hint) {
	if !changed {
		e.mu.Unlock()
		return
	}
	all := e.active()
	fns := append([]func([]Hint, *Hint){}, e.onChange...)
	e.mu.Unlock()
	for _, fn := range fns {
		fn(all, started)
	}
}

func (e *Engine) tick(now time.Time) (started *Hint, changed bool) {
	for id, at := range e.openAt {
		if now.Sub(at) < DoorOpenAfter {
			continue
		}
		d := e.names[id]
		h := Hint{ID: "door-open:" + id, Kind: "door-open", Level: "warn", Device: id, Room: d.Room, Since: at.UnixMilli(),
			Text: fmt.Sprintf("%s对应的门开着已经 %d 分钟了", d.Name, int(now.Sub(at).Minutes()))}
		st, ch := e.set(h)
		if st != nil {
			started = st
		}
		changed = changed || ch
	}
	for id, at := range e.unlockAt {
		if now.Sub(at) < UnlockedAfter {
			continue
		}
		d := e.names[id]
		h := Hint{ID: "unlocked:" + id, Kind: "unlocked", Level: "info", Device: id, Room: d.Room, Since: at.UnixMilli(),
			Text: fmt.Sprintf("%s没锁，已经 %d 分钟了", d.Name, int(now.Sub(at).Minutes()))}
		st, ch := e.set(h)
		if st != nil {
			started = st
		}
		changed = changed || ch
	}
	return
}

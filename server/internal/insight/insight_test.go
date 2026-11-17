package insight

import (
	"strings"
	"testing"
	"time"

	"hometwin/internal/devices"
	"hometwin/internal/home"
)

func ev(d devices.Device) devices.Event { return devices.Event{Type: "device", Device: &d} }

func TestDoorOpen(t *testing.T) {
	e := New(map[string]string{"entry": "玄关"})
	now := time.Date(2026, 10, 17, 20, 0, 0, 0, time.Local)
	e.now = func() time.Time { return now }
	var started []Hint
	e.Listen(func(_ []Hint, s *Hint) {
		if s != nil {
			started = append(started, *s)
		}
	})
	lock := devices.Device{ID: "lock-entry", Kind: devices.Lock, Name: "入户门锁", Room: "entry", State: map[string]any{"locked": false, "open": true}}
	e.Observe(ev(lock))
	now = now.Add(time.Minute)
	e.Tick()
	if len(e.Active()) != 0 {
		t.Fatal("one minute open is not worth a hint")
	}
	now = now.Add(2 * time.Minute)
	e.Tick()
	a := e.Active()
	if len(a) != 1 || a[0].Kind != "door-open" || !strings.Contains(a[0].Text, "3 分钟") {
		t.Fatalf("%+v", a)
	}
	if len(started) != 1 {
		t.Fatalf("started %d times", len(started))
	}
	e.Tick()
	if len(started) != 1 {
		t.Fatal("the same hint started again")
	}
	lock.State = map[string]any{"locked": true, "open": false}
	e.Observe(ev(lock))
	if len(e.Active()) != 0 {
		t.Fatalf("door shut and locked: %+v", e.Active())
	}
}

func TestUnlockedTooLong(t *testing.T) {
	e := New(nil)
	now := time.Now()
	e.now = func() time.Time { return now }
	e.Observe(ev(devices.Device{ID: "l", Kind: devices.Lock, Name: "入户门锁", State: map[string]any{"locked": false, "open": false}}))
	now = now.Add(11 * time.Minute)
	e.Tick()
	if a := e.Active(); len(a) != 1 || a[0].Kind != "unlocked" {
		t.Fatalf("%+v", a)
	}
}

func TestHumiditySpike(t *testing.T) {
	e := New(map[string]string{"bath": "卫生间"})
	s := devices.Device{ID: "sensor-bath", Kind: devices.Sensor, Room: "bath"}
	read := func(rh float64) {
		s.State = map[string]any{"humidity": rh, "temp": 24.8}
		e.Observe(ev(s))
	}
	for i := 0; i < 30; i++ {
		read(65 + float64(i%3) - 1) // 64..66, normal wobble
	}
	if len(e.Active()) != 0 {
		t.Fatalf("wobble is normal: %+v", e.Active())
	}
	read(70) // up 5: not enough
	if len(e.Active()) != 0 {
		t.Fatalf("+5 is not a jump: %+v", e.Active())
	}
	read(84)
	a := e.Active()
	if len(a) != 1 || a[0].Kind != "spike" || !strings.Contains(a[0].Text, "卫生间湿度突然升高") {
		t.Fatalf("%+v", a)
	}
	// it ends when the room is back near its normal
	read(80)
	if len(e.Active()) != 1 {
		t.Fatal("still high")
	}
	read(66)
	if len(e.Active()) != 0 {
		t.Fatalf("back to normal: %+v", e.Active())
	}
}

func TestSlowRiseIsNotSpike(t *testing.T) {
	e := New(nil)
	s := devices.Device{ID: "s", Kind: devices.Sensor, Room: "living"}
	for i := 0; i < 200; i++ {
		s.State = map[string]any{"humidity": 60 + float64(i)*0.1}
		e.Observe(ev(s))
	}
	if len(e.Active()) != 0 {
		t.Fatalf("a slow rise follows its own normal: %+v", e.Active())
	}
}

// The simulated home end to end: a shower in the bathroom makes the humidity jump, and the
// hint names the bathroom.
func TestShowerInSimulatedHome(t *testing.T) {
	h, _ := devices.NewHub("chengdu-89", "", nil)
	sim := devices.NewSim(home.Sample())
	h.Use(sim)
	e := New(map[string]string{"bath": "卫生间"})
	h.Listen(e.Observe)
	now := sim.Advance(h, time.Now(), 180)
	if len(e.Active()) != 0 {
		t.Fatalf("a quiet home: %+v", e.Active())
	}
	sim.Scenario(h, "shower")
	sim.Advance(h, now, 60)
	a := e.Active()
	if len(a) == 0 || a[0].Kind != "spike" || !strings.Contains(a[0].Text, "卫生间湿度突然升高") {
		t.Fatalf("after a minute of shower: %+v", a)
	}
}

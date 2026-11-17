package devices

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"hometwin/internal/home"
)

func hub(t *testing.T) (*Hub, *Sim) {
	t.Helper()
	h, err := NewHub("chengdu-89", t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	s := NewSim(home.Sample())
	h.Use(s)
	return h, s
}

func TestDefaults(t *testing.T) {
	ds := Defaults("chengdu-89")
	kinds := map[string]int{}
	topics := map[string]bool{}
	for _, d := range ds {
		kinds[d.Kind]++
		if topics[d.Topic] {
			t.Fatalf("topic %s used twice", d.Topic)
		}
		topics[d.Topic] = true
		if !strings.HasPrefix(d.Topic, "home/"+Domain(d.Kind)+"/"+d.Room+"/") || strings.Count(d.Topic, "/") != 3 {
			t.Fatalf("%s: topic %s is not home/<domain>/<room>/<id>", d.ID, d.Topic)
		}
	}
	for _, k := range Kinds {
		if kinds[k] == 0 {
			t.Fatalf("no %s in the sample", k)
		}
	}
}

func TestRules(t *testing.T) {
	h, _ := hub(t)
	get := func(id string) Device { d, _ := h.Get(id); return d }
	cases := []struct {
		dev     string
		patch   map[string]any
		confirm bool
		want    string // "" ok, "confirm", or a piece of the refusal
	}{
		{"light-living", map[string]any{"on": true, "brightness": 42.0}, false, ""},
		{"light-living", map[string]any{"brightness": 0.0}, false, "1～100"},
		{"light-living", map[string]any{"ct": 9000.0}, false, "2700～6500"},
		{"light-living", map[string]any{"colour": "red"}, false, "没有「colour」"},
		{"light-living", map[string]any{"on": "yes"}, false, "true 或 false"},
		{"ac-living", map[string]any{"target": 24.0}, false, ""},
		{"ac-living", map[string]any{"target": 16.0}, false, ""},
		{"ac-living", map[string]any{"target": 30.0}, false, ""},
		{"ac-living", map[string]any{"target": 15.5}, false, "16～30 ℃"},
		{"ac-living", map[string]any{"target": 31.0}, false, "16～30 ℃"},
		{"ac-living", map[string]any{"mode": "turbo"}, false, "cool、heat"},
		{"lock-entry", map[string]any{"locked": false}, false, "confirm"},
		{"lock-entry", map[string]any{"locked": false}, true, ""},
		{"lock-entry", map[string]any{"locked": true}, false, ""},
		{"curtain-living", map[string]any{"position": 60.0}, false, ""},
		{"curtain-living", map[string]any{"position": 120.0}, false, "0～100"},
		{"sensor-living", map[string]any{"temp": 20.0}, false, "只能读数"},
		{"vacuum", map[string]any{"phase": "cleaning"}, false, ""},
	}
	for _, c := range cases {
		err := Check(get(c.dev), c.patch, c.confirm)
		var nc *NeedConfirm
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s %v: unexpected %v", c.dev, c.patch, err)
		case c.want == "confirm" && !errors.As(err, &nc):
			t.Errorf("%s %v: want a confirmation, got %v", c.dev, c.patch, err)
		case c.want != "" && c.want != "confirm" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s %v: want %q, got %v", c.dev, c.patch, c.want, err)
		}
	}
	// a robot with an empty battery does not start
	v := get("vacuum")
	v.State["battery"] = 12.0
	if err := Check(v, map[string]any{"phase": "cleaning"}, false); err == nil || !strings.Contains(err.Error(), "12%") {
		t.Fatalf("low battery: %v", err)
	}
}

func TestCommandFlow(t *testing.T) {
	h, _ := hub(t)
	var events []Event
	h.Listen(func(e Event) { events = append(events, e) })
	ctx := context.Background()
	d, err := h.Command(ctx, "light-living", map[string]any{"on": true, "brightness": 42.0}, false, "tab1")
	if err != nil || d.State["on"] != true || d.State["brightness"] != 42.0 {
		t.Fatalf("%v %+v", err, d.State)
	}
	if len(events) != 1 || events[0].By != "tab1" || events[0].Device.ID != "light-living" {
		t.Fatalf("events %+v", events)
	}
	// the same again changes nothing and tells nobody
	h.Command(ctx, "light-living", map[string]any{"brightness": 42.0}, false, "tab1")
	if len(events) != 1 {
		t.Fatalf("no change, but %d events", len(events))
	}
	// unlocking waits for a person
	if _, err := h.Command(ctx, "lock-entry", map[string]any{"locked": false}, false, "tab1"); err == nil {
		t.Fatal("unlocked without confirming")
	}
	if d, _ := h.Get("lock-entry"); d.State["locked"] != true {
		t.Fatal("lock changed")
	}
	if d, err := h.Command(ctx, "lock-entry", map[string]any{"locked": false}, true, "tab1"); err != nil || d.State["locked"] != false {
		t.Fatalf("confirmed unlock: %v", err)
	}
}

func TestPersist(t *testing.T) {
	dir := t.TempDir()
	h, _ := NewHub("chengdu-89", dir, nil)
	h.Use(NewSim(home.Sample()))
	h.Command(context.Background(), "ac-master", map[string]any{"on": true, "target": 22.0}, false, "")
	h.save()
	h2, err := NewHub("chengdu-89", dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if d, _ := h2.Get("ac-master"); d.State["target"] != 22.0 || d.State["on"] != true {
		t.Fatalf("reloaded %+v", d.State)
	}
}

func TestSimCurtainAndVacuum(t *testing.T) {
	h, s := hub(t)
	ctx := context.Background()
	h.Command(ctx, "curtain-living", map[string]any{"position": 40.0}, false, "")
	now := time.Now()
	for i := 0; i < 10; i++ {
		s.step(h, 0.5, now)
	}
	if d, _ := h.Get("curtain-living"); d.State["position"] != 40.0 {
		t.Fatalf("curtain at %v after 5 s", d.State["position"])
	}
	h.Command(ctx, "vacuum", map[string]any{"phase": "cleaning"}, false, "")
	for i := 0; i < int(cleanSeconds/0.5)+2; i++ {
		s.step(h, 0.5, now)
	}
	if d, _ := h.Get("vacuum"); d.State["phase"] != "returning" {
		t.Fatalf("after a full run the robot should head home, is %v", d.State["phase"])
	}
	for i := 0; i < int(returnSeconds/0.5)+2; i++ {
		s.step(h, 0.5, now)
	}
	if d, _ := h.Get("vacuum"); d.State["phase"] != "docked" {
		t.Fatalf("robot not docked: %v", d.State["phase"])
	}
}

func TestSimACCools(t *testing.T) {
	h, s := hub(t)
	s.Scenario(h, "hot")
	before, _ := s.roomTemp("master")
	h.Command(context.Background(), "ac-master", map[string]any{"on": true, "mode": "cool", "target": 22.0}, false, "")
	for i := 0; i < 600; i++ {
		s.step(h, 1, time.Now())
	}
	after, _ := s.roomTemp("master")
	if after > before-1 {
		t.Fatalf("10 min of cooling: %.1f → %.1f", before, after)
	}
	if d, _ := h.Get("ac-master"); d.State["running"] != true {
		t.Fatalf("compressor should run: %+v", d.State)
	}
}


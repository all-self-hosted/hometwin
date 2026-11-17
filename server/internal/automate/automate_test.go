package automate

import (
	"context"
	"strings"
	"testing"
	"time"

	"hometwin/internal/devices"
	"hometwin/internal/home"
	"hometwin/internal/insight"
)

func setup(t *testing.T) (*Engine, *devices.Hub) {
	t.Helper()
	h, err := devices.NewHub("chengdu-89", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	h.Use(devices.NewSim(home.Sample()))
	e, err := New(h, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return e, h
}

func state(h *devices.Hub, id, k string) any { d, _ := h.Get(id); return d.State[k] }

func TestAdviseOnlyProposes(t *testing.T) {
	e, h := setup(t)
	p, err := e.Run(context.Background(), "bedtime", "test")
	if err != nil || p.Status != "pending" {
		t.Fatalf("%+v %v", p, err)
	}
	if len(p.Steps) != 4 || !strings.Contains(p.Steps[0], "入户门锁 上锁") {
		t.Fatalf("steps %v", p.Steps)
	}
	h.Command(context.Background(), "light-living", map[string]any{"on": true}, false, "")
	if state(h, "light-living", "on") != true {
		t.Fatal("setup")
	}
	if _, err := e.Decide(context.Background(), p.ID, true, "我"); err != nil {
		t.Fatal(err)
	}
	if state(h, "light-living", "on") != false {
		t.Fatal("approved, but the light is still on")
	}
	if ps := e.Proposals(); ps[0].Status != "done" || ps[0].By != "我 批准" {
		t.Fatalf("%+v", ps[0])
	}
}

func TestGuardedActs(t *testing.T) {
	e, h := setup(t)
	p, _ := e.Run(context.Background(), "leave", "test")
	if p.Status != "done" || state(h, "vacuum", "phase") != "cleaning" || state(h, "lock-entry", "locked") != true {
		t.Fatalf("%+v", p)
	}
}

func TestGuardedAsksWhenRulesRefuse(t *testing.T) {
	e, h := setup(t)
	// an empty robot battery: the rules refuse to start it, so the guarded run asks instead
	h.Report("vacuum", map[string]any{"battery": 10.0}, "")
	p, _ := e.Run(context.Background(), "leave", "test")
	if p.Status != "pending" || !strings.Contains(p.Reason, "电量") {
		t.Fatalf("%+v", p)
	}
	if state(h, "light-living", "on") != false || state(h, "lock-entry", "locked") != true {
		t.Fatal("nothing may run before a person decides")
	}
}

func TestUnlockAlwaysNeedsAPerson(t *testing.T) {
	e, h := setup(t)
	// "guest" is autonomous, but unlocking the front door still waits
	p, _ := e.Run(context.Background(), "guest", "test")
	if p.Status != "pending" || !strings.Contains(p.Reason, "一定要人确认") {
		t.Fatalf("%+v", p)
	}
	if state(h, "lock-entry", "locked") != true {
		t.Fatal("unlocked by an automation")
	}
	e.Decide(context.Background(), p.ID, true, "我")
	if state(h, "lock-entry", "locked") != false {
		t.Fatal("approved but still locked")
	}
}

func TestStateTriggerFiresOnCrossing(t *testing.T) {
	e, h := setup(t)
	h.Listen(func(ev devices.Event) { e.OnDevice(context.Background(), ev) })
	h.Report("sensor-living", map[string]any{"temp": 27.5}, "sim")
	if state(h, "ac-living", "on") == true {
		t.Fatal("27.5 is not over 28")
	}
	h.Report("sensor-living", map[string]any{"temp": 28.4}, "sim")
	if state(h, "ac-living", "on") != true || state(h, "ac-living", "target") != 26.0 {
		t.Fatal("over 28: the guarded automation should cool to 26")
	}
	n := len(e.Proposals())
	h.Report("sensor-living", map[string]any{"temp": 28.6}, "sim")
	if len(e.Proposals()) != n {
		t.Fatal("still over 28 is not a new crossing")
	}
}

func TestHintAndTimeTriggers(t *testing.T) {
	e, _ := setup(t)
	e.OnHint(context.Background(), insight.Hint{Kind: "door-open", Text: "门开着"})
	if ps := e.Proposals(); len(ps) != 1 || ps[0].Automation != "door-open" || ps[0].Status != "pending" {
		t.Fatalf("%+v", ps)
	}
	now := time.Date(2026, 10, 17, 23, 0, 10, 0, time.Local)
	e.now = func() time.Time { return now }
	e.Tick(context.Background())
	e.Tick(context.Background())
	if ps := e.Proposals(); len(ps) != 2 || ps[0].Automation != "bedtime" {
		t.Fatalf("bedtime once: %+v", ps)
	}
}

func TestSetLevel(t *testing.T) {
	e, _ := setup(t)
	if _, err := e.SetLevel("bedtime", "yolo", true); err == nil {
		t.Fatal("bad level accepted")
	}
	a, err := e.SetLevel("bedtime", Guarded, true)
	if err != nil || a.Level != Guarded {
		t.Fatal(err)
	}
}

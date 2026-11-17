package ha

// Checks against a real Home Assistant, run on demand:
//
//	HA_LIVE_URL=http://127.0.0.1:38123 go test ./internal/ha -run Live -v
//
// Without a token (Keychain, HA_TOKEN or HA_TOKEN_FILE) only the handshake is checked. With
// one, it reads the states and registries, maps the demo entities onto the sample flat, and
// with HA_LIVE_COMMAND=1 also turns a demo light on and off through HomeTwin.

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"hometwin/internal/devices"
	"hometwin/internal/home"
)

func liveURL(t *testing.T) string {
	u := os.Getenv("HA_LIVE_URL")
	if u == "" {
		t.Skip("HA_LIVE_URL not set")
	}
	return u
}

func TestLiveHandshake(t *testing.T) {
	u := liveURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// a made-up token: the real server must go through auth_required → auth → auth_invalid
	_, err := Dial(ctx, u, "not-a-real-token")
	if !errors.Is(err, ErrAuth) {
		t.Fatalf("want auth_invalid from the real Home Assistant, got %v", err)
	}
	if strings.Contains(err.Error(), "not-a-real-token") {
		t.Fatal("the token leaked into the error")
	}
}

func TestLiveStatesAndMapping(t *testing.T) {
	u := liveURL(t)
	tok, err := DefaultToken()()
	if err != nil {
		t.Skip("no Home Assistant token yet")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c, err := Dial(ctx, u, tok)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	cfg, err := c.GetConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	states, err := c.GetStates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	areas, err := c.AreaRegistry(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ents, err := c.EntityRegistry(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Home Assistant %s at %s: %d states, %d areas, %d registered entities", cfg.Version, cfg.LocationName, len(states), len(areas), len(ents))
	if len(states) == 0 {
		t.Fatal("no states")
	}

	// the adapter end to end, on a hub of the sample flat
	hub, _ := devices.NewHub("chengdu-89", "", nil)
	hub.Use(devices.NewSim(home.Sample()))
	store, _ := NewMappingStore(t.TempDir())
	a := New(Config{URL: u, Token: DefaultToken(), Dir: t.TempDir(), Home: home.Sample()}, hub, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	hub.Use(a)
	go a.Run(ctx, hub)
	for ctx.Err() == nil && !a.Status().Connected {
		time.Sleep(200 * time.Millisecond)
	}
	rows := a.Rows()
	supported := 0
	for _, r := range rows {
		if r.Supported {
			supported++
		}
	}
	n, err := a.AutoMap()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d entities, %d HomeTwin can use, %d mapped automatically", len(rows), supported, len(n))
	var light devices.Device
	for _, d := range hub.List() {
		if d.Source == "ha" {
			t.Logf("  %s (%s) in %s ← %s: %v", d.Name, d.Kind, d.Room, d.Entity, d.State)
			if d.Kind == devices.Light && light.ID == "" {
				light = d
			}
		}
	}
	if os.Getenv("HA_LIVE_COMMAND") != "1" || light.ID == "" {
		return
	}
	for _, want := range []bool{true, false} {
		if _, err := hub.Command(ctx, light.ID, map[string]any{"on": want, "brightness": 40.0}, false, "test"); err != nil && want {
			t.Fatal(err)
		}
		if !want {
			hub.Command(ctx, light.ID, map[string]any{"on": false}, false, "test")
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			d, _ := hub.Get(light.ID)
			if d.State["on"] == want {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if d, _ := hub.Get(light.ID); d.State["on"] != want {
			t.Fatalf("%s did not come back %v from Home Assistant: %v", light.Entity, want, d.State)
		}
	}
	t.Logf("%s turned on and off through HomeTwin", light.Entity)
}

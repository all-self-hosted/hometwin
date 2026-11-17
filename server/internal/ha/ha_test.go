package ha

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"hometwin/internal/devices"
	"hometwin/internal/home"
)

const secret = "tok-0123456789-very-secret"

// --- a fake Home Assistant that speaks the real WebSocket protocol ---

type fakeConn struct {
	c   *websocket.Conn
	mu  sync.Mutex
	sub int
}

func (f *fakeConn) send(v any) {
	b, _ := json.Marshal(v)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.c.Write(context.Background(), websocket.MessageText, b)
}

type fakeHA struct {
	srv    *httptest.Server
	mu     sync.Mutex
	states map[string]*State
	calls  []string
	conns  map[*fakeConn]bool
	dials  int
	wsDown bool
	pings  int
}

func newFake(t *testing.T) *fakeHA {
	f := &fakeHA{conns: map[*fakeConn]bool{}, states: map[string]*State{}}
	add := func(id, state string, attrs map[string]any) {
		f.states[id] = &State{EntityID: id, State: state, Attributes: attrs}
	}
	add("light.living_room", "on", map[string]any{"friendly_name": "Living Room RGBWW Lights", "brightness": 180.0, "color_temp_kelvin": 3000.0, "min_color_temp_kelvin": 2000.0, "max_color_temp_kelvin": 6500.0})
	add("climate.hvac", "cool", map[string]any{"friendly_name": "Hvac", "temperature": 21.0, "current_temperature": 22.0, "hvac_action": "cooling", "fan_mode": "on_high"})
	add("lock.front_door", "locked", map[string]any{"friendly_name": "Front Door"})
	add("cover.living_room_window", "open", map[string]any{"friendly_name": "Living Room Window", "current_position": 70.0, "supported_features": 15.0})
	add("vacuum.demo_vacuum_0_ground_floor", "docked", map[string]any{"friendly_name": "0 Ground floor", "battery_level": 100.0})
	add("sensor.kitchen_temperature", "23.4", map[string]any{"friendly_name": "Kitchen Temperature", "device_class": "temperature", "unit_of_measurement": "°C"})
	add("sensor.kitchen_humidity", "55", map[string]any{"friendly_name": "Kitchen Humidity", "device_class": "humidity", "unit_of_measurement": "%"})
	add("sensor.outside_temperature", "60.1", map[string]any{"friendly_name": "Outside Temperature", "device_class": "temperature", "unit_of_measurement": "°F"})
	add("sensor.outside_humidity", "54", map[string]any{"friendly_name": "Outside Humidity", "device_class": "humidity", "unit_of_measurement": "%"})
	add("switch.decorative_lights", "on", map[string]any{"friendly_name": "Decorative Lights"})
	mux := http.NewServeMux()
	mux.HandleFunc("/api/websocket", f.ws)
	mux.HandleFunc("POST /api/services/{domain}/{service}", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secret {
			http.Error(w, "401: Unauthorized", 401)
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		id, _ := body["entity_id"].(string)
		delete(body, "entity_id")
		f.apply(r.PathValue("domain"), r.PathValue("service"), id, body, "rest")
		w.Write([]byte("[]"))
	})
	mux.HandleFunc("GET /api/states/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secret {
			http.Error(w, "401: Unauthorized", 401)
			return
		}
		f.mu.Lock()
		s := f.states[r.PathValue("id")]
		f.mu.Unlock()
		json.NewEncoder(w).Encode(s)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeHA) ws(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	down := f.wsDown
	f.dials++
	f.mu.Unlock()
	if down {
		http.Error(w, "down", 503)
		return
	}
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	fc := &fakeConn{c: c}
	defer c.CloseNow()
	fc.send(map[string]any{"type": "auth_required", "ha_version": "2026.10.2"})
	_, b, err := c.Read(r.Context())
	if err != nil {
		return
	}
	var auth map[string]any
	json.Unmarshal(b, &auth)
	if auth["type"] != "auth" || auth["access_token"] != secret {
		fc.send(map[string]any{"type": "auth_invalid", "message": "Invalid access token or password"})
		return
	}
	fc.send(map[string]any{"type": "auth_ok", "ha_version": "2026.10.2"})
	f.mu.Lock()
	f.conns[fc] = true
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		delete(f.conns, fc)
		f.mu.Unlock()
	}()
	for {
		_, b, err := c.Read(r.Context())
		if err != nil {
			return
		}
		var m map[string]any
		json.Unmarshal(b, &m)
		id := int(m["id"].(float64))
		result := func(v any) { fc.send(map[string]any{"id": id, "type": "result", "success": true, "result": v}) }
		switch m["type"] {
		case "ping":
			f.mu.Lock()
			f.pings++
			f.mu.Unlock()
			fc.send(map[string]any{"id": id, "type": "pong"})
		case "get_config":
			result(map[string]any{"version": "2026.10.2", "location_name": "Test Home"})
		case "config/area_registry/list":
			result([]Area{{"living_room", "Living Room"}, {"kitchen", "Kitchen"}, {"hallway", "Hallway"}})
		case "config/device_registry/list":
			result([]DeviceEntry{
				{ID: "d-light", Name: "Living Room Lights", AreaID: "living_room"},
				{ID: "d-hvac", Name: "Hvac", AreaID: "living_room"},
				{ID: "d-lock", Name: "Front Door Lock", AreaID: "hallway"},
				{ID: "d-cover", Name: "Living Room Window", AreaID: "living_room"},
				{ID: "d-kitchen", Name: "Kitchen Sensor", AreaID: "kitchen"},
			})
		case "config/entity_registry/list":
			result([]EntityEntry{
				{EntityID: "light.living_room", DeviceID: "d-light"},
				{EntityID: "climate.hvac", DeviceID: "d-hvac"},
				{EntityID: "lock.front_door", DeviceID: "d-lock"},
				{EntityID: "cover.living_room_window", DeviceID: "d-cover"},
				{EntityID: "vacuum.demo_vacuum_0_ground_floor", AreaID: "living_room"},
				{EntityID: "sensor.kitchen_temperature", DeviceID: "d-kitchen"},
				{EntityID: "sensor.kitchen_humidity", DeviceID: "d-kitchen"},
			})
		case "get_states":
			f.mu.Lock()
			var out []State
			for _, s := range f.states {
				out = append(out, *s)
			}
			f.mu.Unlock()
			result(out)
		case "subscribe_events":
			fc.mu.Lock()
			fc.sub = id
			fc.mu.Unlock()
			result(nil)
		case "call_service":
			data, _ := m["service_data"].(map[string]any)
			target, _ := m["target"].(map[string]any)
			ent, _ := target["entity_id"].(string)
			result(map[string]any{"context": map[string]any{"id": "x"}})
			f.apply(m["domain"].(string), m["service"].(string), ent, data, "ws")
		default:
			fc.send(map[string]any{"id": id, "type": "result", "success": false, "error": map[string]any{"code": "unknown_command", "message": "Unknown command."}})
		}
	}
}

// apply changes a state the way HA would and pushes state_changed.
func (f *fakeHA) apply(domain, service, ent string, data map[string]any, via string) {
	f.mu.Lock()
	js, _ := json.Marshal(data)
	f.calls = append(f.calls, fmt.Sprintf("%s %s.%s %s %s", via, domain, service, ent, js))
	s, ok := f.states[ent]
	if !ok {
		f.mu.Unlock()
		return
	}
	switch domain + "." + service {
	case "light.turn_on":
		s.State = "on"
		if v, ok := data["brightness_pct"].(float64); ok {
			s.Attributes["brightness"] = math.Round(v * 2.55)
		}
		if v, ok := data["color_temp_kelvin"].(float64); ok {
			s.Attributes["color_temp_kelvin"] = v
		}
	case "light.turn_off", "climate.turn_off":
		s.State = "off"
	case "climate.set_temperature":
		s.Attributes["temperature"] = data["temperature"]
	case "climate.set_hvac_mode":
		s.State = data["hvac_mode"].(string)
	case "lock.lock":
		s.State = "locked"
	case "lock.unlock":
		s.State = "unlocked"
	case "cover.set_cover_position":
		s.Attributes["current_position"] = data["position"]
	case "vacuum.start":
		s.State = "cleaning"
	case "vacuum.return_to_base":
		s.State = "returning"
	}
	c := *s
	f.mu.Unlock()
	f.push(c)
}

func (f *fakeHA) push(s State) {
	f.mu.Lock()
	conns := []*fakeConn{}
	for c := range f.conns {
		conns = append(conns, c)
	}
	f.mu.Unlock()
	for _, c := range conns {
		c.mu.Lock()
		sub := c.sub
		c.mu.Unlock()
		if sub > 0 {
			c.send(map[string]any{"id": sub, "type": "event", "event": map[string]any{"event_type": "state_changed", "data": map[string]any{"entity_id": s.EntityID, "new_state": s, "old_state": s}}})
		}
	}
}

// set changes a state from HA's side (someone used the HA app).
func (f *fakeHA) set(id, state string, attrs map[string]any) {
	f.mu.Lock()
	s := f.states[id]
	s.State = state
	for k, v := range attrs {
		s.Attributes[k] = v
	}
	c := *s
	f.mu.Unlock()
	f.push(c)
}

func (f *fakeHA) drop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for c := range f.conns {
		c.c.CloseNow()
	}
}

func (f *fakeHA) called(substr string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if strings.Contains(c, substr) {
			return true
		}
	}
	return false
}

// --- helpers ---

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func setup(t *testing.T, f *fakeHA) (*Adapter, *devices.Hub) {
	t.Helper()
	dir := t.TempDir()
	hub, err := devices.NewHub("chengdu-89", dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	hub.Use(devices.NewSim(home.Sample()))
	store, err := NewMappingStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	a := New(Config{URL: f.srv.URL, Token: StaticToken(secret), Dir: dir, Home: home.Sample(), Backoff: 30 * time.Millisecond, Ping: 50 * time.Millisecond}, hub, store, nil)
	hub.Use(a)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go a.Run(ctx, hub)
	return a, hub
}

func connected(t *testing.T, a *Adapter) {
	t.Helper()
	eventually(t, "connected", func() bool { return a.Status().Connected })
}

func noSecret(t *testing.T, s string) {
	t.Helper()
	if strings.Contains(s, secret) {
		t.Fatalf("the token leaked into: %s", s)
	}
}

// --- tests ---

func TestAuthInvalid(t *testing.T) {
	f := newFake(t)
	_, err := Dial(context.Background(), f.srv.URL, "wrong-"+secret)
	if !errors.Is(err, ErrAuth) {
		t.Fatalf("want ErrAuth, got %v", err)
	}
	noSecret(t, err.Error())
	// the adapter says so in its status, without the token
	dir := t.TempDir()
	hub, _ := devices.NewHub("chengdu-89", dir, nil)
	store, _ := NewMappingStore(dir)
	a := New(Config{URL: f.srv.URL, Token: StaticToken("bad-" + secret), Dir: dir, Home: home.Sample(), Backoff: 20 * time.Millisecond}, hub, store, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Run(ctx, hub)
	eventually(t, "auth error in status", func() bool { return strings.Contains(a.Status().Error, "不认这个访问令牌") })
	b, _ := json.Marshal(a.Status())
	noSecret(t, string(b))
	// REST says the same on a bad token
	err = REST{Base: f.srv.URL, Token: "bad-" + secret}.CallService(context.Background(), "light", "turn_on", nil, map[string]any{"entity_id": "light.living_room"})
	if !errors.Is(err, ErrAuth) {
		t.Fatalf("rest: %v", err)
	}
	noSecret(t, err.Error())
}

func TestClient(t *testing.T) {
	f := newFake(t)
	ctx := context.Background()
	c, err := Dial(ctx, f.srv.URL, secret)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.Version != "2026.10.2" {
		t.Fatalf("version %q", c.Version)
	}
	st, err := c.GetStates(ctx)
	if err != nil || len(st) != 10 {
		t.Fatalf("%d states, %v", len(st), err)
	}
	areas, _ := c.AreaRegistry(ctx)
	devs, _ := c.DeviceRegistry(ctx)
	ents, _ := c.EntityRegistry(ctx)
	if len(areas) != 3 || len(devs) != 5 || len(ents) != 7 {
		t.Fatalf("registries %d %d %d", len(areas), len(devs), len(ents))
	}
	if err := c.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.command(ctx, "no/such", nil); err == nil || !strings.Contains(err.Error(), "unknown_command") {
		t.Fatalf("unknown command: %v", err)
	}
	ch, err := c.SubscribeStateChanged(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.CallService(ctx, "light", "turn_off", nil, map[string]any{"entity_id": "light.living_room"}); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-ch:
		if ev.EntityID != "light.living_room" || ev.NewState.State != "off" {
			t.Fatalf("%+v", ev)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no state_changed")
	}
	// many commands at once, each gets its own answer
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); c.Ping(ctx) }()
	}
	wg.Wait()
}

func TestSuggest(t *testing.T) {
	f := newFake(t)
	a, hub := setup(t, f)
	connected(t, a)
	h := home.Sample()
	sugg := Suggest(h, a.Entities(), hub.List(), a.store)
	by := map[string]Suggestion{}
	for _, s := range sugg {
		by[s.Entities[0]] = s
	}
	inside := func(s Suggestion) bool {
		r := roomOf(h, s.Room)
		return r != nil && home.Inside(r.Poly, home.P{s.Placed.X, s.Placed.Z})
	}
	cases := []struct{ entity, kind, room string }{
		{"light.living_room", devices.Light, "living"},
		{"climate.hvac", devices.AC, "living"},
		{"lock.front_door", devices.Lock, "entry"},
		{"cover.living_room_window", devices.Curtain, "living"},
		{"vacuum.demo_vacuum_0_ground_floor", devices.Vacuum, "living"},
	}
	for _, c := range cases {
		s, ok := by[c.entity]
		if !ok || s.Kind != c.kind || s.Room != c.room {
			t.Errorf("%s: %+v", c.entity, s)
			continue
		}
		if !inside(s) {
			t.Errorf("%s placed at %.2f,%.2f, outside %s", c.entity, s.Placed.X, s.Placed.Z, c.room)
		}
		if s.TakeOver == "" {
			t.Errorf("%s: a simulated %s in %s should be taken over", c.entity, c.kind, c.room)
		}
	}
	if by["lock.front_door"].Placed.Door != "entry" || by["lock.front_door"].TakeOver != "lock-entry" {
		t.Errorf("lock: %+v", by["lock.front_door"])
	}
	if by["light.living_room"].Placed.Y != 2.72 || by["cover.living_room_window"].Placed.Width < 1 {
		t.Errorf("light/cover placement: %+v %+v", by["light.living_room"].Placed, by["cover.living_room_window"].Placed)
	}
	// two sensors of one HA device make one twin sensor; the outside pair goes outdoors
	k := by["sensor.kitchen_temperature"]
	if k.Kind != devices.Sensor || k.Room != "kitchen" || len(k.Entities) != 2 || !inside(k) || k.Name != "Kitchen Sensor" {
		t.Errorf("kitchen sensor: %+v", k)
	}
	if _, ok := by["sensor.kitchen_humidity"]; ok {
		t.Error("kitchen humidity should be merged into the temperature sensor")
	}
	o := by["sensor.outside_temperature"]
	if o.Room != "outdoors" || len(o.Entities) != 2 {
		t.Errorf("outside sensor: %+v", o)
	}
	if _, ok := by["switch.decorative_lights"]; ok {
		t.Error("switches are not supported")
	}
	for _, e := range a.Entities() {
		if e.ID == "switch.decorative_lights" && e.Supported {
			t.Error("switch marked supported")
		}
	}
	// HA areas and words both ways
	for text, want := range map[string]string{"Living Room": "living", "Master Bedroom": "master", "Study": "second", "卫生间": "bath", "Hallway": "entry", "客厅": "living", "Garden": "outdoors", "Bedroom 2": "master", "Garage door": "outdoors", "nothing": ""} {
		if got := roomFor(h, text); got != want {
			t.Errorf("roomFor(%q) = %q, want %q", text, got, want)
		}
	}
}

func TestMapLiveAndCommand(t *testing.T) {
	f := newFake(t)
	a, hub := setup(t, f)
	connected(t, a)
	ms, err := a.AutoMap()
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 7 {
		t.Fatalf("auto-mapped %d", len(ms))
	}
	light, _ := a.store.ForEntity("light.living_room")
	d, ok := hub.Get(light.Device.ID)
	if !ok || d.Source != "ha" || d.Entity != "light.living_room" {
		t.Fatalf("%+v", d)
	}
	if light.TakeOver == "" || d.Topic == "" {
		t.Fatalf("taking over should keep the simulated light's id and topic: %+v %+v", light, d)
	}
	// HA's state is in the twin
	if d.State["brightness"] != 71.0 || d.State["on"] != true || d.State["ct"] != 3000.0 {
		t.Fatalf("state %+v", d.State)
	}
	// someone changes the light in HA: the twin follows
	f.set("light.living_room", "on", map[string]any{"brightness": 255.0})
	eventually(t, "live brightness", func() bool { d, _ := hub.Get(light.Device.ID); return d.State["brightness"] == 100.0 })
	// a command from the twin reaches HA as a service call and comes back as state
	if _, err := hub.Command(context.Background(), light.Device.ID, map[string]any{"brightness": 40.0}, false, "test"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "light.turn_on call", func() bool { return f.called(`ws light.turn_on light.living_room {"brightness_pct":40}`) })
	eventually(t, "brightness back from HA", func() bool { d, _ := hub.Get(light.Device.ID); return d.State["brightness"] == 40.0 })
	// the twin's rules still hold
	ac, _ := a.store.ForEntity("climate.hvac")
	if _, err := hub.Command(context.Background(), ac.Device.ID, map[string]any{"target": 35.0}, false, "test"); err == nil {
		t.Fatal("35 ℃ must be refused before reaching HA")
	}
	if f.called("climate.set_temperature") {
		t.Fatal("a refused change reached HA")
	}
	// several fields at once become several calls
	if _, err := hub.Command(context.Background(), ac.Device.ID, map[string]any{"on": true, "mode": "heat", "target": 23.0}, false, "test"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "climate calls", func() bool {
		return f.called(`climate.set_hvac_mode climate.hvac {"hvac_mode":"heat"}`) && f.called(`climate.set_temperature climate.hvac {"temperature":23}`)
	})
	// the kitchen pair reports temperature and humidity into one sensor; °F is converted
	ks, _ := a.store.ForEntity("sensor.kitchen_humidity")
	kd, _ := hub.Get(ks.Device.ID)
	if kd.State["temp"] != 23.4 || kd.State["humidity"] != 55.0 {
		t.Fatalf("kitchen sensor %+v", kd.State)
	}
	os, _ := a.store.ForEntity("sensor.outside_temperature")
	od, _ := hub.Get(os.Device.ID)
	if od.State["temp"] != 15.6 {
		t.Fatalf("outside %+v", od.State)
	}
	// a curtain position
	cv, _ := a.store.ForEntity("cover.living_room_window")
	hub.Command(context.Background(), cv.Device.ID, map[string]any{"position": 30.0}, false, "test")
	eventually(t, "cover moved", func() bool { d, _ := hub.Get(cv.Device.ID); return d.State["position"] == 30.0 })
	st := a.Status()
	if st.Mapped != 7 || st.Version != "2026.10.2" || st.LocationName != "Test Home" || st.LastEvent.IsZero() || !st.TokenFound {
		t.Fatalf("status %+v", st)
	}
}

func TestUnlockNeedsAPerson(t *testing.T) {
	f := newFake(t)
	a, hub := setup(t, f)
	connected(t, a)
	m, err := a.Map(MapRequest{Entities: []string{"lock.front_door"}, Room: "entry"})
	if err != nil {
		t.Fatal(err)
	}
	if m.Device.ID != "lock-entry" {
		t.Fatalf("the front door lock should take over the simulated one: %+v", m)
	}
	_, err = hub.Command(context.Background(), m.Device.ID, map[string]any{"locked": false}, false, "test")
	var nc *devices.NeedConfirm
	if !errors.As(err, &nc) {
		t.Fatalf("want a confirmation, got %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if f.called("lock.unlock") {
		t.Fatal("unlocked without a person")
	}
	if _, err := hub.Command(context.Background(), m.Device.ID, map[string]any{"locked": false}, true, "test"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "unlock call", func() bool { return f.called("lock.unlock lock.front_door") })
	eventually(t, "unlocked state", func() bool { d, _ := hub.Get(m.Device.ID); return d.State["locked"] == false })
}

func TestReconnect(t *testing.T) {
	f := newFake(t)
	a, hub := setup(t, f)
	connected(t, a)
	m, _ := a.Map(MapRequest{Entities: []string{"light.living_room"}, Room: "living"})
	f.drop()
	eventually(t, "a second connection", func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.dials >= 2 && len(f.conns) == 1
	})
	connected(t, a)
	// resubscribed: events after the drop still arrive
	eventually(t, "event after reconnect", func() bool {
		f.set("light.living_room", "off", nil)
		d, _ := hub.Get(m.Device.ID)
		return d.State["on"] == false
	})
	// the keepalive pings
	eventually(t, "pings", func() bool { f.mu.Lock(); defer f.mu.Unlock(); return f.pings > 0 })
	// Reconnect drops and comes back
	before := f.dials
	a.Reconnect()
	eventually(t, "reconnect on request", func() bool { f.mu.Lock(); defer f.mu.Unlock(); return f.dials > before })
	connected(t, a)
}

func TestRESTFallback(t *testing.T) {
	f := newFake(t)
	f.wsDown = true
	a, hub := setup(t, f)
	eventually(t, "a failed dial", func() bool { return a.Status().Error != "" })
	m, err := a.Map(MapRequest{Entities: []string{"light.living_room"}, Kind: devices.Light, Room: "living"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hub.Command(context.Background(), m.Device.ID, map[string]any{"on": false}, false, "test"); err != nil {
		t.Fatal(err)
	}
	if !f.called("rest light.turn_off light.living_room") {
		t.Fatalf("calls %v", f.calls)
	}
	s, err := REST{Base: f.srv.URL, Token: secret}.State(context.Background(), "light.living_room")
	if err != nil || s.State != "off" {
		t.Fatalf("%+v %v", s, err)
	}
}

func TestUnmapRestoresSimulated(t *testing.T) {
	f := newFake(t)
	a, hub := setup(t, f)
	connected(t, a)
	before, _ := hub.Get("curtain-living")
	m, err := a.Map(MapRequest{Entities: []string{"cover.living_room_window"}, Room: "living"})
	if err != nil || m.Device.ID != "curtain-living" {
		t.Fatalf("%+v %v", m, err)
	}
	if d, _ := hub.Get("curtain-living"); d.Source != "ha" || d.X != before.X || d.State["position"] != 70.0 {
		t.Fatalf("%+v", d)
	}
	if err := a.Unmap("cover.living_room_window"); err != nil {
		t.Fatal(err)
	}
	if d, _ := hub.Get("curtain-living"); d.Source != "sim" || d.Entity != "" {
		t.Fatalf("not restored: %+v", d)
	}
	// without taking over, a new device appears and goes again
	no := false
	m, _ = a.Map(MapRequest{Entities: []string{"vacuum.demo_vacuum_0_ground_floor"}, Room: "living", TakeOver: &no})
	if !strings.HasPrefix(m.Device.ID, "ha-") {
		t.Fatalf("%+v", m)
	}
	a.Unmap("vacuum.demo_vacuum_0_ground_floor")
	if _, ok := hub.Get(m.Device.ID); ok {
		t.Fatal("device made for HA should go")
	}
	// mappings persist
	a.Map(MapRequest{Entities: []string{"light.living_room"}, Room: "living"})
	s2, _ := NewMappingStore(a.cfg.Dir)
	if _, ok := s2.ForEntity("light.living_room"); !ok {
		t.Fatal("mapping not saved")
	}
}

func TestHandler(t *testing.T) {
	f := newFake(t)
	a, _ := setup(t, f)
	connected(t, a)
	srv := httptest.NewServer(a.Handler())
	defer srv.Close()
	do := func(method, path, body string) (int, string) {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var b strings.Builder
		buf := make([]byte, 1<<16)
		for {
			n, err := res.Body.Read(buf)
			b.Write(buf[:n])
			if err != nil {
				break
			}
		}
		noSecret(t, b.String())
		return res.StatusCode, b.String()
	}
	if c, b := do("GET", "/api/ha/status", ""); c != 200 || !strings.Contains(b, `"connected":true`) || !strings.Contains(b, `"tokenFound":true`) {
		t.Fatalf("%d %s", c, b)
	}
	if c, b := do("GET", "/api/ha/entities", ""); c != 200 || !strings.Contains(b, `"suggestion"`) || !strings.Contains(b, "switch.decorative_lights") {
		t.Fatalf("%d %s", c, b)
	}
	if c, _ := do("PUT", "/api/ha/config", `{"url":"`+f.srv.URL+`","token":"x"}`); c != 400 {
		t.Fatalf("a token must not be accepted over HTTP: %d", c)
	}
	if c, _ := do("PUT", "/api/ha/mapping", `{"entities":["light.living_room"],"room":"nowhere"}`); c != 400 {
		t.Fatal("unknown room accepted")
	}
	// a room picked by the person, no position: placed in that room
	c, b := do("PUT", "/api/ha/mapping", `{"entities":["light.living_room"],"kind":"light","name":"大灯","room":"master","takeOver":false}`)
	if c != 200 {
		t.Fatalf("%d %s", c, b)
	}
	var m Mapping
	json.Unmarshal([]byte(b), &m)
	r := roomOf(home.Sample(), "master")
	if !home.Inside(r.Poly, home.P{m.Device.X, m.Device.Z}) || m.Device.Name != "大灯" || m.TakeOver != "" {
		t.Fatalf("%+v", m)
	}
	if c, _ := do("DELETE", "/api/ha/mapping/light.living_room", ""); c != 200 {
		t.Fatal(c)
	}
	if c, b := do("POST", "/api/ha/mapping/auto", ""); c != 200 || !strings.Contains(b, "lock.front_door") {
		t.Fatalf("%d %s", c, b)
	}
	// a new URL is kept in ha.json
	if c, _ := do("PUT", "/api/ha/config", `{"url":"`+f.srv.URL+`/"}`); c != 200 {
		t.Fatal(c)
	}
	if b, _ := os.ReadFile(filepath.Join(a.cfg.Dir, "ha.json")); !strings.Contains(string(b), f.srv.URL) {
		t.Fatalf("ha.json %s", b)
	}
}

func TestTranslate(t *testing.T) {
	cv := devices.Device{Kind: devices.Curtain, Name: "窗帘", Entity: "cover.garage"}
	cs, _ := calls(cv, map[string]any{"position": 80.0}, map[string]any{"supported_features": 3.0})
	if len(cs) != 1 || cs[0].Service != "open_cover" {
		t.Fatalf("%+v", cs)
	}
	cs, _ = calls(cv, map[string]any{"position": 10.0}, map[string]any{"supported_features": 3.0})
	if cs[0].Service != "close_cover" {
		t.Fatalf("%+v", cs)
	}
	lt := devices.Device{Kind: devices.Light, Name: "灯", Entity: "light.x"}
	cs, _ = calls(lt, map[string]any{"on": true, "ct": 6500.0}, map[string]any{"max_color_temp_kelvin": 6000.0})
	if cs[0].Data["color_temp_kelvin"] != 6000.0 {
		t.Fatalf("ct clamp %+v", cs)
	}
	if _, err := calls(devices.Device{Kind: devices.Light, Name: "灯"}, map[string]any{"on": true}, nil); err == nil {
		t.Fatal("no entity")
	}
	st := fromHA(devices.Lock, State{State: "jammed"})
	if st["locked"] != false || st["jammed"] != true {
		t.Fatalf("%+v", st)
	}
	if st := fromHA(devices.Lock, State{State: "open"}); st["open"] != true {
		t.Fatalf("%+v", st)
	}
	if st := fromHA(devices.Curtain, State{State: "closed", Attributes: map[string]any{}}); st["position"] != 0.0 {
		t.Fatalf("%+v", st)
	}
	if st := fromHA(devices.Vacuum, State{State: "error", Attributes: map[string]any{"battery_level": 40.0}}); st["phase"] != "paused" || st["error"] != true || st["battery"] != 40.0 {
		t.Fatalf("%+v", st)
	}
	if st := fromHA(devices.Light, State{State: "unavailable"}); st["available"] != false {
		t.Fatalf("%+v", st)
	}
	if KindOf("sensor.power", map[string]any{"device_class": "power"}) != "" || KindOf("sensor.t", map[string]any{"device_class": "temperature"}) != devices.Sensor {
		t.Fatal("sensor kinds")
	}
}

func TestTokenSources(t *testing.T) {
	t.Setenv("HA_TOKEN", "")
	t.Setenv("HA_TOKEN_FILE", "")
	if _, err := EnvToken()(); !errors.Is(err, ErrNoToken) {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "ha.env")
	os.WriteFile(p, []byte("# home\nexport HA_TOKEN=\"abc\"\n"), 0o600)
	t.Setenv("HA_TOKEN_FILE", p)
	if tok, err := EnvToken()(); err != nil || tok != "abc" {
		t.Fatalf("%q %v", tok, err)
	}
	t.Setenv("HA_TOKEN", "fromenv")
	if tok, _ := EnvToken()(); tok != "fromenv" {
		t.Fatal(tok)
	}
	if !strings.Contains(ErrNoToken.Error(), "security add-generic-password -a hometwin -s hometwin-ha -w") {
		t.Fatal(ErrNoToken)
	}
	if scrub("x tok y", "tok") != "x *** y" {
		t.Fatal("scrub")
	}
}

func TestLoopbackIsDirect(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://192.0.2.1:9")
	t.Setenv("HTTP_PROXY", "http://192.0.2.1:9")
	tr := httpClient().Transport.(*http.Transport)
	for _, u := range []string{"http://127.0.0.1:38123/api/", "http://localhost:38123/"} {
		req, _ := http.NewRequest("GET", u, nil)
		if p, _ := tr.Proxy(req); p != nil {
			t.Fatalf("%s goes through %v", u, p)
		}
	}
	if w, err := wsURL("https://ha.example.com/"); err != nil || w != "wss://ha.example.com/api/websocket" {
		t.Fatalf("%s %v", w, err)
	}
	if _, err := wsURL("ftp://x"); err == nil {
		t.Fatal("ftp accepted")
	}
}

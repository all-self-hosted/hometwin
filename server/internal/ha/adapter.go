package ha

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"hometwin/internal/devices"
	"hometwin/internal/home"
)

// DefaultURL is where the test Home Assistant on this machine listens.
const DefaultURL = "http://127.0.0.1:38123"

// Config says where Home Assistant is and how to get its token.
type Config struct {
	// URL of Home Assistant; empty reads HA_URL, then <Dir>/ha.json, then DefaultURL.
	URL   string
	Token TokenSource
	// Dir keeps ha.json (the URL set in the UI) and ha-mapping.json.
	Dir  string
	Home home.Home
	// Backoff is the first wait before reconnecting (1 s when zero); it doubles up to 30 s.
	Backoff time.Duration
	// Ping is how often the connection is checked (30 s when zero).
	Ping time.Duration
}

// Entity is one HA entity as the twin knows it.
type Entity struct {
	ID          string         `json:"entity_id"`
	Domain      string         `json:"domain"`
	Name        string         `json:"name"`
	State       string         `json:"state"`
	Attributes  map[string]any `json:"attributes,omitempty"`
	DeviceClass string         `json:"deviceClass,omitempty"`
	Area        string         `json:"area,omitempty"`
	DeviceID    string         `json:"deviceId,omitempty"`
	Device      string         `json:"device,omitempty"`
	Kind        string         `json:"kind,omitempty"`
	Supported   bool           `json:"supported"`
}

// Status is how the connection is doing.
type Status struct {
	URL          string    `json:"url"`
	Connected    bool      `json:"connected"`
	Connecting   bool      `json:"connecting"`
	Error        string    `json:"error,omitempty"`
	Version      string    `json:"version,omitempty"`
	LocationName string    `json:"locationName,omitempty"`
	Entities     int       `json:"entities"`
	Supported    int       `json:"supported"`
	Mapped       int       `json:"mapped"`
	LastEvent    time.Time `json:"lastEvent"`
	TokenFound   bool      `json:"tokenFound"`
	NeedToken    string    `json:"needToken,omitempty"`
}

// Adapter drives the twin devices whose Source is "ha".
type Adapter struct {
	tokMu sync.Mutex
	tokAt time.Time
	tokOK bool

	cfg   Config
	hub   *devices.Hub
	store *MappingStore
	log   *slog.Logger

	mu       sync.RWMutex
	url      string
	client   *Client
	entities map[string]*Entity
	status   Status
	kick     chan struct{}
	cancel   context.CancelFunc
}

// New makes the adapter; it connects when Run starts.
func New(cfg Config, hub *devices.Hub, store *MappingStore, log *slog.Logger) *Adapter {
	if cfg.Token == nil {
		cfg.Token = DefaultToken()
	}
	if cfg.Backoff == 0 {
		cfg.Backoff = time.Second
	}
	if cfg.Ping == 0 {
		cfg.Ping = 30 * time.Second
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	a := &Adapter{cfg: cfg, hub: hub, store: store, log: log, entities: map[string]*Entity{}, kick: make(chan struct{}, 1)}
	a.url = a.configuredURL()
	a.status.URL = a.url
	return a
}

func (a *Adapter) Name() string { return "ha" }

type saved struct {
	URL string `json:"url"`
}

func (a *Adapter) configuredURL() string {
	if a.cfg.URL != "" {
		return a.cfg.URL
	}
	if u := os.Getenv("HA_URL"); u != "" {
		return u
	}
	if a.cfg.Dir != "" {
		if b, err := os.ReadFile(filepath.Join(a.cfg.Dir, "ha.json")); err == nil {
			var s saved
			if json.Unmarshal(b, &s) == nil && s.URL != "" {
				return s.URL
			}
		}
	}
	return DefaultURL
}

// SetURL changes where Home Assistant is (kept in ha.json) and reconnects.
func (a *Adapter) SetURL(u string) error {
	u = strings.TrimRight(strings.TrimSpace(u), "/")
	if _, err := wsURL(u); err != nil {
		return err
	}
	if a.cfg.Dir != "" {
		b, _ := json.Marshal(saved{URL: u})
		if err := os.WriteFile(filepath.Join(a.cfg.Dir, "ha.json"), b, 0o644); err != nil {
			return err
		}
	}
	a.mu.Lock()
	a.url = u
	a.status.URL = u
	a.mu.Unlock()
	a.Reconnect()
	return nil
}

// Reconnect drops the connection (if any) and connects again at once.
func (a *Adapter) Reconnect() {
	a.mu.Lock()
	if a.cancel != nil {
		a.cancel()
	}
	a.mu.Unlock()
	select {
	case a.kick <- struct{}{}:
	default:
	}
}

func (a *Adapter) Status() Status {
	a.mu.RLock()
	s := a.status
	s.Entities = len(a.entities)
	for _, e := range a.entities {
		if e.Supported {
			s.Supported++
		}
	}
	a.mu.RUnlock()
	s.Mapped = len(a.store.All())
	if s.TokenFound = a.tokenFound(); !s.TokenFound {
		s.NeedToken = NeedToken
	}
	return s
}

// tokenFound says whether the token source has a token, asking it at most every 15 s (on a
// Mac that runs `security`).
func (a *Adapter) tokenFound() bool {
	a.tokMu.Lock()
	defer a.tokMu.Unlock()
	if time.Since(a.tokAt) < 15*time.Second {
		return a.tokOK
	}
	_, err := a.cfg.Token()
	a.tokOK, a.tokAt = err == nil, time.Now()
	return a.tokOK
}

func (a *Adapter) setStatus(fn func(*Status)) {
	a.mu.Lock()
	fn(&a.status)
	a.mu.Unlock()
}

// Run keeps a connection to Home Assistant until ctx ends, reconnecting with backoff.
func (a *Adapter) Run(ctx context.Context, _ *devices.Hub) {
	wait := a.cfg.Backoff
	said := ""
	for ctx.Err() == nil {
		err := a.session(ctx)
		if ctx.Err() != nil {
			return
		}
		quick := false
		if err != nil {
			a.setStatus(func(s *Status) { s.Connected, s.Connecting, s.Error = false, false, err.Error() })
			// the same failure again and again is said once
			if err.Error() != said {
				said = err.Error()
				a.log.Info("home assistant", "url", a.url, "err", err)
			}
		}
		if errors.Is(err, errKicked) {
			quick, wait = true, a.cfg.Backoff
		}
		if !quick {
			d := wait + time.Duration(rand.Int64N(int64(wait)/4+1))
			select {
			case <-ctx.Done():
				return
			case <-a.kick:
				wait = a.cfg.Backoff
				continue
			case <-time.After(d):
			}
			wait = min(wait*2, 30*time.Second)
		}
	}
}

var errKicked = errors.New("重新连接")

// session is one connection: load everything, then follow state changes until it ends.
func (a *Adapter) session(parent context.Context) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	a.mu.Lock()
	a.cancel = cancel
	url := a.url
	a.status.Connecting = true
	a.mu.Unlock()
	select {
	case <-a.kick: // a kick that arrived before this session counts for it
	default:
	}
	token, err := a.cfg.Token()
	if err != nil {
		return err
	}
	dctx, dcancel := context.WithTimeout(ctx, 15*time.Second)
	c, err := Dial(dctx, url, token)
	dcancel()
	if err != nil {
		if parent.Err() == nil && ctx.Err() != nil {
			return errKicked
		}
		return err
	}
	defer c.Close()
	if err := a.load(ctx, c); err != nil {
		return err
	}
	changes, err := c.SubscribeStateChanged(ctx)
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.client = c
	a.status.Connected, a.status.Connecting, a.status.Error = true, false, ""
	a.mu.Unlock()
	a.log.Info("home assistant connected", "url", url, "version", c.Version)
	defer func() {
		a.mu.Lock()
		if a.client == c {
			a.client = nil
		}
		a.status.Connected = false
		a.mu.Unlock()
	}()
	ping := time.NewTicker(a.cfg.Ping)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			if parent.Err() == nil {
				return errKicked
			}
			return ctx.Err()
		case <-c.Done():
			return c.Err()
		case ch, ok := <-changes:
			if !ok {
				return c.Err()
			}
			a.onChange(ch)
		case <-ping.C:
			pctx, pc := context.WithTimeout(ctx, 10*time.Second)
			err := c.Ping(pctx)
			pc()
			if err != nil {
				return fmt.Errorf("Home Assistant 没有回应心跳：%v", err)
			}
		}
	}
}

// load reads the registries and every state, and reports the mapped entities to the hub.
func (a *Adapter) load(ctx context.Context, c *Client) error {
	cfg, err := c.GetConfig(ctx)
	if err != nil {
		return err
	}
	areas, err := c.AreaRegistry(ctx)
	if err != nil {
		return err
	}
	devs, err := c.DeviceRegistry(ctx)
	if err != nil {
		return err
	}
	ents, err := c.EntityRegistry(ctx)
	if err != nil {
		return err
	}
	states, err := c.GetStates(ctx)
	if err != nil {
		return err
	}
	areaName := map[string]string{}
	for _, ar := range areas {
		areaName[ar.AreaID] = ar.Name
	}
	devByID := map[string]DeviceEntry{}
	for _, d := range devs {
		devByID[d.ID] = d
	}
	entByID := map[string]EntityEntry{}
	for _, e := range ents {
		entByID[e.EntityID] = e
	}
	cat := map[string]*Entity{}
	for _, s := range states {
		e := &Entity{ID: s.EntityID, State: s.State, Attributes: s.Attributes, DeviceClass: deviceClass(s.Attributes)}
		e.Domain, _, _ = strings.Cut(s.EntityID, ".")
		e.Name, _ = s.Attributes["friendly_name"].(string)
		if e.Name == "" {
			e.Name = s.EntityID
		}
		if re, ok := entByID[s.EntityID]; ok {
			if re.DisabledBy != "" {
				continue
			}
			e.DeviceID = re.DeviceID
			if d, ok := devByID[re.DeviceID]; ok {
				e.Device = d.NameByUser
				if e.Device == "" {
					e.Device = d.Name
				}
				e.Area = areaName[d.AreaID]
			}
			if re.AreaID != "" {
				e.Area = areaName[re.AreaID]
			}
		}
		e.Kind = KindOf(s.EntityID, s.Attributes)
		e.Supported = e.Kind != ""
		cat[s.EntityID] = e
	}
	a.mu.Lock()
	a.entities = cat
	a.status.Version, a.status.LocationName = cfg.Version, cfg.LocationName
	if a.status.Version == "" {
		a.status.Version = c.Version
	}
	a.mu.Unlock()
	for _, m := range a.store.All() {
		a.report(m)
	}
	return nil
}

// report pushes the current HA state of a mapping's entities into its twin device.
func (a *Adapter) report(m Mapping) {
	st := map[string]any{}
	a.mu.RLock()
	for _, id := range m.Entities {
		if e, ok := a.entities[id]; ok {
			for k, v := range fromHA(m.Device.Kind, State{EntityID: id, State: e.State, Attributes: e.Attributes}) {
				st[k] = v
			}
		}
	}
	a.mu.RUnlock()
	if len(st) > 0 {
		a.hub.Report(m.Device.ID, st, "ha")
	}
}

func (a *Adapter) onChange(ch StateChange) {
	a.mu.Lock()
	a.status.LastEvent = time.Now()
	e, ok := a.entities[ch.EntityID]
	if ch.NewState == nil {
		delete(a.entities, ch.EntityID)
		a.mu.Unlock()
		return
	}
	if !ok {
		e = &Entity{ID: ch.EntityID, Name: ch.EntityID}
		e.Domain, _, _ = strings.Cut(ch.EntityID, ".")
		a.entities[ch.EntityID] = e
	}
	e.State, e.Attributes = ch.NewState.State, ch.NewState.Attributes
	if n, _ := e.Attributes["friendly_name"].(string); n != "" {
		e.Name = n
	}
	e.DeviceClass = deviceClass(e.Attributes)
	e.Kind = KindOf(e.ID, e.Attributes)
	e.Supported = e.Kind != ""
	a.mu.Unlock()
	if m, ok := a.store.ForEntity(ch.EntityID); ok {
		a.report(m)
	}
}

// Apply sends a checked change to HA. The rules and the person's confirmation for risky
// changes ran in devices.Hub.Command before this.
func (a *Adapter) Apply(ctx context.Context, d devices.Device, patch map[string]any) (map[string]any, error) {
	a.mu.RLock()
	c := a.client
	url := a.url
	var attrs map[string]any
	if e, ok := a.entities[d.Entity]; ok {
		attrs = e.Attributes
	}
	a.mu.RUnlock()
	cs, err := calls(d, patch, attrs)
	if err != nil {
		return nil, err
	}
	for _, cl := range cs {
		target := map[string]any{"entity_id": cl.Entity}
		if c != nil {
			err = c.CallService(ctx, cl.Domain, cl.Service, cl.Data, target)
		}
		if c == nil || (err != nil && c.Err() != nil) {
			// the socket is down: one call over REST
			token, terr := a.cfg.Token()
			if terr != nil {
				return nil, terr
			}
			err = REST{Base: url, Token: token}.CallService(ctx, cl.Domain, cl.Service, cl.Data, target)
		}
		if err != nil {
			return nil, err
		}
	}
	return expected(d.Kind, patch), nil
}

// Entities is the catalogue, supported ones first, by area and name.
func (a *Adapter) Entities() []Entity {
	a.mu.RLock()
	out := make([]Entity, 0, len(a.entities))
	for _, e := range a.entities {
		out = append(out, *e)
	}
	a.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Supported != out[j].Supported {
			return out[i].Supported
		}
		if out[i].Area != out[j].Area {
			return out[i].Area < out[j].Area
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Suggestion is where the twin would put an entity (or a pair of sensors).
type Suggestion struct {
	Entities []string `json:"entities"`
	Kind     string   `json:"kind"`
	Name     string   `json:"name"`
	Room     string   `json:"room"`
	RoomName string   `json:"roomName,omitempty"`
	Placed   Placed   `json:"placed"`
	// TakeOver is the simulated device of the same kind in that room it would replace.
	TakeOver string `json:"takeOver,omitempty"`
}

// Suggest proposes a twin device for every supported entity not mapped yet: the room from
// the HA area (or words in the name), a place in the room, and a simulated device it can
// take over so the model stays where it is.
func Suggest(h home.Home, ents []Entity, existing []devices.Device, store *MappingStore) []Suggestion {
	var out []Suggestion
	used := map[string]bool{}
	taken := map[string]bool{}
	if store != nil {
		for _, m := range store.All() {
			for _, e := range m.Entities {
				used[e] = true
			}
			if m.TakeOver != "" {
				taken[m.TakeOver] = true
			}
		}
	}
	byID := map[string]Entity{}
	for _, e := range ents {
		byID[e.ID] = e
	}
	for _, e := range ents {
		if !e.Supported || used[e.ID] {
			continue
		}
		s := Suggestion{Entities: []string{e.ID}, Kind: e.Kind, Name: e.Name}
		if e.Kind == devices.Sensor {
			// a temperature and a humidity sensor of the same HA device (or area) make one
			for _, o := range ents {
				if o.ID == e.ID || used[o.ID] || o.Kind != devices.Sensor || o.DeviceClass == e.DeviceClass {
					continue
				}
				same := (e.DeviceID != "" && o.DeviceID == e.DeviceID) || (e.DeviceID == "" && e.Area != "" && o.Area == e.Area) ||
					(e.DeviceID == "" && e.Area == "" && o.Area == "" && stem(e.ID) == stem(o.ID))
				if same {
					// the temperature sensor leads the pair
					if e.DeviceClass == "humidity" {
						s.Entities = []string{o.ID, e.ID}
					} else {
						s.Entities = append(s.Entities, o.ID)
					}
					used[o.ID] = true
					s.Name = sensorName(e, o)
					break
				}
			}
		}
		used[e.ID] = true
		s.Room = roomFor(h, e.Area)
		if s.Room == "" {
			s.Room = roomFor(h, e.Name+" "+strings.ReplaceAll(e.ID, "_", " ")+" "+e.Device)
		}
		if s.Kind == devices.Lock && s.Room == "" {
			s.Room = "entry"
		}
		if r := roomOf(h, s.Room); r != nil {
			s.RoomName = r.Name
		} else if s.Room == "outdoors" {
			s.RoomName = "室外"
		}
		if s.Room != "" {
			s.Placed, _ = Place(h, s.Kind, s.Room)
			if s.Kind == devices.Lock {
				s.Room = s.Placed.Room
			}
			for _, d := range existing {
				if d.Kind == s.Kind && d.Room == s.Room && d.Source == "sim" && !taken[d.ID] {
					s.TakeOver = d.ID
					taken[d.ID] = true
					break
				}
			}
		}
		s.Placed.Kind, s.Placed.Name, s.Placed.Room = s.Kind, s.Name, s.Room
		out = append(out, s)
	}
	return out
}

var stemRe = regexp.MustCompile(`_(temperature|temp|humidity|humid)$`)

func stem(id string) string { return stemRe.ReplaceAllString(id, "") }

func sensorName(a, b Entity) string {
	if a.Device != "" {
		return a.Device
	}
	n := a.Name
	for _, w := range []string{" Temperature", " temperature", " Humidity", " humidity", "温度", "湿度"} {
		n = strings.TrimSuffix(n, w)
	}
	return strings.TrimSpace(n)
}

// MapRequest is a person's mapping: which entities, what kind, which room, and optionally
// where. Without x/y/z the place is worked out as Suggest does.
type MapRequest struct {
	Entities []string `json:"entities"`
	Kind     string   `json:"kind"`
	Name     string   `json:"name"`
	Room     string   `json:"room"`
	TakeOver *bool    `json:"takeOver,omitempty"`
	// TakeOverID names the simulated device to replace; empty picks one of the kind in the room.
	TakeOverID string   `json:"takeOverId,omitempty"`
	X          *float64 `json:"x,omitempty"`
	Y          *float64 `json:"y,omitempty"`
	Z          *float64 `json:"z,omitempty"`
	Rot        *float64 `json:"rot,omitempty"`
	Width      *float64 `json:"width,omitempty"`
	Door       string   `json:"door,omitempty"`
}

var idSafe = regexp.MustCompile(`[^a-z0-9_-]+`)

// Map applies a mapping: the twin device is made (or a simulated one taken over) with Source
// "ha", and HA's current state flows into it.
func (a *Adapter) Map(req MapRequest) (Mapping, error) {
	if len(req.Entities) == 0 {
		return Mapping{}, errors.New("要选至少一个 Home Assistant 实体")
	}
	h := a.cfg.Home
	a.mu.RLock()
	first, known := a.entities[req.Entities[0]]
	var fe Entity
	if known {
		fe = *first
	}
	a.mu.RUnlock()
	if req.Kind == "" && known {
		req.Kind = fe.Kind
	}
	if !slices.Contains(devices.Kinds, req.Kind) {
		return Mapping{}, fmt.Errorf("没有「%s」这种设备", req.Kind)
	}
	if req.Room != "outdoors" && roomOf(h, req.Room) == nil {
		return Mapping{}, fmt.Errorf("家里没有「%s」这个房间", req.Room)
	}
	if req.Name == "" {
		req.Name = fe.Name
		if req.Name == "" {
			req.Name = req.Entities[0]
		}
	}
	p, _ := Place(h, req.Kind, req.Room)
	p.Kind, p.Name, p.Room = req.Kind, req.Name, req.Room
	if req.Kind == devices.Lock && req.Room == "" {
		p.Room = "entry"
	}
	m := Mapping{Entities: req.Entities}
	// take over a simulated device of the same kind in the room, unless told not to
	if req.TakeOver == nil || *req.TakeOver {
		taken := map[string]bool{}
		for _, o := range a.store.All() {
			if o.TakeOver != "" && !slices.Equal(o.Entities, req.Entities) {
				taken[o.TakeOver] = true
			}
		}
		prevTake := ""
		if prev, ok := a.store.ForEntity(req.Entities[0]); ok {
			prevTake = prev.TakeOver
		}
		for _, d := range a.hub.List() {
			if req.TakeOverID != "" && d.ID != req.TakeOverID {
				continue
			}
			mine := d.ID == req.TakeOverID || (prevTake != "" && d.ID == prevTake)
			if d.Kind == req.Kind && (d.Room == p.Room || req.TakeOverID != "") && (d.Source == "sim" || mine) && !taken[d.ID] {
				orig := d
				if prev, ok := a.store.ForEntity(req.Entities[0]); ok && prev.TakeOver == d.ID && prev.Original != nil {
					orig = *prev.Original
				}
				m.TakeOver, m.Original = d.ID, &orig
				p.ID, p.X, p.Y, p.Z, p.Rot, p.Width, p.Door = d.ID, d.X, d.Y, d.Z, d.Rot, d.Width, d.Door
				break
			}
		}
	}
	if req.X != nil {
		p.X = *req.X
	}
	if req.Y != nil {
		p.Y = *req.Y
	}
	if req.Z != nil {
		p.Z = *req.Z
	}
	if req.Rot != nil {
		p.Rot = *req.Rot
	}
	if req.Width != nil {
		p.Width = *req.Width
	}
	if req.Door != "" {
		p.Door = req.Door
	}
	if p.ID == "" {
		p.ID = "ha-" + idSafe.ReplaceAllString(strings.ToLower(strings.ReplaceAll(req.Entities[0], ".", "-")), "-")
	}
	m.Device = p
	// a remapped entity first lets go of what it held before
	if prev, ok := a.store.ForEntity(req.Entities[0]); ok && prev.Device.ID != p.ID {
		a.Unmap(req.Entities[0])
	}
	d := devices.Device{ID: p.ID, Kind: p.Kind, Name: p.Name, Room: p.Room, X: p.X, Y: p.Y, Z: p.Z, Rot: p.Rot, Width: p.Width, Door: p.Door, Source: "ha", Entity: req.Entities[0]}
	if old, ok := a.hub.Get(p.ID); ok {
		d.Topic = old.Topic
	}
	if _, err := a.hub.Put(d, "ha"); err != nil {
		return Mapping{}, err
	}
	if err := a.store.put(m); err != nil {
		return Mapping{}, err
	}
	a.report(m)
	return m, nil
}

// Unmap removes an entity's mapping: a taken-over simulated device comes back, a device made
// for HA goes.
func (a *Adapter) Unmap(entity string) error {
	m, ok, err := a.store.drop(entity)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%s 没有接到家里的设备上", entity)
	}
	if m.Original != nil {
		o := *m.Original
		o.Source, o.Entity = "sim", ""
		_, err := a.hub.Put(o, "ha")
		return err
	}
	return a.hub.Remove(m.Device.ID, "ha")
}

// AutoMap applies every suggestion that has a room.
func (a *Adapter) AutoMap() ([]Mapping, error) {
	var out []Mapping
	for _, s := range Suggest(a.cfg.Home, a.Entities(), a.hub.List(), a.store) {
		if s.Room == "" {
			continue
		}
		req := MapRequest{Entities: s.Entities, Kind: s.Kind, Name: s.Name, Room: s.Room, TakeOverID: s.TakeOver}
		if s.TakeOver == "" {
			f := false
			req.TakeOver = &f
			x, y, z, r, w := s.Placed.X, s.Placed.Y, s.Placed.Z, s.Placed.Rot, s.Placed.Width
			req.X, req.Y, req.Z, req.Rot, req.Width, req.Door = &x, &y, &z, &r, &w, s.Placed.Door
		}
		m, err := a.Map(req)
		if err != nil {
			return out, err
		}
		out = append(out, m)
	}
	return out, nil
}

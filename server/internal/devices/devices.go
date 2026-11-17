// Package devices keeps the smart devices of a home: where they are, what state they are in,
// and the way a change reaches them — rules first, then the adapter that drives the device
// (the simulator in v1, Home Assistant later).
package devices

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"time"
)

// Kinds a device can be.
const (
	Light   = "light"
	AC      = "ac"
	Lock    = "lock"
	Curtain = "curtain"
	Vacuum  = "vacuum"
	Sensor  = "sensor"
)

var Kinds = []string{Light, AC, Lock, Curtain, Vacuum, Sensor}

// Device is one smart device. X, Y, Z are metres (Y up); Rot turns its front (local +z), in
// degrees, the same way furniture turns.
type Device struct {
	ID   string  `json:"id"`
	Kind string  `json:"kind"`
	Name string  `json:"name"`
	Room string  `json:"room"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	Z    float64 `json:"z"`
	Rot  float64 `json:"rot"`
	// Width of a curtain, metres.
	Width float64 `json:"width,omitempty"`
	// Door a lock is fitted to.
	Door string `json:"door,omitempty"`
	// Topic is the device's logical MQTT topic, home/<domain>/<room>/<logical id>: it names
	// what the device is for and where, not which hardware, so hardware can be swapped
	// without renaming. State is published on it, changes are asked for on Topic + "/set".
	Topic string `json:"topic"`
	// Source names the adapter that drives it: sim, ha.
	Source string `json:"source"`
	// Entity is the device's id in that system (light.living_room).
	Entity string         `json:"entity,omitempty"`
	State  map[string]any `json:"state"`
	// T is when the state last changed, unix ms.
	T int64 `json:"t"`
}

// Adapter drives devices of one system.
type Adapter interface {
	Name() string
	// Apply sends a checked change to a device and returns the state the device reports back
	// (only the fields that changed are needed).
	Apply(ctx context.Context, d Device, patch map[string]any) (map[string]any, error)
	// Run keeps the adapter's devices up to date until ctx ends, reporting what changes.
	Run(ctx context.Context, h *Hub)
}

// Hub holds every device of one home.
type Hub struct {
	Home string
	log  *slog.Logger
	path string

	mu       sync.RWMutex
	devices  map[string]*Device
	adapters map[string]Adapter
	onChange []func(Event)
	dirty    bool
}

// Event is what the hub tells listeners: a device changed or was removed.
type Event struct {
	Type   string  `json:"type"` // device | removed
	Device *Device `json:"device,omitempty"`
	ID     string  `json:"id,omitempty"`
	// By is who caused it (a browser's tab id, "sim").
	By string `json:"by,omitempty"`
}

//go:embed defaults.json
var defaultsJSON []byte

// Defaults are the devices a sample home comes with.
func Defaults(homeID string) []Device {
	var all map[string][]Device
	if err := json.Unmarshal(defaultsJSON, &all); err != nil {
		panic(err)
	}
	out := all[homeID]
	for i := range out {
		if out[i].Source == "" {
			out[i].Source = "sim"
		}
		if out[i].Topic == "" {
			out[i].Topic = fmt.Sprintf("home/%s/%s/%s_1", Domain(out[i].Kind), out[i].Room, logical(out[i].Kind))
		}
		out[i].State = mergeState(initial(out[i].Kind), out[i].State)
	}
	return out
}

func initial(kind string) map[string]any {
	switch kind {
	case Light:
		return map[string]any{"on": false, "brightness": 80.0, "ct": 3000.0}
	case AC:
		return map[string]any{"on": false, "mode": "cool", "target": 24.0, "fan": "auto"}
	case Lock:
		return map[string]any{"locked": true, "open": false, "battery": 86.0}
	case Curtain:
		return map[string]any{"position": 100.0, "target": 100.0}
	case Vacuum:
		return map[string]any{"phase": "docked", "progress": 0.0, "battery": 100.0}
	case Sensor:
		return map[string]any{"temp": 24.0, "humidity": 60.0}
	}
	return map[string]any{}
}

func mergeState(base, over map[string]any) map[string]any {
	out := maps.Clone(base)
	maps.Copy(out, over)
	return out
}

// NewHub loads a home's devices from dir (devices-<home>.json), or starts from the defaults.
func NewHub(homeID, dir string, log *slog.Logger) (*Hub, error) {
	h := &Hub{Home: homeID, log: log, devices: map[string]*Device{}, adapters: map[string]Adapter{}}
	var list []Device
	if dir != "" {
		h.path = filepath.Join(dir, "devices-"+homeID+".json")
		b, err := os.ReadFile(h.path)
		switch {
		case err == nil:
			if err := json.Unmarshal(b, &list); err != nil {
				return nil, fmt.Errorf("%s: %w", h.path, err)
			}
		case !errors.Is(err, os.ErrNotExist):
			return nil, err
		}
	}
	if list == nil {
		list = Defaults(homeID)
	}
	for i := range list {
		d := list[i]
		d.State = mergeState(initial(d.Kind), d.State)
		h.devices[d.ID] = &d
	}
	return h, nil
}

// Use registers an adapter under its name; devices whose Source is that name go through it.
func (h *Hub) Use(a Adapter) { h.adapters[a.Name()] = a }

// Listen calls fn for every change. fn must not block.
func (h *Hub) Listen(fn func(Event)) {
	h.mu.Lock()
	h.onChange = append(h.onChange, fn)
	h.mu.Unlock()
}

func (h *Hub) emit(e Event) {
	h.mu.RLock()
	fns := slices.Clone(h.onChange)
	h.mu.RUnlock()
	for _, fn := range fns {
		fn(e)
	}
}

// List is every device, by id.
func (h *Hub) List() []Device {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]Device, 0, len(h.devices))
	for _, d := range h.devices {
		c := *d
		c.State = maps.Clone(d.State)
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (h *Hub) Get(id string) (Device, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	d, ok := h.devices[id]
	if !ok {
		return Device{}, false
	}
	c := *d
	c.State = maps.Clone(d.State)
	return c, true
}

// ErrNotFound is returned for an unknown device.
var ErrNotFound = errors.New("没有这个设备")

// Command checks a change against the rules and sends it to the device's adapter. A change that
// needs confirming returns a *NeedConfirm until it is sent again with confirm set.
func (h *Hub) Command(ctx context.Context, id string, patch map[string]any, confirm bool, by string) (Device, error) {
	d, ok := h.Get(id)
	if !ok {
		return d, ErrNotFound
	}
	if err := Check(d, patch, confirm); err != nil {
		return d, err
	}
	a, ok := h.adapters[d.Source]
	if !ok {
		return d, fmt.Errorf("%s 由 %s 控制，但它没有接上", d.Name, d.Source)
	}
	got, err := a.Apply(ctx, d, patch)
	if err != nil {
		return d, err
	}
	return h.Report(id, got, by), nil
}

// Report merges what a device says about itself into its state and tells listeners.
func (h *Hub) Report(id string, state map[string]any, by string) Device {
	h.mu.Lock()
	d, ok := h.devices[id]
	if !ok {
		h.mu.Unlock()
		return Device{}
	}
	changed := false
	for k, v := range state {
		if old, ok := d.State[k]; !ok || old != v {
			d.State[k] = v
			changed = true
		}
	}
	// a sensor's reading is news even when it reads the same as before
	news := changed || d.Kind == Sensor
	if news {
		d.T = time.Now().UnixMilli()
		h.dirty = true
	}
	c := *d
	c.State = maps.Clone(d.State)
	h.mu.Unlock()
	if news {
		h.emit(Event{Type: "device", Device: &c, By: by})
	}
	return c
}

// Put adds a device or moves an existing one (its state is kept).
func (h *Hub) Put(d Device, by string) (Device, error) {
	if !slices.Contains(Kinds, d.Kind) {
		return d, fmt.Errorf("没有 %s 这种设备", d.Kind)
	}
	if d.ID == "" {
		d.ID = fmt.Sprintf("%s-%d", d.Kind, time.Now().UnixNano()%1e9)
	}
	if d.Source == "" {
		d.Source = "sim"
	}
	h.mu.Lock()
	if d.Topic == "" {
		d.Topic = h.freeTopic(d)
	}
	if old, ok := h.devices[d.ID]; ok {
		d.State = old.State
	} else {
		d.State = mergeState(initial(d.Kind), d.State)
	}
	d.T = time.Now().UnixMilli()
	h.devices[d.ID] = &d
	h.dirty = true
	c := d
	c.State = maps.Clone(d.State)
	h.mu.Unlock()
	h.emit(Event{Type: "device", Device: &c, By: by})
	return c, nil
}

func (h *Hub) Remove(id, by string) error {
	h.mu.Lock()
	_, ok := h.devices[id]
	delete(h.devices, id)
	h.dirty = ok || h.dirty
	h.mu.Unlock()
	if !ok {
		return ErrNotFound
	}
	h.emit(Event{Type: "removed", ID: id, By: by})
	return nil
}

// Run starts every adapter and saves the devices every few seconds when they changed.
func (h *Hub) Run(ctx context.Context) {
	for _, a := range h.adapters {
		go a.Run(ctx, h)
	}
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			h.save()
			return
		case <-t.C:
			h.save()
		}
	}
}

func (h *Hub) save() {
	h.mu.Lock()
	if !h.dirty || h.path == "" {
		h.mu.Unlock()
		return
	}
	h.dirty = false
	h.mu.Unlock()
	b, _ := json.MarshalIndent(h.List(), "", "  ")
	tmp := h.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err == nil {
		err = os.Rename(tmp, h.path)
		if err != nil && h.log != nil {
			h.log.Warn("save devices", "err", err)
		}
	}
}

// Domain is the first topic level after home/ for a kind of device.
func Domain(kind string) string {
	return map[string]string{Light: "lighting", AC: "climate", Lock: "security", Curtain: "covers", Vacuum: "cleaning", Sensor: "environment"}[kind]
}

func logical(kind string) string {
	if kind == Sensor {
		return "temp_sensor"
	}
	return kind
}

// freeTopic is the next unused logical topic for a device in its room. Caller holds h.mu.
func (h *Hub) freeTopic(d Device) string {
	room := d.Room
	if room == "" {
		room = "home"
	}
	used := map[string]bool{}
	for _, o := range h.devices {
		used[o.Topic] = true
	}
	for n := 1; ; n++ {
		t := fmt.Sprintf("home/%s/%s/%s_%d", Domain(d.Kind), room, logical(d.Kind), n)
		if !used[t] {
			return t
		}
	}
}

// ByTopic finds the device publishing on a topic.
func (h *Hub) ByTopic(topic string) (Device, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, d := range h.devices {
		if d.Topic == topic {
			c := *d
			c.State = maps.Clone(d.State)
			return c, true
		}
	}
	return Device{}, false
}

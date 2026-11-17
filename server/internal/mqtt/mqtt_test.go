package mqtt

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	mq "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/packets"

	"hometwin/internal/devices"
	"hometwin/internal/home"
)

func freeAddr(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

func TestBroker(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hub, _ := devices.NewHub("chengdu-89", "", nil)
	hub.Use(devices.NewSim(home.Sample()))
	var mu sync.Mutex
	var asked []string
	b, err := Start(ctx, freeAddr(t), hub, func(id string, _ map[string]any, reason string) {
		mu.Lock()
		asked = append(asked, id)
		mu.Unlock()
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]map[string]any{}
	b.srv.Subscribe("home/#", 9, func(_ *mq.Client, _ packets.Subscription, pk packets.Packet) {
		var m map[string]any
		if json.Unmarshal(pk.Payload, &m) == nil {
			mu.Lock()
			got[pk.TopicName] = m
			mu.Unlock()
		}
	})
	// retained state is there for a late subscriber
	mu.Lock()
	_, ok := got["home/lighting/living/light_1"]
	mu.Unlock()
	if !ok {
		t.Fatal("no retained state for the living room light")
	}
	b.srv.Publish("home/lighting/living/light_1/set", []byte(`{"on": true, "brightness": 40}`), false, 0)
	b.srv.Publish("home/climate/living/ac_1/set", []byte(`{"target": 35}`), false, 0)
	b.srv.Publish("home/security/entry/lock_1/set", []byte(`{"locked": false}`), false, 0)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		st, _ := got["home/lighting/living/light_1"]["state"].(map[string]any)
		e := got["home/climate/living/ac_1/error"]
		n := len(asked)
		mu.Unlock()
		if st["on"] == true && e != nil && n == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if d, _ := hub.Get("light-living"); d.State["on"] != true || d.State["brightness"] != 40.0 {
		t.Fatalf("light not set over MQTT: %+v", d.State)
	}
	mu.Lock()
	defer mu.Unlock()
	if e := got["home/climate/living/ac_1/error"]; e == nil {
		t.Fatal("35 ℃ should be refused on the error topic")
	}
	if d, _ := hub.Get("lock-entry"); d.State["locked"] != true || len(asked) != 1 {
		t.Fatalf("unlocking over MQTT must wait for a person: %+v asked %v", d.State, asked)
	}
}

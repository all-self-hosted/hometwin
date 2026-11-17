// Package mqtt is the home's MQTT broker. Every device's state is published, retained, on its
// logical topic (home/lighting/living/light_1), so anything that speaks MQTT can follow the
// home with one subscription to home/#; a change is asked for by publishing a JSON patch on
// the topic plus /set ({"brightness": 40}). Changes from MQTT pass the same rules as the
// browser's; one that needs a person (unlocking) is put before one instead of being run.
package mqtt

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"

	mqtt "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/mochi-mqtt/server/v2/packets"

	"hometwin/internal/devices"
)

type Broker struct {
	srv *mqtt.Server
	hub *devices.Hub
	log *slog.Logger
	ask func(device string, patch map[string]any, reason string)
}

// Start runs the broker on addr (127.0.0.1:port keeps it to this machine) and keeps it in
// step with the hub. ask is called for a change that needs a person's confirmation.
func Start(ctx context.Context, addr string, hub *devices.Hub, ask func(device string, patch map[string]any, reason string), log *slog.Logger) (*Broker, error) {
	s := mqtt.New(&mqtt.Options{InlineClient: true, Logger: log.With("part", "mqtt")})
	if err := s.AddHook(new(auth.AllowHook), nil); err != nil {
		return nil, err
	}
	if err := s.AddListener(listeners.NewTCP(listeners.Config{ID: "tcp", Address: addr})); err != nil {
		return nil, err
	}
	b := &Broker{srv: s, hub: hub, log: log, ask: ask}
	go func() {
		if err := s.Serve(); err != nil {
			log.Error("mqtt broker stopped", "err", err)
		}
	}()
	for _, d := range hub.List() {
		b.publish(d)
	}
	hub.Listen(func(e devices.Event) {
		if e.Type == "device" && e.Device != nil {
			b.publish(*e.Device)
		}
	})
	// handled off the broker's goroutine: a change publishes the new state again
	set := func(cl *mqtt.Client, sub packets.Subscription, pk packets.Packet) { go b.onSet(cl, sub, pk) }
	if err := s.Subscribe("home/+/+/+/set", 1, set); err != nil {
		return nil, err
	}
	go func() {
		<-ctx.Done()
		s.Close()
	}()
	return b, nil
}

// payload is what is published for a device: its state plus who and where it is.
func payload(d devices.Device) []byte {
	b, _ := json.Marshal(map[string]any{"id": d.ID, "name": d.Name, "kind": d.Kind, "room": d.Room, "state": d.State, "t": d.T})
	return b
}

func (b *Broker) publish(d devices.Device) {
	if d.Topic == "" {
		return
	}
	if err := b.srv.Publish(d.Topic, payload(d), true, 0); err != nil {
		b.log.Warn("mqtt publish", "topic", d.Topic, "err", err)
	}
}

func (b *Broker) onSet(cl *mqtt.Client, _ packets.Subscription, pk packets.Packet) {
	topic := strings.TrimSuffix(pk.TopicName, "/set")
	d, ok := b.hub.ByTopic(topic)
	if !ok {
		b.reply(topic, "没有设备用这个主题")
		return
	}
	var patch map[string]any
	if err := json.Unmarshal(pk.Payload, &patch); err != nil {
		b.reply(topic, "要发 JSON，比如 {\"on\": true}")
		return
	}
	who := "MQTT"
	if cl != nil && cl.ID != "" {
		who = "MQTT " + cl.ID
	}
	_, err := b.hub.Command(context.Background(), d.ID, patch, false, who)
	var nc *devices.NeedConfirm
	if errors.As(err, &nc) {
		if b.ask != nil {
			b.ask(d.ID, patch, nc.Text)
		}
		b.reply(topic, "等人确认："+nc.Text)
		return
	}
	if err != nil {
		b.reply(topic, err.Error())
	}
}

// reply says why a change did not happen, on the topic plus /error.
func (b *Broker) reply(topic, text string) {
	m, _ := json.Marshal(map[string]string{"error": text})
	b.srv.Publish(topic+"/error", m, false, 0)
}

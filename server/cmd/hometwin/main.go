// hometwin serves a live 3D twin of a home: the front end, its API, the simulated devices and
// an MQTT broker on the devices' logical topics.
//
//	hometwin [-http 127.0.0.1:6417] [-data ./data] [-mqtt 127.0.0.1:47183] [-speed 1]
//
// HTTPS_PROXY is used for fetching product pages; BLENDER (path to the Blender binary) turns
// on generated furniture models; HA_URL and HA_TOKEN are read for the Home Assistant adapter.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"hometwin/internal/assistant"
	"hometwin/internal/automate"
	"hometwin/internal/catalog"
	"hometwin/internal/devices"
	"hometwin/internal/floorplan"
	"hometwin/internal/ha"
	"hometwin/internal/home"
	"hometwin/internal/ikea"
	"hometwin/internal/insight"
	"hometwin/internal/layout"
	"hometwin/internal/models"
	"hometwin/internal/mqtt"
	"hometwin/internal/ontology"
	"hometwin/internal/predict"
	"hometwin/internal/scan"
	hweb "hometwin/internal/web"
	"hometwin/web"
)

var version = "dev"

func main() {
	addr := flag.String("http", "127.0.0.1:6417", "address to serve on")
	data := flag.String("data", "data", "directory for layouts, devices and generated models")
	mq := flag.String("mqtt", "127.0.0.1:47183", "MQTT broker address (empty: no broker)")
	speed := flag.Float64("speed", 1, "how much faster than real time the simulated home runs")
	vrImport := flag.Bool("vr-import", false, "turn on importing 贝壳/如视 VR scans (experimental; only for scans of your own home)")
	showVersion := flag.Bool("version", false, "print the version")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	err := run(*addr, *data, *mq, *speed, *vrImport, log)
	if errors.Is(err, errRestart) {
		// a new home was confirmed: start over on it, same process, same arguments
		log.Info("hometwin", "restart", "home changed")
		err = restart()
	}
	if err != nil {
		log.Error("hometwin", "err", err)
		os.Exit(1)
	}
}

// errRestart ends run when the home in use has changed.
var errRestart = errors.New("restart")

func run(addr, data, mq string, speed float64, vrImport bool, log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	h, err := home.Load(data)
	if err != nil {
		return fmt.Errorf("home: %w", err)
	}
	store, err := layout.NewStore(data)
	if err != nil {
		return err
	}
	hub, err := devices.NewHub(h.ID, data, log)
	if err != nil {
		return err
	}
	sim := devices.NewSim(h)
	sim.Speed = speed
	hub.Use(sim)
	auto, err := automate.New(hub, data)
	if err != nil {
		return err
	}
	rooms := map[string]string{}
	for _, r := range h.Rooms {
		rooms[r.ID] = r.Name
	}
	onto, err := ontology.Open(ctx, filepath.Join(data, "home.cortex.db"))
	if err != nil {
		return fmt.Errorf("knowledge graph: %w", err)
	}
	defer onto.Close()
	builder := models.New(os.Getenv("BLENDER"), filepath.Join(data, "models"))
	haStore, err := ha.NewMappingStore(data)
	if err != nil {
		return fmt.Errorf("home assistant mapping: %w", err)
	}
	homeAssistant := ha.New(ha.Config{Token: ha.DefaultToken(), Dir: data, Home: h}, hub, haStore, log)
	hub.Use(homeAssistant)
	srv := &hweb.Server{
		Home: h, Layouts: store, Devices: hub, Sim: sim, Auto: auto,
		Insight: insight.New(rooms), Estimate: predict.NewEstimator(h), Onto: onto,
		Models: builder.Handler(), HA: homeAssistant.Handler(), Static: web.Dist(), Log: log, HTTP: ikea.NewClient(),
	}
	if mq != "" {
		ask := func(id string, patch map[string]any, reason string) { auto.Ask(id, patch, "MQTT", reason) }
		if _, err := mqtt.Start(ctx, mq, hub, ask, log); err != nil {
			return fmt.Errorf("mqtt: %w", err)
		}
		srv.MQTT = mq
		log.Info("mqtt broker", "addr", mq, "topics", "home/#")
	}
	llm, err := floorplan.ModelFromEnv()
	if err != nil {
		return err
	}
	srv.Catalog = catalog.New(filepath.Join(data, "catalog"), llm)
	helper, err := assistant.New(assistant.Deps{
		Home: h, Layouts: store, Devices: hub, Auto: auto, Estimate: srv.Estimate, HTTP: srv.HTTP,
		Knowledge: knowledge{srv}, Catalog: srv.Catalog, Log: log,
	}, filepath.Join(data, "assistant"))
	if err != nil {
		return fmt.Errorf("assistant: %w", err)
	}
	defer helper.Close()
	srv.Assistant = helper.Handler()
	changed := make(chan struct{}, 1)
	useHome := func(next home.Home) error {
		if err := home.Use(data, next); err != nil {
			return err
		}
		select {
		case changed <- struct{}{}:
		default:
		}
		return nil
	}
	importer, err := floorplan.NewImporter(filepath.Join(data, "floorplan"), llm, func() home.Home { return h }, useHome, log)
	if err != nil {
		return fmt.Errorf("floor plan import: %w", err)
	}
	defer importer.Close()
	srv.Floorplan = importer.Handler()
	var readPlan func(ctx context.Context, img image.Image) (floorplan.Draft, error)
	if llm != nil {
		readPlan = func(ctx context.Context, img image.Image) (floorplan.Draft, error) {
			d, _, err := floorplan.ReadDraft(ctx, llm, img, "plan", "户型图")
			return d, err
		}
	}
	scans, err := scan.NewImporter(filepath.Join(data, "scan"), readPlan, func() home.Home { return h }, useHome, nil, log)
	if err != nil {
		return fmt.Errorf("scan import: %w", err)
	}
	srv.Scan = scans.Handler()
	srv.VRImport = vrImport
	log.Info("assistant", "model", helper.Model(), "configured", helper.Configured())
	srv.Wire(ctx)
	go hub.Run(ctx)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	password, err := readPassword()
	if err != nil {
		return err
	}
	handler, err := hweb.Guard(srv.Handler(), password, filepath.Join(data, "session.key"))
	if err != nil {
		return fmt.Errorf("password: %w", err)
	}
	if password == "" && !loopback(ln.Addr()) {
		log.Warn("serving beyond this machine with no password; set HOMETWIN_PASSWORD", "addr", ln.Addr().String())
	}
	hs := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	var restarting atomic.Bool
	hup := hangups()
	go func() {
		select {
		case <-ctx.Done():
		case <-hup:
			restarting.Store(true)
		case <-changed:
			restarting.Store(true)
			time.Sleep(300 * time.Millisecond) // let the answer reach the page
		}
		sh, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		hs.Shutdown(sh)
	}()
	log.Info("hometwin", "url", "http://"+ln.Addr().String(), "version", version, "blender", builder.Available(), "home", h.Name)
	if err := hs.Serve(ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	if restarting.Load() {
		return errRestart
	}
	return nil
}

// readPassword is the password in front of the web UI: HOMETWIN_PASSWORD, or the first line
// of the file HOMETWIN_PASSWORD_FILE names. Empty means none.
func readPassword() (string, error) {
	if p := os.Getenv("HOMETWIN_PASSWORD"); p != "" {
		return p, nil
	}
	f := os.Getenv("HOMETWIN_PASSWORD_FILE")
	if f == "" {
		return "", nil
	}
	b, err := os.ReadFile(f)
	if err != nil {
		return "", fmt.Errorf("HOMETWIN_PASSWORD_FILE: %w", err)
	}
	p, _, _ := strings.Cut(string(b), "\n")
	return strings.TrimSpace(p), nil
}

func loopback(a net.Addr) bool {
	t, ok := a.(*net.TCPAddr)
	return ok && t.IP.IsLoopback()
}

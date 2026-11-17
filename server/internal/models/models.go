// Package models turns a product's real size into a 3D model: a Blender script per kind of
// furniture, run headless, exported as GLB and cached by spec. Without Blender the server says
// so and the browser builds its own simpler model.
package models

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"hometwin/internal/ikea"
)

// ScriptVersion is part of every cache key: bump it when the scripts change what they build.
const ScriptVersion = "1"

//go:embed blender/*.py
var scripts embed.FS

// Spec is what the furniture script builds from. Sizes are centimetres.
type Spec struct {
	Type     string             `json:"type"`
	W        float64            `json:"w"`
	D        float64            `json:"d"`
	H        float64            `json:"h"`
	Color    string             `json:"color"`
	Material string             `json:"material"`
	Options  map[string]any     `json:"options,omitempty"`
	Measures map[string]float64 `json:"measures,omitempty"`
}

var (
	reSeats    = regexp.MustCompile(`(?i)(\d)\s*-?\s*seat|([一二三四两])人`)
	reDrawers  = regexp.MustCompile(`(?i)(\d+)\s*-?\s*drawers?|chest of (\d+)|([一二三四五六七八两])斗`)
	reDoors    = regexp.MustCompile(`(?i)(\d)\s*doors?|([单一二三四两双])门`)
	reShelves  = regexp.MustCompile(`(?i)(\d+)\s*shelves`)
	cnDigits   = map[string]int{"一": 1, "单": 1, "二": 2, "两": 2, "双": 2, "三": 3, "四": 4, "五": 5, "六": 6, "七": 7, "八": 8}
	woodWords  = []string{"veneer", "oak", "birch", "walnut", "pine", "beech", "bamboo", "acacia", "橡木", "桦木", "胡桃", "松木", "实木", "贴面", "竹"}
	metalWords = []string{"steel", "metal", "aluminium", "aluminum", "钢", "金属", "铝"}
	glassWords = []string{"glass", "玻璃"}
	fabWords   = []string{"upholstered", "fabric", "布艺", "软包"}
)

func num(groups []string) int {
	for _, g := range groups[1:] {
		if g == "" {
			continue
		}
		if n, err := strconv.Atoi(g); err == nil {
			return n
		}
		if n, ok := cnDigits[g]; ok {
			return n
		}
	}
	return 0
}

func hasAny(s string, words []string) bool {
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

// SpecFor reads what a model needs out of a product.
func SpecFor(p ikea.Product) Spec {
	s := Spec{Type: p.Type, W: p.W, D: p.D, H: p.H, Color: p.ColorHex, Options: map[string]any{}, Measures: map[string]float64{}}
	if s.Type == "" {
		s.Type = "box"
	}
	if s.Color == "" {
		s.Color = "#e9e7e1"
	}
	text := strings.ToLower(p.Name + " " + p.Color)
	switch {
	case s.Type == "sofa" || s.Type == "armchair" || s.Type == "rug":
		s.Material = "fabric"
	case s.Type == "bed" && hasAny(text, fabWords):
		s.Material = "fabric"
	case hasAny(text, woodWords):
		s.Material = "wood"
	case hasAny(text, metalWords) && s.Type != "lamp":
		s.Material = "metal"
	case hasAny(text, glassWords) && s.Type == "table":
		s.Material = "glass"
	case s.Type == "lamp":
		s.Material = "metal"
	default:
		s.Material = "painted"
	}
	if s.Type == "armchair" && hasAny(text, []string{"birch", "桦木", "oak", "橡木"}) {
		s.Options["legs"] = "wood"
	}
	switch s.Type {
	case "bookcase":
		if m := reShelves.FindStringSubmatch(text); m != nil {
			s.Options["shelves"] = num(m)
		} else {
			// about one shelf every 37 cm of inside height, as the script does
			inner := p.H/100 - 0.096
			s.Options["shelves"] = max(1, int(math.Round(inner/0.37))-1)
		}
	case "sofa":
		if m := reSeats.FindStringSubmatch(text); m != nil && num(m) > 0 {
			s.Options["seats"] = num(m)
		} else if p.W >= 200 {
			s.Options["seats"] = 3
		} else if p.W >= 150 {
			s.Options["seats"] = 2
		} else {
			s.Options["seats"] = 1
		}
	case "drawers", "desk", "tvbench":
		if m := reDrawers.FindStringSubmatch(text); m != nil && num(m) > 0 {
			s.Options["drawers"] = num(m)
		}
	case "wardrobe":
		if m := reDoors.FindStringSubmatch(text); m != nil && num(m) > 0 {
			s.Options["doors"] = num(m)
		} else if p.W <= 60 {
			s.Options["doors"] = 1
		} else if p.W <= 120 {
			s.Options["doors"] = 2
		} else {
			s.Options["doors"] = 3
		}
	}
	for _, m := range p.Measures {
		if m.Key != "" && m.CM > 0 {
			s.Measures[m.Key] = m.CM
		}
	}
	return s
}

// Key names a spec's model: the same spec always gives the same key.
func Key(s Spec) string {
	// round sizes so a size parsed again a hair differently reuses the model
	r := func(v float64) float64 { return math.Round(v*10) / 10 }
	opts := make([]string, 0, len(s.Options))
	for k, v := range s.Options {
		opts = append(opts, fmt.Sprintf("%s=%v", k, v))
	}
	sort.Strings(opts)
	meas := make([]string, 0, len(s.Measures))
	for k, v := range s.Measures {
		meas = append(meas, fmt.Sprintf("%s=%.1f", k, r(v)))
	}
	sort.Strings(meas)
	src := fmt.Sprintf("v%s|%s|%.1f|%.1f|%.1f|%s|%s|%s|%s", ScriptVersion, s.Type, r(s.W), r(s.D), r(s.H),
		strings.ToLower(s.Color), s.Material, strings.Join(opts, ","), strings.Join(meas, ","))
	h := sha256.Sum256([]byte(src))
	return s.Type + "-" + hex.EncodeToString(h[:8])
}

// Builder runs Blender to make models and keeps them in Dir.
type Builder struct {
	Blender string
	Dir     string
	// Draco compresses the meshes (the browser then needs the Draco decoder).
	Draco   bool
	Timeout time.Duration

	slots    chan struct{}
	mu       sync.Mutex
	inflight map[string]*call
	failed   map[string]string
	scriptMu sync.Mutex
	scripted bool
}

type call struct {
	done chan struct{}
	path string
	err  error
}

// New makes a builder. blender "" means the BLENDER environment variable; when that is empty
// too the builder is unavailable.
func New(blender, dir string) *Builder {
	if blender == "" {
		blender = os.Getenv("BLENDER")
	}
	if blender != "" {
		if _, err := os.Stat(blender); err != nil {
			if p, err := exec.LookPath(blender); err == nil {
				blender = p
			} else {
				blender = ""
			}
		}
	}
	return &Builder{Blender: blender, Dir: dir, Draco: true, Timeout: 120 * time.Second,
		slots: make(chan struct{}, 2), inflight: map[string]*call{}, failed: map[string]string{}}
}

// Available says whether Blender is there to build models.
func (b *Builder) Available() bool { return b != nil && b.Blender != "" }

// Path is where a key's model is kept.
func (b *Builder) Path(key string) string { return filepath.Join(b.Dir, key+".glb") }

// Cached says whether a key's model has been built.
func (b *Builder) Cached(key string) bool {
	_, err := os.Stat(b.Path(key))
	return err == nil
}

// Building says whether a key's model is being built now.
func (b *Builder) Building(key string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.inflight[key]
	return ok
}

// Failed is why the last build of a key failed, if it did.
func (b *Builder) Failed(key string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.failed[key]
}

func (b *Builder) writeScripts() (string, error) {
	b.scriptMu.Lock()
	defer b.scriptMu.Unlock()
	dir := filepath.Join(b.Dir, "_scripts", "v"+ScriptVersion)
	if b.scripted {
		return dir, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	entries, err := scripts.ReadDir("blender")
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		data, err := scripts.ReadFile("blender/" + e.Name())
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), data, 0o644); err != nil {
			return "", err
		}
	}
	b.scripted = true
	return dir, nil
}

// ErrUnavailable is returned when there is no Blender to build with.
var ErrUnavailable = errors.New("没有配置 Blender，用浏览器里的简化模型")

// Build makes the model of a spec, or returns the cached one. Concurrent builds of the same spec
// share one Blender run; at most two run at once.
func (b *Builder) Build(ctx context.Context, s Spec) (string, error) {
	if !b.Available() {
		return "", ErrUnavailable
	}
	key := Key(s)
	if b.Cached(key) {
		return b.Path(key), nil
	}
	c, leader := b.begin(key)
	if leader {
		b.finish(key, c, s)
	}
	select {
	case <-c.done:
		return c.path, c.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// begin joins the build of key, or registers a new one (leader true) the caller must finish.
func (b *Builder) begin(key string) (*call, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if c, ok := b.inflight[key]; ok {
		return c, false
	}
	c := &call{done: make(chan struct{})}
	b.inflight[key] = c
	delete(b.failed, key)
	return c, true
}

func (b *Builder) finish(key string, c *call, s Spec) {
	ctx, cancel := context.WithTimeout(context.Background(), b.Timeout+10*time.Second)
	defer cancel()
	c.path, c.err = b.run(ctx, s, key)
	b.mu.Lock()
	delete(b.inflight, key)
	if c.err != nil {
		b.failed[key] = c.err.Error()
	}
	b.mu.Unlock()
	close(c.done)
}

// Start begins building a spec's model in the background unless it is built or being built.
func (b *Builder) Start(s Spec) {
	if !b.Available() {
		return
	}
	key := Key(s)
	if b.Cached(key) {
		return
	}
	if c, leader := b.begin(key); leader {
		go b.finish(key, c, s)
	}
}

func (b *Builder) run(ctx context.Context, s Spec, key string) (string, error) {
	select {
	case b.slots <- struct{}{}:
		defer func() { <-b.slots }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	if err := os.MkdirAll(b.Dir, 0o755); err != nil {
		return "", err
	}
	dir, err := b.writeScripts()
	if err != nil {
		return "", err
	}
	spec, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	specPath := filepath.Join(b.Dir, key+".spec.json")
	if err := os.WriteFile(specPath, spec, 0o644); err != nil {
		return "", err
	}
	defer os.Remove(specPath)
	tmp := filepath.Join(b.Dir, key+".tmp.glb")
	args := []string{"-b", "--factory-startup", "-noaudio", "-P", filepath.Join(dir, "furniture.py"), "--", specPath, tmp}
	if !b.Draco {
		args = append(args, "--no-draco")
	}
	tctx, cancel := context.WithTimeout(ctx, b.Timeout)
	defer cancel()
	cmd := exec.CommandContext(tctx, b.Blender, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err = cmd.Run()
	if err == nil && !strings.Contains(out.String(), "HT-OK") {
		err = errors.New("脚本没有导出模型")
	}
	if err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("Blender 生成模型失败：%v\n%s", err, tail(out.String(), 1200))
	}
	path := b.Path(key)
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	return path, nil
}

// tail is the end of a long log, the part that says what went wrong.
func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

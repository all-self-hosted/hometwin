package scan

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"hometwin/internal/floorplan"
	"hometwin/internal/home"
)

// Importer runs VR-scan imports: a share link (and, if given, the listing's floor plan) in,
// a draft home and its textured model out, made the home in use when a person confirms it.
type Importer struct {
	dir string
	// readPlan reads a floor-plan picture; nil when no model is configured (the picture is
	// then only shown next to the result).
	readPlan func(ctx context.Context, img image.Image) (floorplan.Draft, error)
	current  func() home.Home
	use      func(home.Home) error
	client   *http.Client
	log      *slog.Logger

	mu   sync.Mutex
	jobs map[string]*Job
}

// Job is one import as the page follows it.
type Job struct {
	ID      string           `json:"id"`
	Status  string           `json:"status"` // running, done, failed
	Steps   []floorplan.Step `json:"steps"`
	Error   string           `json:"error,omitempty"`
	Result  *Result          `json:"result,omitempty"`
	View    *ViewInfo        `json:"view,omitempty"`
	Plan    bool             `json:"plan"` // a floor-plan picture came with it
	Created int64            `json:"created"`

	link string // never logged
	img  []byte
}

// ViewInfo places the top view (GET /api/scan/{id}/top.png) in the home's frame.
type ViewInfo struct {
	X0   float64 `json:"x0"`
	Z0   float64 `json:"z0"`
	Cell float64 `json:"cell"`
	W    int     `json:"w"`
	H    int     `json:"h"`
}

// NewImporter keeps downloads, drafts and the confirmed homes' models under dir.
func NewImporter(dir string, readPlan func(ctx context.Context, img image.Image) (floorplan.Draft, error), current func() home.Home, use func(home.Home) error, client *http.Client, log *slog.Logger) (*Importer, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if log == nil {
		log = slog.Default()
	}
	return &Importer{dir: dir, readPlan: readPlan, current: current, use: use, client: client, log: log, jobs: map[string]*Job{}}, nil
}

func newID() string {
	b := make([]byte, 5)
	rand.Read(b)
	return time.Now().Format("0102") + "-" + hex.EncodeToString(b)
}

// linkRe finds the share link in whatever was pasted (the apps share "text + link").
var linkRe = regexp.MustCompile(`https?://[^\s"'<>，。]+`)

// Start begins an import and returns its job straight away.
func (im *Importer) Start(pasted string, planImg []byte) (*Job, error) {
	link := linkRe.FindString(pasted)
	if link == "" {
		return nil, errors.New("没找到链接：把 VR 看房的分享链接整段贴进来")
	}
	j := &Job{ID: newID(), Status: "running", Steps: []floorplan.Step{}, Created: time.Now().UnixMilli(), link: link, img: planImg, Plan: len(planImg) > 0}
	im.mu.Lock()
	im.jobs[j.ID] = j
	im.mu.Unlock()
	go im.run(j)
	return j, nil
}

func (im *Importer) step(j *Job, format string, a ...any) {
	im.mu.Lock()
	defer im.mu.Unlock()
	j.Steps = append(j.Steps, floorplan.Step{T: time.Now().UnixMilli(), Text: fmt.Sprintf(format, a...)})
}

func (im *Importer) fail(j *Job, err error) {
	im.log.Info("scan import", "job", j.ID, "err", err)
	im.mu.Lock()
	defer im.mu.Unlock()
	j.Status, j.Error = "failed", err.Error()
}

func (im *Importer) jobDir(id string) string { return filepath.Join(im.dir, "jobs", id) }

func (im *Importer) run(j *Job) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	dir := im.jobDir(j.ID)
	im.step(j, "打开 VR 看房页面")
	w, err := Fetch(ctx, j.link, im.client)
	if err != nil {
		im.fail(j, err)
		return
	}
	im.step(j, "页面里有 %d 个拍摄点、%d 张贴图", len(w.Observers), len(w.Model.Textures))
	files, err := Download(ctx, w, dir, im.client, 0)
	if err != nil {
		im.fail(j, err)
		return
	}
	mb, err := os.ReadFile(files.Model)
	if err != nil {
		im.fail(j, err)
		return
	}
	m, err := Decode(mb)
	if err != nil {
		im.fail(j, err)
		return
	}
	im.step(j, "下载并解开三维模型：%d 个顶点、%d 个三角面（%.1f MB）", m.Vertices(), m.Triangles(), float64(len(mb))/1e6)
	var texs [][]byte
	var imgs []image.Image
	for _, p := range files.Textures {
		b, err := os.ReadFile(p)
		if err != nil {
			im.fail(j, err)
			return
		}
		texs = append(texs, b)
		ti, err := jpeg.Decode(bytes.NewReader(b))
		if err != nil {
			ti = nil
		}
		imgs = append(imgs, ti)
	}
	var layout []LayoutRoom
	if files.Layout != "" {
		if b, err := os.ReadFile(files.Layout); err == nil {
			layout, _ = ParseLayout(b)
		}
	}
	if len(layout) > 0 {
		im.step(j, "房间名来自 VR 里的房间标注（%d 个拍摄点有名字）", len(layout))
	}

	var ref *Reference
	if len(j.img) > 0 {
		pic, _, err := image.Decode(bytes.NewReader(j.img))
		switch {
		case err != nil:
			im.step(j, "户型图读不了（要 JPG 或 PNG），只用扫描")
		case im.readPlan == nil:
			im.step(j, "没有配置看图的模型，户型图只用来对照")
		default:
			im.step(j, "让模型读户型图：房间、尺寸、门窗（要半分钟到一分钟）")
			d, err := im.readPlan(ctx, pic)
			if err != nil {
				im.step(j, "户型图没读成（%v），只用扫描", err)
				break
			}
			ref = &Reference{Home: d.Home, Printed: map[string]float64{}}
			for _, a := range d.Areas {
				ref.Printed[a.Name] = a.Printed
			}
			im.step(j, "户型图读到 %d 个房间、%d 扇门、%d 扇窗", len(d.Home.Rooms), len(d.Home.Doors), len(d.Home.Windows))
		}
	}

	turns := 0
	if ref != nil {
		var hints []RoomHint
		for _, r := range ref.Home.Rooms {
			hints = append(hints, RoomHint{Name: r.Name, Kind: r.Kind, Poly: r.Poly})
		}
		im.step(j, "对着户型图找朝向")
		if turns, err = ChooseTurns(m, hints); err != nil {
			im.fail(j, err)
			return
		}
		im.step(j, "扫描转 %d° 后和户型图一致（上北）", turns*90)
	} else {
		im.step(j, "扫描不知道哪边是北：按拍摄方向摆，贴上户型图能对正")
	}

	im.step(j, "切墙找房间、门窗、地台和台阶")
	id := "scan-" + j.ID
	res, glb, err := Import(m, w, layout, texs, turns, Options{ID: id, Name: homeName(w), Reference: ref, Textures: imgs})
	if err != nil {
		im.fail(j, err)
		return
	}
	h := res.Home
	im.step(j, "找到 %d 个房间、%d 面墙、%d 扇门、%d 扇窗、%d 处地台台阶，层高 %.2f m", len(h.Rooms), len(h.Walls), len(h.Doors), len(h.Windows), len(h.Levels), res.Frame.Ceiling)
	if err := os.WriteFile(filepath.Join(dir, "model.glb"), glb, 0o644); err != nil {
		im.fail(j, err)
		return
	}
	v := TopView(m, res.Frame, imgs, h.CutHeight, 0.02)
	var top bytes.Buffer
	if err := png.Encode(&top, v.Img); err == nil {
		os.WriteFile(filepath.Join(dir, "top.png"), top.Bytes(), 0o644)
	}
	if out, err := json.MarshalIndent(res, "", "  "); err == nil {
		os.WriteFile(filepath.Join(dir, "result.json"), out, 0o644)
	}
	im.mu.Lock()
	j.Result, j.Status = &res, "done"
	j.View = &ViewInfo{X0: v.X0, Z0: v.Z0, Cell: v.Cell, W: v.Img.Bounds().Dx(), H: v.Img.Bounds().Dy()}
	im.mu.Unlock()
	im.log.Info("scan import", "job", j.ID, "rooms", len(h.Rooms))
}

// homeName is 我的家 · 3室1厅 (from the listing's counts, else the rooms found).
func homeName(w Work) string {
	var hl struct {
		Bed    int `json:"bedroom_amount"`
		Parlor int `json:"parlor_amount"`
	}
	if json.Unmarshal([]byte(w.Layout), &hl) == nil && hl.Bed > 0 {
		return fmt.Sprintf("我的家 · %d室%d厅", hl.Bed, max(hl.Parlor, 1))
	}
	return "我的家 · VR 导入"
}

func (im *Importer) job(id string) (*Job, bool) {
	im.mu.Lock()
	defer im.mu.Unlock()
	j, ok := im.jobs[id]
	return j, ok
}

func (im *Importer) snapshot(j *Job) Job {
	im.mu.Lock()
	defer im.mu.Unlock()
	out := *j
	out.Steps = append([]floorplan.Step{}, j.Steps...)
	return out
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// modelPath is where a confirmed home's scan model is kept.
func (im *Importer) modelPath(homeID string) string {
	return filepath.Join(im.dir, "homes", homeID, "model.glb")
}

var idRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// Handler serves VR imports under /api/scan:
//
//	POST /api/scan                    multipart "link" (the pasted share text), optional "image"
//	GET  /api/scan/{id}               the job: steps, then the result
//	GET  /api/scan/{id}/top.png       the scan from above, for the review
//	GET  /api/scan/{id}/image         the floor-plan picture that came with it
//	POST /api/scan/{id}/confirm       make the result the home (the server restarts)
//	GET  /api/scan/model/{home}/scan.glb   a confirmed home's scan model
func (im *Importer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/scan", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 20<<20)
		if err := r.ParseMultipartForm(20 << 20); err != nil {
			fail(w, 400, "没收到内容（户型图最大 20 MB）")
			return
		}
		var img []byte
		if f, _, err := r.FormFile("image"); err == nil {
			img, _ = io.ReadAll(f)
			f.Close()
		}
		j, err := im.Start(r.FormValue("link"), img)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, im.snapshot(j))
	})
	mux.HandleFunc("GET /api/scan/model/{home}/scan.glb", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("home")
		if !idRe.MatchString(id) {
			fail(w, 404, "没有这个模型")
			return
		}
		w.Header().Set("Content-Type", "model/gltf-binary")
		w.Header().Set("Cache-Control", "private, max-age=300")
		http.ServeFile(w, r, im.modelPath(id))
	})
	mux.HandleFunc("GET /api/scan/{id}", func(w http.ResponseWriter, r *http.Request) {
		j, ok := im.job(r.PathValue("id"))
		if !ok {
			fail(w, 404, "没有这次导入（服务重启过的话要重新贴链接）")
			return
		}
		writeJSON(w, 200, im.snapshot(j))
	})
	mux.HandleFunc("GET /api/scan/{id}/top.png", func(w http.ResponseWriter, r *http.Request) {
		j, ok := im.job(r.PathValue("id"))
		if !ok {
			fail(w, 404, "没有这次导入")
			return
		}
		w.Header().Set("Content-Type", "image/png")
		http.ServeFile(w, r, filepath.Join(im.jobDir(j.ID), "top.png"))
	})
	mux.HandleFunc("GET /api/scan/{id}/image", func(w http.ResponseWriter, r *http.Request) {
		j, ok := im.job(r.PathValue("id"))
		if !ok || len(j.img) == 0 {
			fail(w, 404, "没有户型图")
			return
		}
		w.Header().Set("Content-Type", http.DetectContentType(j.img))
		w.Write(j.img)
	})
	mux.HandleFunc("POST /api/scan/{id}/confirm", func(w http.ResponseWriter, r *http.Request) {
		j, ok := im.job(r.PathValue("id"))
		if !ok {
			fail(w, 404, "没有这次导入")
			return
		}
		im.mu.Lock()
		res := j.Result
		im.mu.Unlock()
		if res == nil {
			fail(w, 409, "还没做完")
			return
		}
		h := res.Home
		if errs := h.Check(); len(errs) > 0 {
			fail(w, 422, "这个户型还不能用："+strings.Join(errs, "；"))
			return
		}
		if err := os.MkdirAll(filepath.Dir(im.modelPath(h.ID)), 0o755); err != nil {
			fail(w, 500, err.Error())
			return
		}
		if err := copyFile(filepath.Join(im.jobDir(j.ID), "model.glb"), im.modelPath(h.ID)); err != nil {
			fail(w, 500, "模型没存下："+err.Error())
			return
		}
		h.Scan = "/api/scan/model/" + h.ID + "/scan.glb"
		writeJSON(w, 200, map[string]any{"ok": true, "home": map[string]string{"id": h.ID, "name": h.Name}})
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
		if err := im.use(h); err != nil {
			im.log.Error("scan confirm", "err", err)
		}
	})
	return mux
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

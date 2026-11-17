package floorplan

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/domain"

	"hometwin/internal/home"
)

// Importer runs imports: a picture in, a draft home out, and the draft made the home when a
// person confirms it.
type Importer struct {
	dir   string
	llm   domain.Generator
	fixer *Fixer
	log   *slog.Logger
	// current is the home in use; use makes h the home in use (the server restarts on it).
	current func() home.Home
	use     func(h home.Home) error
	// read is the model's reading of a picture (tests replay a recorded one).
	read func(ctx context.Context, img image.Image) (Reading, string, error)

	mu   sync.Mutex
	jobs map[string]*Job
}

// Job is one import, as the page follows it.
type Job struct {
	ID      string `json:"id"`
	Status  string `json:"status"` // running, done, failed
	Steps   []Step `json:"steps"`
	Error   string `json:"error,omitempty"`
	Draft   *Draft `json:"draft,omitempty"`
	Created int64  `json:"created"`

	data []byte
	mime string
}

type Step struct {
	T    int64  `json:"t"` // unix ms
	Text string `json:"text"`
}

// NewImporter keeps uploads and drafts in dir. llm reads the pictures and runs the fixer;
// without one imports are refused.
func NewImporter(dir string, llm domain.Generator, current func() home.Home, use func(home.Home) error, log *slog.Logger) (*Importer, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if log == nil {
		log = slog.Default()
	}
	im := &Importer{dir: dir, llm: llm, log: log, current: current, use: use, jobs: map[string]*Job{}}
	if llm != nil {
		im.fixer = NewFixer(llm, llm, dir)
		im.read = func(ctx context.Context, img image.Image) (Reading, string, error) { return Read(ctx, llm, img) }
	}
	return im, nil
}

func (im *Importer) Close() error {
	if im.fixer != nil {
		return im.fixer.Close()
	}
	return nil
}

// ErrNoModel is said when no model is configured.
var ErrNoModel = errors.New("还没有配置看图的模型：设置 LLM_BASE_URL、LLM_API_KEY、LLM_MODEL 后重启")

func newID() string {
	b := make([]byte, 5)
	rand.Read(b)
	return time.Now().Format("0102") + "-" + hex.EncodeToString(b)
}

// Start begins an import of a picture and returns its job straight away.
func (im *Importer) Start(data []byte, mime string) (*Job, error) {
	if im.read == nil {
		return nil, ErrNoModel
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("读不了这张图片，要 JPG 或 PNG")
	}
	j := &Job{ID: newID(), Status: "running", Steps: []Step{}, Created: time.Now().UnixMilli(), data: data, mime: mime}
	im.mu.Lock()
	im.jobs[j.ID] = j
	im.mu.Unlock()
	dir := filepath.Join(im.dir, j.ID)
	os.MkdirAll(dir, 0o755)
	ext := ".jpg"
	if strings.Contains(mime, "png") {
		ext = ".png"
	}
	os.WriteFile(filepath.Join(dir, "upload"+ext), data, 0o644)
	go im.run(j, img)
	return j, nil
}

func (im *Importer) step(j *Job, format string, a ...any) {
	im.mu.Lock()
	defer im.mu.Unlock()
	j.Steps = append(j.Steps, Step{T: time.Now().UnixMilli(), Text: fmt.Sprintf(format, a...)})
}

func (im *Importer) fail(j *Job, err error) {
	im.log.Info("floorplan import", "job", j.ID, "err", err)
	im.mu.Lock()
	defer im.mu.Unlock()
	j.Status, j.Error = "failed", err.Error()
}

func (im *Importer) run(j *Job, img image.Image) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	b := img.Bounds()
	im.step(j, "收到图片 %d×%d", b.Dx(), b.Dy())
	rect := Drawing(img)
	if rect != b {
		im.step(j, "裁掉图片四周不是户型图的部分，剩下 %d×%d", rect.Dx(), rect.Dy())
	}
	plan := crop(img, rect)
	im.step(j, "让模型读图：尺寸、房间、墙、门窗（要半分钟到一分钟）")
	rd, raw, err := im.read(ctx, plan)
	if raw != "" {
		os.WriteFile(filepath.Join(im.dir, j.ID, "reading.json"), []byte(raw), 0o644)
	}
	if err != nil {
		im.fail(j, err)
		return
	}
	im.step(j, "读到 %d 个房间、%d 面墙、%d 扇门、%d 扇窗", len(rd.Rooms), len(rd.Walls), len(rd.Doors), len(rd.Windows))
	p, err := Build(rd, plan)
	if err != nil {
		im.fail(j, err)
		return
	}
	found := 0
	for _, l := range append(append([]gridLine{}, p.x.lines...), p.z.lines...) {
		if l.chain {
			found++
		}
	}
	im.step(j, "按尺寸标注定了比例：每像素 %.1f mm，%d 条尺寸线", (p.x.mmPerPx+p.z.mmPerPx)/2, found)
	id, name := "plan-"+j.ID, homeName(p)
	d := p.Draft(id, name)
	im.step(j, "检查：%s", tally(d))
	if len(warnings(d)) > 0 && im.fixer != nil {
		im.step(j, "交给校对 agent 逐条核对")
		fixed, err := im.fixer.Fix(ctx, p, plan, id, name, 2, func(s string) { im.step(j, "%s", s) })
		if err != nil && ctx.Err() == nil {
			im.step(j, "校对没有做完：%v", err)
		}
		d = fixed
		im.step(j, "校对后：%s", tally(d))
	}
	d.Frame.Crop = [4]int{rect.Min.X - b.Min.X, rect.Min.Y - b.Min.Y, rect.Max.X - b.Min.X, rect.Max.Y - b.Min.Y}
	d.Frame.Size = [2]int{b.Dx(), b.Dy()}
	if out, err := json.MarshalIndent(d, "", "  "); err == nil {
		os.WriteFile(filepath.Join(im.dir, j.ID, "draft.json"), out, 0o644)
	}
	im.mu.Lock()
	j.Draft, j.Status = &d, "done"
	im.mu.Unlock()
}

func tally(d Draft) string {
	w := len(warnings(d))
	if w == 0 {
		return fmt.Sprintf("%d 个房间、%d 面墙、%d 扇门、%d 扇窗，没发现问题", len(d.Home.Rooms), len(d.Home.Walls), len(d.Home.Doors), len(d.Home.Windows))
	}
	return fmt.Sprintf("%d 个房间、%d 面墙、%d 扇门、%d 扇窗，还有 %d 个问题", len(d.Home.Rooms), len(d.Home.Walls), len(d.Home.Doors), len(d.Home.Windows), w)
}

// homeName is 导入户型 · 3室1厅 103㎡.
func homeName(p *Plan) string {
	beds, halls, total := 0, 0, 0.0
	for _, r := range p.Rooms {
		switch r.Kind {
		case "bedroom":
			beds++
		case "living":
			halls++
		}
		total += r.Printed
	}
	if total == 0 {
		for _, r := range p.Rooms {
			total += home.Area(r.Poly) / 1e6
		}
	}
	return fmt.Sprintf("导入户型 · %d室%d厅 %.0f㎡", beds, max(halls, 1), total)
}

func (im *Importer) job(id string) (*Job, bool) {
	im.mu.Lock()
	defer im.mu.Unlock()
	j, ok := im.jobs[id]
	return j, ok
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// Handler serves imports under /api/floorplan:
//
//	GET  /api/floorplan                  configured, the home in use, whether it is the sample
//	POST /api/floorplan                  multipart "image" → the job
//	GET  /api/floorplan/{id}             the job: steps, then the draft
//	GET  /api/floorplan/{id}/image       the uploaded picture
//	POST /api/floorplan/{id}/retry       read the same picture again → a new job
//	POST /api/floorplan/{id}/confirm     make the draft the home (the server restarts)
//	POST /api/floorplan/sample           go back to the sample home
func (im *Importer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/floorplan", func(w http.ResponseWriter, r *http.Request) {
		h := im.current()
		out := map[string]any{"configured": im.read != nil, "home": map[string]string{"id": h.ID, "name": h.Name}, "sample": h.ID == home.Sample().ID}
		if im.read == nil {
			out["notice"] = ErrNoModel.Error()
		}
		writeJSON(w, 200, out)
	})
	mux.HandleFunc("POST /api/floorplan", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 20<<20)
		f, hdr, err := r.FormFile("image")
		if err != nil {
			fail(w, 400, "没有收到图片（最大 20 MB）")
			return
		}
		defer f.Close()
		data, err := io.ReadAll(f)
		if err != nil {
			fail(w, 400, "图片没传完")
			return
		}
		j, err := im.Start(data, hdr.Header.Get("Content-Type"))
		if errors.Is(err, ErrNoModel) {
			fail(w, 503, err.Error())
			return
		}
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, im.snapshot(j))
	})
	mux.HandleFunc("GET /api/floorplan/{id}", func(w http.ResponseWriter, r *http.Request) {
		j, ok := im.job(r.PathValue("id"))
		if !ok {
			fail(w, 404, "没有这次导入（服务重启过的话要重新上传）")
			return
		}
		writeJSON(w, 200, im.snapshot(j))
	})
	mux.HandleFunc("GET /api/floorplan/{id}/image", func(w http.ResponseWriter, r *http.Request) {
		j, ok := im.job(r.PathValue("id"))
		if !ok {
			fail(w, 404, "没有这次导入")
			return
		}
		mime := j.mime
		if mime == "" {
			mime = http.DetectContentType(j.data)
		}
		w.Header().Set("Content-Type", mime)
		w.Header().Set("Cache-Control", "private, max-age=3600")
		w.Write(j.data)
	})
	mux.HandleFunc("POST /api/floorplan/{id}/retry", func(w http.ResponseWriter, r *http.Request) {
		j, ok := im.job(r.PathValue("id"))
		if !ok {
			fail(w, 404, "没有这次导入")
			return
		}
		nj, err := im.Start(j.data, j.mime)
		if err != nil {
			fail(w, 503, err.Error())
			return
		}
		writeJSON(w, 200, im.snapshot(nj))
	})
	mux.HandleFunc("POST /api/floorplan/{id}/confirm", func(w http.ResponseWriter, r *http.Request) {
		j, ok := im.job(r.PathValue("id"))
		if !ok {
			fail(w, 404, "没有这次导入")
			return
		}
		im.mu.Lock()
		d := j.Draft
		im.mu.Unlock()
		if d == nil {
			fail(w, 409, "还没识别完")
			return
		}
		if errs := d.Home.Check(); len(errs) > 0 {
			fail(w, 422, "这个户型还不能用："+strings.Join(errs, "；"))
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "home": map[string]string{"id": d.Home.ID, "name": d.Home.Name}})
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
		if err := im.use(d.Home); err != nil {
			im.log.Error("floorplan confirm", "err", err)
		}
	})
	mux.HandleFunc("POST /api/floorplan/sample", func(w http.ResponseWriter, r *http.Request) {
		s := home.Sample()
		writeJSON(w, 200, map[string]any{"ok": true, "home": map[string]string{"id": s.ID, "name": s.Name}})
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
		if err := im.use(s); err != nil {
			im.log.Error("floorplan sample", "err", err)
		}
	})
	return mux
}

func (im *Importer) snapshot(j *Job) Job {
	im.mu.Lock()
	defer im.mu.Unlock()
	out := *j
	out.Steps = append([]Step{}, j.Steps...)
	return out
}

// ReadDraft reads a floor-plan picture into a draft in one go, without the proofreading
// agent: what another import (a VR scan) uses to name rooms and fill in what it missed.
// raw is the model's answer, kept for replaying.
func ReadDraft(ctx context.Context, llm domain.Generator, img image.Image, id, name string) (Draft, string, error) {
	if llm == nil {
		return Draft{}, "", ErrNoModel
	}
	rect := Drawing(img)
	plan := crop(img, rect)
	rd, raw, err := Read(ctx, llm, plan)
	if err != nil {
		return Draft{}, raw, err
	}
	p, err := Build(rd, plan)
	if err != nil {
		return Draft{}, raw, err
	}
	d := p.Draft(id, name)
	b := img.Bounds()
	d.Frame.Crop = [4]int{rect.Min.X - b.Min.X, rect.Min.Y - b.Min.Y, rect.Max.X - b.Min.X, rect.Max.Y - b.Min.Y}
	d.Frame.Size = [2]int{b.Dx(), b.Dy()}
	return d, raw, nil
}

// Package catalog finds 3D furniture models in Polyfork's catalogue (https://polyfork.dev) from
// a sentence such as 「卧室A 放一个床头柜」, and turns the one picked into a product the home can
// place. A picked model is downloaded once and kept under the data directory; the free low poly
// meshes are CC BY 4.0, so each product carries its credit line.
package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/domain"

	"hometwin/internal/home"
	"hometwin/internal/ikea"
)

// Base is Polyfork's address.
const Base = "https://polyfork.dev"

// Catalog searches the catalogue and keeps the models picked from it.
type Catalog struct {
	Dir  string           // where picked models are kept
	HTTP *http.Client     // nil: a client that honours HTTPS_PROXY
	LLM  domain.Generator // reads a sentence into keywords and a room; nil: simple matching
	Base string           // empty: Base
}

// New keeps models in dir.
func New(dir string, llm domain.Generator) *Catalog {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = http.ProxyFromEnvironment
	return &Catalog{Dir: dir, HTTP: &http.Client{Transport: tr, Timeout: 60 * time.Second}, LLM: llm, Base: Base}
}

// Hit is one model in a search's results. Sizes are centimetres.
type Hit struct {
	ID        string  `json:"id"`
	Title     string  `json:"title"`
	Image     string  `json:"image"`
	Page      string  `json:"page"`
	Kit       string  `json:"kit,omitempty"`
	Triangles int     `json:"triangles"`
	W         float64 `json:"w"`
	D         float64 `json:"d"`
	H         float64 `json:"h"`
	Type      string  `json:"type"`
	TypeLabel string  `json:"typeLabel"`
}

// Intent is what a sentence asks for: English keywords for the search, and the room it names.
type Intent struct {
	Query string `json:"query"`
	Room  string `json:"room,omitempty"` // a room id of the home, or empty
}

type asset struct {
	ID        string             `json:"id"`
	Title     string             `json:"title"`
	Kit       string             `json:"kit"`
	Free      bool               `json:"free"`
	Triangles int                `json:"triangles"`
	Page      string             `json:"page"`
	Thumbnail string             `json:"thumbnail"`
	Size      map[string]float64 `json:"size_m"`
	Credit    string             `json:"credit"`
	Class     string             `json:"class"`
	Download  struct {
		GLB string `json:"glb"`
	} `json:"download"`
}

func (c *Catalog) base() string {
	if c.Base != "" {
		return strings.TrimRight(c.Base, "/")
	}
	return Base
}

func (c *Catalog) get(ctx context.Context, u string, into any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("连不上 Polyfork：%w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("Polyfork 回了 %d", res.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(into)
}

// kind reads the twin's furniture kind from a model's title.
func kind(title string) string {
	t := strings.ToLower(title)
	switch {
	case strings.Contains(t, "media unit"), strings.Contains(t, "tv unit"), strings.Contains(t, "tv bench"):
		return "tvbench"
	case strings.Contains(t, "bedside"), strings.Contains(t, "nightstand"):
		return "drawers"
	}
	return ikea.DetectType(title)
}

func (a asset) hit() Hit {
	t := kind(a.Title)
	return Hit{
		ID: a.ID, Title: a.Title, Image: a.Thumbnail, Page: a.Page, Kit: a.Kit, Triangles: a.Triangles,
		W: cm(a.Size["x"]), D: cm(a.Size["z"]), H: cm(a.Size["y"]), Type: t, TypeLabel: ikea.TypeLabel(t),
	}
}

func cm(m float64) float64 { return math.Round(m*1000) / 10 }

// Search finds free models for English keywords, best first.
func (c *Catalog) Search(ctx context.Context, query string, n int) ([]Hit, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("要找什么？")
	}
	if n <= 0 || n > 24 {
		n = 12
	}
	var r struct {
		Assets []asset `json:"assets"`
	}
	if err := c.get(ctx, c.base()+"/api/assets?free=1&q="+url.QueryEscape(query), &r); err != nil {
		return nil, err
	}
	// the catalogue's search is loose ("bedside table" brings casino tables): keep the models
	// whose title has the most of the words, in the catalogue's order; a word like "plant" in
	// "houseplant" counts too
	words := strings.Fields(strings.ToLower(query))
	byScore := map[int][]Hit{}
	best := 0
	for _, a := range r.Assets {
		if !a.Free || a.Size["x"] <= 0 || a.Size["y"] <= 0 || a.Size["z"] <= 0 {
			continue
		}
		title := strings.ToLower(a.Title)
		k := 0
		for _, w := range words {
			if strings.Contains(title, strings.TrimSuffix(w, "s")) {
				k++
			}
		}
		byScore[k] = append(byScore[k], a.hit())
		best = max(best, k)
	}
	out := byScore[best]
	if out == nil {
		out = []Hit{}
	}
	if len(out) > n {
		out = out[:n]
	}
	return out, nil
}

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{2,80}$`)

// ValidID says whether s looks like a catalogue id (so it is safe in a path).
func ValidID(s string) bool { return idRe.MatchString(s) }

// File is where a picked model is kept.
func (c *Catalog) File(id string) string { return filepath.Join(c.Dir, id+".glb") }

// ModelURL is where the page loads a picked model from.
func ModelURL(id string) string { return "/api/catalog/model/" + id + ".glb" }

// Product downloads a model (once) and describes it as a product of its real size.
func (c *Catalog) Product(ctx context.Context, id string) (ikea.Product, error) {
	if !ValidID(id) {
		return ikea.Product{}, errors.New("模型编号不对")
	}
	var a asset
	if err := c.get(ctx, c.base()+"/api/assets/"+id, &a); err != nil {
		return ikea.Product{}, err
	}
	if !a.Free {
		return ikea.Product{}, errors.New("这个模型不是免费的")
	}
	if err := c.fetch(ctx, id, a.Download.GLB); err != nil {
		return ikea.Product{}, err
	}
	h := a.hit()
	credit := a.Credit
	if credit == "" {
		credit = fmt.Sprintf("%s by Polyfork (%s), CC BY 4.0", a.Title, a.Page)
	}
	return ikea.Product{
		Source: "polyfork", URL: a.Page, Name: a.Title, Type: h.Type, TypeLabel: h.TypeLabel,
		ColorHex: "#e9e7e1", Image: a.Thumbnail, W: h.W, D: h.D, H: h.H, DimsFrom: "catalog",
		Model: ModelURL(id), Credit: credit,
	}, nil
}

func (c *Catalog) fetch(ctx context.Context, id, src string) error {
	dst := c.File(id)
	if st, err := os.Stat(dst); err == nil && st.Size() > 0 {
		return nil
	}
	if src == "" {
		src = c.base() + "/cdn/" + id + ".glb"
	}
	req, err := http.NewRequestWithContext(ctx, "GET", src, nil)
	if err != nil {
		return err
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("模型没下载下来：%w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("模型没下载下来（%d）", res.StatusCode)
	}
	if err := os.MkdirAll(c.Dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(c.Dir, id+".*.part")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	head := make([]byte, 4)
	n, _ := io.ReadFull(res.Body, head)
	if n < 4 || string(head) != "glTF" {
		tmp.Close()
		return errors.New("下载到的不是 GLB 模型")
	}
	if _, err := tmp.Write(head); err != nil {
		tmp.Close()
		return err
	}
	if _, err := io.Copy(tmp, io.LimitReader(res.Body, 64<<20)); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

// words the catalogue knows, for sentences read without a model
var english = map[string]string{
	"sofa": "sofa", "armchair": "armchair", "bed": "bed", "bookcase": "bookcase", "wardrobe": "wardrobe",
	"drawers": "chest of drawers", "desk": "desk", "table": "table", "chair": "chair", "tvbench": "media unit",
	"shelf": "shelf", "lamp": "lamp", "rug": "rug",
}

var extra = []struct {
	re *regexp.Regexp
	q  string
}{
	{regexp.MustCompile(`绿植|盆栽|植物`), "houseplant"},
	{regexp.MustCompile(`床头柜`), "bedside table"},
	{regexp.MustCompile(`茶几`), "coffee table"},
	{regexp.MustCompile(`餐桌`), "dining table"},
	{regexp.MustCompile(`餐椅`), "dining chair"},
	{regexp.MustCompile(`电视机|电视(?:[^柜]|$)`), "television"},
	{regexp.MustCompile(`冰箱`), "fridge"},
	{regexp.MustCompile(`镜子`), "mirror"},
	{regexp.MustCompile(`钢琴`), "piano"},
	{regexp.MustCompile(`挂画|装饰画`), "wall art"},
	{regexp.MustCompile(`靠垫|抱枕`), "cushion"},
	{regexp.MustCompile(`收纳筐|篮子`), "basket"},
}

// roomIn finds the room a sentence names, longest name first so 卧室A is not taken for 卧室.
func roomIn(text string, rooms []home.Room) string {
	best, n := "", 0
	for _, r := range rooms {
		if r.Name != "" && strings.Contains(text, r.Name) && len([]rune(r.Name)) > n {
			best, n = r.ID, len([]rune(r.Name))
		}
	}
	return best
}

func ascii(s string) bool {
	for _, r := range s {
		if r > 127 {
			return false
		}
	}
	return true
}

// Understand reads a sentence into keywords and a room. With a model it reads any wording;
// without one it knows the common kinds of furniture and the home's room names.
func (c *Catalog) Understand(ctx context.Context, text string, rooms []home.Room) (Intent, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Intent{}, errors.New("说说要放什么，比如：卧室A 放一个床头柜")
	}
	in := Intent{Room: roomIn(text, rooms)}
	if c.LLM != nil {
		if got, err := c.ask(ctx, text, rooms); err == nil && got.Query != "" {
			if got.Room == "" || !hasRoom(rooms, got.Room) {
				got.Room = in.Room
			}
			return got, nil
		}
	}
	if ascii(text) {
		in.Query = text
		return in, nil
	}
	for _, e := range extra {
		if e.re.MatchString(text) {
			in.Query = e.q
			return in, nil
		}
	}
	if t := ikea.DetectType(text); t != "box" {
		in.Query = english[t]
		return in, nil
	}
	return in, errors.New("没听懂要放什么。换个说法，或者直接写英文，比如 piano")
}

func hasRoom(rooms []home.Room, id string) bool {
	for _, r := range rooms {
		if r.ID == id {
			return true
		}
	}
	return false
}

var jsonRe = regexp.MustCompile(`(?s)\{.*\}`)

func (c *Catalog) ask(ctx context.Context, text string, rooms []home.Room) (Intent, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var b strings.Builder
	for _, r := range rooms {
		fmt.Fprintf(&b, "- %s: %s\n", r.ID, r.Name)
	}
	prompt := fmt.Sprintf(`家里人说了一句要往家里放什么东西。把它变成在英文 3D 模型库里搜索用的关键词，并找出说的是哪个房间。

房间（id: 名字）：
%s
这句话：%s

只回一个 JSON，不要别的：{"query": "1 到 3 个英文单词，说清是什么东西，比如 bedside table、upright piano、floor lamp", "room": "房间 id，没说房间就是空串"}`, b.String(), text)
	out, err := c.LLM.Generate(ctx, prompt, &domain.GenerationOptions{MaxTokens: 200})
	if err != nil {
		return Intent{}, err
	}
	var in Intent
	if err := json.Unmarshal([]byte(jsonRe.FindString(out)), &in); err != nil {
		return Intent{}, err
	}
	in.Query = strings.TrimSpace(in.Query)
	return in, nil
}

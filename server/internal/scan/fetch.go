package scan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"
	maxPage   = 8 << 20
	// MaxDownload caps one Download; a flat's model and textures are about 2.5 MB.
	MaxDownload = 64 << 20
)

// Work is what a share page says about its scan (its firstscreen.defaultWork), plus the
// address of the per-room layout the page's ruler uses, which carries the room names.
type Work struct {
	Code   string `json:"code"`
	Layout string `json:"house_layout"` // JSON text: bedroom_amount, parlor_amount, …
	Model  struct {
		FileURL  string   `json:"file_url"`
		Textures []string `json:"material_textures"`
	} `json:"model"`
	Observers []Observer `json:"observers"`
	Panorama  struct {
		Count int `json:"count"`
	} `json:"panorama"`
	FloorPlans []struct {
		URL    string `json:"url"`
		SVGURL string `json:"svg_url,omitempty"`
	} `json:"outline_floor_plan"`
	HierarchyPlans []struct {
		URL string `json:"url"`
	} `json:"hierarchy_floor_plan"`
	// RulerURL is the room layout (room_layout.json), empty when the page has none.
	RulerURL string `json:"ruler_url,omitempty"`
	// WindowLabels mark windows: where the viewer pins its "view out of the window" labels.
	WindowLabels []WindowLabel `json:"window_labels,omitempty"`
}

// WindowLabel is a small box on a window, seen from a panorama. Its corners are in the
// model's frame with y and z swapped: (x, z up, y).
type WindowLabel struct {
	Pano int          `json:"index"`
	Box  [][3]float64 `json:"box_points"`
}

// Observer is one place the scanner stood. Positions are in the viewer's frame: x as the
// model's x, y up, z as the model's −y.
type Observer struct {
	Index    int        `json:"index"`
	Floor    int        `json:"floor_index"`
	Position [3]float64 `json:"position"`
	Standing [3]float64 `json:"standing_position"`
}

// LayoutRoom is one entry of room_layout.json: the room a panorama was taken in.
type LayoutRoom struct {
	Pano int    `json:"panoIndex"`
	Name string `json:"roomName"`
}

// Fetch reads a share page and the scan description in it. client nil means a client that
// goes through HTTPS_PROXY when set.
func Fetch(ctx context.Context, shareURL string, client *http.Client) (Work, error) {
	u, err := url.Parse(strings.TrimSpace(shareURL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return Work{}, errors.New("链接不对：要以 http:// 或 https:// 开头的 VR 分享链接")
	}
	body, err := get(ctx, client, u.String(), maxPage, "text/html,application/xhtml+xml;q=0.9,*/*;q=0.8")
	if err != nil {
		return Work{}, fmt.Errorf("VR 页面打不开：%w", err)
	}
	return ParsePage(body)
}

var stateRe = regexp.MustCompile(`(?s)<script[^>]*>\s*<!--\s*(\{.*?\})\s*-->\s*</script>`)

// ParsePage finds the first-screen state in a share page: JSON inside an HTML comment in a
// <script> tag, holding firstscreen.defaultWork.
func ParsePage(page []byte) (Work, error) {
	for _, m := range stateRe.FindAllSubmatch(page, -1) {
		if !strings.Contains(string(m[1]), `"defaultWork"`) {
			continue
		}
		var st struct {
			First struct {
				Work         json.RawMessage `json:"defaultWork"`
				ObjectsInURL struct {
					Ruler string `json:"ruler"`
				} `json:"objectsInUrl"`
				Objects struct {
					Windows struct {
						Labels []WindowLabel `json:"label_positions"`
					} `json:"mrWindows"`
				} `json:"objects"`
			} `json:"firstscreen"`
		}
		if err := json.Unmarshal(m[1], &st); err != nil || len(st.First.Work) == 0 {
			continue
		}
		var w Work
		if err := json.Unmarshal(st.First.Work, &w); err != nil {
			return Work{}, fmt.Errorf("VR 页面里的模型信息读不懂：%v", err)
		}
		w.RulerURL = st.First.ObjectsInURL.Ruler
		w.WindowLabels = st.First.Objects.Windows.Labels
		if w.Model.FileURL == "" {
			return Work{}, errors.New("VR 页面里没有三维模型（可能已下线）")
		}
		return w, nil
	}
	return Work{}, errors.New("VR 页面里找不到模型信息（链接失效或页面改版了）")
}

// Files are what Download saved, as paths.
type Files struct {
	Model    string
	Textures []string
	Layout   string // room_layout.json, empty if the page had none or it failed
}

// Download saves the model, its textures and the room layout into dir, at most limit bytes
// in all (MaxDownload when 0). Only the scan's own CDN files are fetched.
func Download(ctx context.Context, w Work, dir string, client *http.Client, limit int64) (Files, error) {
	if limit <= 0 {
		limit = MaxDownload
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Files{}, err
	}
	var f Files
	left := limit
	save := func(raw, accept string) (string, error) {
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return "", fmt.Errorf("文件地址不对：%s", path.Base(raw))
		}
		name := path.Base(u.Path)
		if name == "" || name == "." || name == "/" || strings.ContainsAny(name, `\:`) {
			return "", fmt.Errorf("文件名不对：%q", name)
		}
		b, err := get(ctx, client, u.String(), left, accept)
		if err != nil {
			return "", fmt.Errorf("下载 %s 失败：%w", name, err)
		}
		left -= int64(len(b))
		p := filepath.Join(dir, name)
		return p, os.WriteFile(p, b, 0o644)
	}
	var err error
	if f.Model, err = save(w.Model.FileURL, "*/*"); err != nil {
		return f, err
	}
	for _, t := range w.Model.Textures {
		p, err := save(t, "image/jpeg,image/*")
		if err != nil {
			return f, err
		}
		f.Textures = append(f.Textures, p)
	}
	if w.RulerURL != "" {
		// The names are a convenience; the import works without them.
		if p, err := save(w.RulerURL, "application/json"); err == nil {
			f.Layout = p
		}
	}
	return f, nil
}

// ParseLayout reads room_layout.json.
func ParseLayout(b []byte) ([]LayoutRoom, error) {
	var rs []LayoutRoom
	if err := json.Unmarshal(b, &rs); err != nil {
		return nil, fmt.Errorf("房间布局读不懂：%v", err)
	}
	return rs, nil
}

func get(ctx context.Context, client *http.Client, u string, limit int64, accept string) ([]byte, error) {
	if client == nil {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.Proxy = http.ProxyFromEnvironment
		client = &http.Client{Transport: tr, Timeout: 60 * time.Second}
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", accept)
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	resp, err := client.Do(req)
	if err != nil {
		// The error text carries the address, which for the share page holds its tokens.
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, errors.New("超时，网络可能需要代理（设置 HTTPS_PROXY）")
		}
		var ue *url.Error
		if errors.As(err, &ue) {
			return nil, ue.Err
		}
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, errors.New("文件太大")
	}
	return b, nil
}

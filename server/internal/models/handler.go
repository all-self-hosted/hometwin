package models

import (
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"

	"hometwin/internal/ikea"
)

// Answer is what POST /api/models says about a product's model.
type Answer struct {
	Key string `json:"key"`
	URL string `json:"url"`
	// Ready: the GLB can be fetched now. Otherwise it is being built; ask again.
	Ready bool `json:"ready"`
	// Available: Blender is configured. When false the browser draws its own model.
	Available bool   `json:"available"`
	Error     string `json:"error,omitempty"`
	Spec      Spec   `json:"spec"`
}

var keyRe = regexp.MustCompile(`^[a-z]+-[0-9a-f]{16}$`)

// Handler serves POST /api/models (a product, or {"spec": …}) and GET /api/models/<key>.glb.
// Register it on both "/api/models" and "/api/models/".
func (b *Builder) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/api/models"), "/")
		switch {
		case name == "" && r.Method == http.MethodPost:
			b.answer(w, r)
		case name != "" && (r.Method == http.MethodGet || r.Method == http.MethodHead):
			b.serve(w, r, strings.TrimSuffix(name, ".glb"))
		default:
			http.Error(w, "不支持", http.StatusMethodNotAllowed)
		}
	})
}

func (b *Builder) answer(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var wrap struct {
		Spec *Spec `json:"spec"`
	}
	var s Spec
	if json.Unmarshal(body, &wrap) == nil && wrap.Spec != nil {
		s = *wrap.Spec
	} else {
		var p ikea.Product
		if err := json.Unmarshal(body, &p); err != nil {
			http.Error(w, "看不懂这个商品："+err.Error(), http.StatusBadRequest)
			return
		}
		s = SpecFor(p)
	}
	if s.W <= 0 || s.D <= 0 || s.H <= 0 || s.W > 1000 || s.D > 1000 || s.H > 400 {
		http.Error(w, "尺寸不对：宽、深、高要在 0～10 米之间", http.StatusBadRequest)
		return
	}
	key := Key(s)
	a := Answer{Key: key, URL: "/api/models/" + key + ".glb", Available: b.Available(), Spec: s}
	switch {
	case !a.Available:
	case b.Cached(key):
		a.Ready = true
	default:
		// a failed build is tried again only when asked with ?retry=1
		if a.Error = b.Failed(key); a.Error == "" || r.URL.Query().Get("retry") == "1" {
			a.Error = ""
			b.Start(s)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a)
}

func (b *Builder) serve(w http.ResponseWriter, r *http.Request, key string) {
	if !keyRe.MatchString(key) || b == nil || b.Dir == "" {
		http.NotFound(w, r)
		return
	}
	if !b.Cached(key) {
		if b.Building(key) {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "model/gltf-binary")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeFile(w, r, b.Path(key))
}

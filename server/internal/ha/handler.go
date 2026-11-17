package ha

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// Row is one entity in the mapping panel.
type Row struct {
	Entity
	Suggestion *Suggestion `json:"suggestion,omitempty"`
	Mapped     *Mapping    `json:"mapped,omitempty"`
}

// Rows is the catalogue with each entity's suggestion or mapping.
func (a *Adapter) Rows() []Row {
	ents := a.Entities()
	sugg := map[string]*Suggestion{}
	for _, s := range Suggest(a.cfg.Home, ents, a.hub.List(), a.store) {
		s := s
		for _, e := range s.Entities {
			sugg[e] = &s
		}
	}
	out := make([]Row, 0, len(ents))
	for _, e := range ents {
		e.Attributes = nil // the panel does not need them
		r := Row{Entity: e, Suggestion: sugg[e.ID]}
		if m, ok := a.store.ForEntity(e.ID); ok {
			m := m
			r.Mapped = &m
			r.Suggestion = nil
		}
		out = append(out, r)
	}
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

func read(r *http.Request, v any) error {
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20)).Decode(v); err != nil {
		return errors.New("请求的内容不是有效的 JSON")
	}
	return nil
}

// Handler serves /api/ha/: status, config, reconnect, entities and the mappings.
func (a *Adapter) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ha/status", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, a.Status()) })
	mux.HandleFunc("PUT /api/ha/config", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := read(r, &body); err != nil {
			fail(w, 400, err.Error())
			return
		}
		for k := range body {
			if strings.Contains(strings.ToLower(k), "token") {
				fail(w, 400, "令牌不经过网页：请用 security add-generic-password -a hometwin -s hometwin-ha -w 存进钥匙串")
				return
			}
		}
		u, _ := body["url"].(string)
		if err := a.SetURL(u); err != nil {
			fail(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, a.Status())
	})
	mux.HandleFunc("POST /api/ha/reconnect", func(w http.ResponseWriter, r *http.Request) {
		a.Reconnect()
		writeJSON(w, 200, a.Status())
	})
	mux.HandleFunc("GET /api/ha/entities", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, a.Rows()) })
	mux.HandleFunc("PUT /api/ha/mapping", func(w http.ResponseWriter, r *http.Request) {
		var req MapRequest
		if err := read(r, &req); err != nil {
			fail(w, 400, err.Error())
			return
		}
		m, err := a.Map(req)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, m)
	})
	mux.HandleFunc("DELETE /api/ha/mapping/{entity}", func(w http.ResponseWriter, r *http.Request) {
		if err := a.Unmap(r.PathValue("entity")); err != nil {
			fail(w, 404, err.Error())
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/ha/mapping/auto", func(w http.ResponseWriter, r *http.Request) {
		ms, err := a.AutoMap()
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"mapped": ms})
	})
	mux.HandleFunc("/api/ha/", func(w http.ResponseWriter, r *http.Request) { fail(w, 404, "没有这个接口") })
	return mux
}

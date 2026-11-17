package web

import (
	"context"
	"net/http"
	"strings"
	"time"

	"hometwin/internal/catalog"
)

// findModels reads a sentence (「卧室A 放一个床头柜」) and searches the model catalogue for it.
func (s *Server) findModels(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	if err := read(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	in, err := s.Catalog.Understand(ctx, body.Text, s.Home.Rooms)
	if err != nil {
		fail(w, 422, err.Error())
		return
	}
	hits, err := s.Catalog.Search(ctx, in.Query, 12)
	if err != nil {
		s.Log.Info("catalog search failed", "query", in.Query, "err", err)
		fail(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"query": in.Query, "room": in.Room, "hits": hits})
}

// addModel downloads a picked model and puts it in the 家具库.
func (s *Server) addModel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID string `json:"id"`
	}
	if err := read(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	p, err := s.Catalog.Product(ctx, strings.TrimSpace(body.ID))
	if err != nil {
		s.Log.Info("catalog add failed", "id", body.ID, "err", err)
		fail(w, 502, err.Error())
		return
	}
	lib, err := s.Layouts.AddToLibrary(s.Home.ID, p)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"product": p, "library": lib})
}

// catalogModel serves a model picked earlier.
func (s *Server) catalogModel(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(r.PathValue("file"), ".glb")
	if !catalog.ValidID(id) {
		fail(w, 404, "没有这个模型")
		return
	}
	w.Header().Set("Content-Type", "model/gltf-binary")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeFile(w, r, s.Catalog.File(id))
}

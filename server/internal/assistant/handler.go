package assistant

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// Examples are things to ask, for the chat's first screen.
var Examples = []string{
	"客厅现在多少度",
	"把客厅灯调到 40%",
	"这张沙发放得下吗 https://www.ikea.com/us/en/p/kivik-sofa-tibbleby-beige-gray-s39440593/",
	"给访客开门",
	"主卧空调多久能到 24 度",
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// stream answers with server-sent events, one per call of the function it returns.
func stream(w http.ResponseWriter) func(Event) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	fl, _ := w.(http.Flusher)
	return func(e Event) {
		b, _ := json.Marshal(e)
		w.Write([]byte("data: "))
		w.Write(b)
		w.Write([]byte("\n\n"))
		if fl != nil {
			fl.Flush()
		}
	}
}

// Handler serves the assistant under /api/assistant/:
//
//	GET    /api/assistant/status                      configured, model, examples
//	GET    /api/assistant/conversations               the list
//	POST   /api/assistant/conversations               a new one
//	GET    /api/assistant/conversations/{id}          one with its messages
//	DELETE /api/assistant/conversations/{id}
//	POST   /api/assistant/conversations/{id}/messages {"text": …} → server-sent events
func (a *Assistant) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/assistant/status", func(w http.ResponseWriter, r *http.Request) {
		out := map[string]any{"configured": a.Configured(), "model": a.Model(), "examples": Examples}
		if !a.Configured() {
			out["notice"] = ErrNoModel.Error()
		}
		writeJSON(w, 200, out)
	})
	mux.HandleFunc("GET /api/assistant/conversations", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, a.store.list())
	})
	mux.HandleFunc("POST /api/assistant/conversations", func(w http.ResponseWriter, r *http.Request) {
		c, err := a.store.create("新对话")
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, c)
	})
	mux.HandleFunc("GET /api/assistant/conversations/{id}", func(w http.ResponseWriter, r *http.Request) {
		c, err := a.store.get(r.PathValue("id"))
		if err != nil {
			fail(w, 404, err.Error())
			return
		}
		writeJSON(w, 200, c)
	})
	mux.HandleFunc("DELETE /api/assistant/conversations/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := a.store.remove(r.PathValue("id")); err != nil {
			fail(w, 404, err.Error())
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/assistant/conversations/{id}/messages", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
			fail(w, 400, "请求的内容不是有效的 JSON")
			return
		}
		id := r.PathValue("id")
		if _, err := a.store.get(id); err != nil {
			fail(w, 404, err.Error())
			return
		}
		if !a.Configured() {
			fail(w, 503, ErrNoModel.Error())
			return
		}
		emit := stream(w)
		emit(Event{Kind: "conversation", ID: id})
		if err := a.Ask(r.Context(), id, body.Text, emit); err != nil {
			a.log.Info("assistant", "err", err)
			emit(Event{Kind: "error", Text: err.Error()})
		}
	})
	mux.HandleFunc("/api/assistant/", func(w http.ResponseWriter, r *http.Request) { fail(w, 404, "没有这个接口") })
	return mux
}

// compact keeps a tool result small enough to send to a browser and keep.
func compact(v any) any {
	if s, ok := v.(string); ok {
		var out any
		if json.Unmarshal([]byte(s), &out) == nil {
			v = out
		} else if len([]rune(s)) > 2000 {
			return string([]rune(s)[:2000]) + "…"
		} else {
			return s
		}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	if len(b) <= 6000 {
		var out any
		if json.Unmarshal(b, &out) == nil {
			return out
		}
	}
	r := []rune(string(b))
	return string(r[:min(len(r), 2000)]) + "…"
}

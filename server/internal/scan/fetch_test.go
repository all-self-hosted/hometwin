package scan

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// scanSite is what a scan's share page and CDN serve: the page's description of the scan,
// the model, its textures and the room layout.
type scanSite struct {
	work     Work
	model    []byte
	textures [][]byte
	layout   []byte
}

// fakeSite serves the fictional flat (fictional_test.go).
func fakeSite(t testing.TB) scanSite {
	w, _ := fakeWork()
	return scanSite{work: w, model: encodeAt3d(fakeFlat()), textures: fakeTextures(t), layout: fakeLayoutJSON(t)}
}

// sharePage builds a share page the way the real one carries its state: JSON in an HTML
// comment inside a <script type="application/json">, next to other such scripts.
func sharePage(t *testing.T, site scanSite, base string) string {
	t.Helper()
	w := site.work
	w.Model.FileURL = base + "/model/flat.at3d"
	w.Model.Textures = nil
	for i := range site.textures {
		w.Model.Textures = append(w.Model.Textures, fmt.Sprintf("%s/materials/texture_%d.jpg", base, i))
	}
	labels := w.WindowLabels
	w.WindowLabels = nil
	work, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	lb, _ := json.Marshal(labels)
	state := fmt.Sprintf(`{"firstscreen":{"defaultWork":%s,"objects":{"mrWindows":{"label_positions":%s}},"objectsInUrl":{"ruler":"%s/layout/room_layout.json?1"}},"configure":{}}`, work, lb, base)
	return `<!doctype html><html><head><script>window.$ENV='production'</script>
<script crossorigin="anonymous" type="application/json" id="state-i18n">
  <!-- {"立即下载":{}} --> </script>
<script crossorigin="anonymous" type="application/json" id="state-page-config">
  <!-- ` + state + ` --> </script></head><body></body></html>`
}

func testServer(t *testing.T, site scanSite) *httptest.Server {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.UserAgent(), "Mozilla") {
			http.Error(w, "no browser", http.StatusForbidden)
			return
		}
		var i int
		switch {
		case r.URL.Path == "/share":
			fmt.Fprint(w, sharePage(t, site, srv.URL))
		case r.URL.Path == "/model/flat.at3d":
			w.Write(site.model)
		case sscanf(r.URL.Path, "/materials/texture_%d.jpg", &i) && i >= 0 && i < len(site.textures):
			w.Header().Set("Content-Type", "image/jpeg")
			w.Write(site.textures[i])
		case r.URL.Path == "/layout/room_layout.json":
			w.Write(site.layout)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func sscanf(s, format string, a ...any) bool {
	n, err := fmt.Sscanf(s, format, a...)
	return err == nil && n == len(a)
}

func TestFetchAndDownloadFictional(t *testing.T) {
	site := fakeSite(t)
	srv := testServer(t, site)
	w, err := Fetch(context.Background(), srv.URL+"/share", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if w.Code != site.work.Code || len(w.Observers) != len(site.work.Observers) || len(w.Model.Textures) != fakeChunks || len(w.WindowLabels) != 2 || !strings.HasSuffix(w.RulerURL, "room_layout.json?1") {
		t.Fatalf("work %q: %d observers, %d textures, %d windows, ruler %q", w.Code, len(w.Observers), len(w.Model.Textures), len(w.WindowLabels), w.RulerURL)
	}
	if fmt.Sprint(w.Observers) != fmt.Sprint(site.work.Observers) || fmt.Sprint(w.WindowLabels) != fmt.Sprint(site.work.WindowLabels) {
		t.Error("observers or window labels changed on the way")
	}
	dir := t.TempDir()
	files, err := Download(context.Background(), w, dir, srv.Client(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(files.Textures) != fakeChunks || files.Layout == "" {
		t.Fatalf("files %+v", files)
	}
	for i, p := range files.Textures {
		if b, err := os.ReadFile(p); err != nil || string(b) != string(site.textures[i]) {
			t.Errorf("texture %d: %v", i, err)
		}
	}
	b, err := os.ReadFile(files.Model)
	if err != nil {
		t.Fatal(err)
	}
	if m, err := Decode(b); err != nil || len(m.Chunks) != fakeChunks {
		t.Fatalf("downloaded model: %v", err)
	}
	lb, _ := os.ReadFile(files.Layout)
	if l, err := ParseLayout(lb); err != nil || len(l) != len(site.work.Observers) || l[0].Name != "客厅" {
		t.Fatalf("layout: %v, %v", l, err)
	}

	// The cap counts everything.
	if _, err := Download(context.Background(), w, t.TempDir(), srv.Client(), int64(len(site.model)/2)); err == nil {
		t.Error("a cap of half the model let it through")
	}
	// A missing layout is no failure: the names are a convenience.
	w.RulerURL = srv.URL + "/layout/none.json"
	if files, err := Download(context.Background(), w, t.TempDir(), srv.Client(), 0); err != nil || files.Layout != "" {
		t.Errorf("without a layout: %+v, %v", files, err)
	}
	// A missing texture is.
	w.Model.Textures = append(w.Model.Textures, srv.URL+"/materials/texture_9.jpg")
	if _, err := Download(context.Background(), w, t.TempDir(), srv.Client(), 0); err == nil {
		t.Error("a missing texture went unnoticed")
	}
}

func TestFetchErrors(t *testing.T) {
	srv := testServer(t, fakeSite(t))
	for _, u := range []string{"not a url", "ftp://x/y", srv.URL + "/missing"} {
		if _, err := Fetch(context.Background(), u, srv.Client()); err == nil {
			t.Errorf("Fetch(%q): no error", u)
		}
	}
	if _, err := ParsePage([]byte("<html><script><!-- {\"a\":1} --></script></html>")); err == nil {
		t.Error("a page without defaultWork parsed")
	}
	if _, err := ParsePage([]byte(`<html><script><!-- {"firstscreen":{"defaultWork":{"code":"x","model":{}}}} --></script></html>`)); err == nil {
		t.Error("a page without a model parsed")
	}
}

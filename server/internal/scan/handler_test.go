package scan

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"hometwin/internal/home"
)

// postImport sends a pasted share text (and a floor plan picture, when given) to the
// importer and waits for the job to finish.
func postImport(t *testing.T, srv *httptest.Server, pasted string, plan []byte) Job {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("link", pasted)
	if plan != nil {
		fw, _ := mw.CreateFormFile("image", "plan.jpg")
		fw.Write(plan)
	}
	mw.Close()
	resp, err := http.Post(srv.URL+"/api/scan", mw.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	var j Job
	json.NewDecoder(resp.Body).Decode(&j)
	resp.Body.Close()
	if j.ID == "" {
		t.Fatalf("no job: %d", resp.StatusCode)
	}
	deadline := time.Now().Add(60 * time.Second)
	for j.Status == "running" && time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
		r, err := http.Get(srv.URL + "/api/scan/" + j.ID)
		if err != nil {
			t.Fatal(err)
		}
		json.NewDecoder(r.Body).Decode(&j)
		r.Body.Close()
	}
	return j
}

// checkJob holds a finished import to its review and confirm steps: the steps never show
// the link, the top view is served, confirming hands the home on and serves its model.
func checkJob(t *testing.T, srv, cdn *httptest.Server, j Job, used *home.Home) {
	t.Helper()
	for _, s := range j.Steps {
		t.Log(s.Text)
		if strings.Contains(s.Text, cdn.URL) {
			t.Errorf("a step shows the link: %s", s.Text)
		}
	}
	if j.Status != "done" || j.Result == nil || j.View == nil {
		t.Fatalf("job %s: %s", j.Status, j.Error)
	}
	if r, err := http.Get(srv.URL + "/api/scan/" + j.ID + "/top.png"); err != nil || r.StatusCode != 200 {
		t.Fatalf("top view: %v", err)
	}
	r, err := http.Post(srv.URL+"/api/scan/"+j.ID+"/confirm", "", nil)
	if err != nil || r.StatusCode != 200 {
		t.Fatalf("confirm: %v %v", err, r.Status)
	}
	if used.ID == "" || used.Scan == "" || len(used.Levels) < 2 {
		t.Fatalf("home in use: %q scan %q levels %d", used.ID, used.Scan, len(used.Levels))
	}
	g, err := http.Get(srv.URL + used.Scan)
	if err != nil || g.StatusCode != 200 {
		t.Fatalf("model: %v", err)
	}
	b := new(bytes.Buffer)
	b.ReadFrom(g.Body)
	readGLB(t, b.Bytes())
}

// The fictional flat through the whole import, without a floor plan: the names come from
// the scan's layout, north is as scanned.
func TestHandlerImportFictional(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a whole import")
	}
	cdn := testServer(t, fakeSite(t))
	var used home.Home
	im, err := NewImporter(t.TempDir(), nil, home.Sample, func(h home.Home) error { used = h; return nil }, cdn.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(im.Handler())
	defer srv.Close()

	j := postImport(t, srv, "【示例】VR 看房 "+cdn.URL+"/share 点击查看", nil)
	checkJob(t, srv, cdn, j, &used)
	if used.Name != "我的家 · 1室1厅" || len(used.Rooms) != len(fakeAreas) || len(used.Doors) != 3 || len(used.Windows) != 2 {
		t.Errorf("home %q: %d rooms, %d doors, %d windows", used.Name, len(used.Rooms), len(used.Doors), len(used.Windows))
	}
	for _, r := range used.Rooms {
		if _, ok := fakeAreas[r.Name]; !ok {
			t.Errorf("a room called %q", r.Name)
		}
	}
}

package assistant

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	agentpkg "github.com/liliang-cn/agent-go/v3/pkg/agent"
	"github.com/liliang-cn/agent-go/v3/pkg/domain"
	"github.com/liliang-cn/agent-go/v3/pkg/extensiontest"

	"hometwin/internal/automate"
	"hometwin/internal/devices"
	"hometwin/internal/home"
	"hometwin/internal/layout"
	"hometwin/internal/predict"
)

type fakeKnowledge struct{}

func (fakeKnowledge) Ask(_ context.Context, q string) (string, []string, error) {
	return "知识图谱说：" + q, []string{"room:living"}, nil
}

// setup builds a home with the simulator; llm nil means no model.
func setup(t *testing.T, llm domain.Generator) (*Assistant, Deps) {
	t.Helper()
	t.Setenv("AGENTGO_HOME", t.TempDir())
	t.Setenv("LLM_API_KEY", "")
	dir := t.TempDir()
	h := home.Sample()
	st, err := layout.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	hub, err := devices.NewHub(h.ID, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	sim := devices.NewSim(h)
	hub.Use(sim)
	auto, err := automate.New(hub, "")
	if err != nil {
		t.Fatal(err)
	}
	est := predict.NewEstimator(h)
	for _, d := range hub.List() {
		if v, ok := d.State["temp"].(float64); ok && d.Kind == devices.Sensor {
			est.Observe(d.Room, v, time.Now())
		}
	}
	d := Deps{Home: h, Layouts: st, Devices: hub, Auto: auto, Estimate: est, HTTP: http.DefaultClient, Knowledge: fakeKnowledge{},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), LLM: llm}
	a, err := New(d, dir)
	if err != nil {
		t.Fatal(err)
	}
	// the scripted model runs in a throwaway agent-go home
	a.build = func(b *agentpkg.Builder) (*agentpkg.Service, error) {
		return extensiontest.NewServiceWithBuilder(b, t.TempDir())
	}
	t.Cleanup(func() { a.Close() })
	return a, d
}

// ask runs one question and collects the events.
func ask(t *testing.T, a *Assistant, text string) (Conversation, []Event) {
	t.Helper()
	c, err := a.store.create("新对话")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var evs []Event
	if err := a.Ask(context.Background(), c.ID, text, func(e Event) {
		mu.Lock()
		evs = append(evs, e)
		mu.Unlock()
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := a.store.get(c.ID)
	return got, evs
}

func kinds(evs []Event, k string) []Event {
	var out []Event
	for _, e := range evs {
		if e.Kind == k {
			out = append(out, e)
		}
	}
	return out
}

func billyServer(t *testing.T) *httptest.Server {
	t.Helper()
	f, err := os.Open("../ikea/testdata/billy.html.gz")
	if err != nil {
		t.Skip("fixture missing")
	}
	defer f.Close()
	zr, _ := gzip.NewReader(f)
	page, _ := io.ReadAll(zr)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(page) }))
	t.Cleanup(s.Close)
	return s
}

func TestToolsDirect(t *testing.T) {
	a, d := setup(t, nil)
	ctx := context.Background()
	call := func(name string, m map[string]any) any {
		t.Helper()
		res, err := a.tools.call(ctx, name, m)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return res
	}
	js := func(v any) string { b, _ := json.Marshal(v); return string(b) }

	if s := js(call("list_rooms", nil)); !strings.Contains(s, "客餐厅") || !strings.Contains(s, "温湿度") {
		t.Fatalf("rooms: %s", s)
	}
	if s := js(call("list_devices", map[string]any{"room": "客厅"})); !strings.Contains(s, "客厅主灯") || strings.Contains(s, "主卧空调") {
		t.Fatalf("living room devices: %s", s)
	}
	call("set_device", map[string]any{"device": "客厅主灯", "on": true, "brightness": 40.0})
	if dv, _ := d.Devices.Get("light-living"); dv.State["on"] != true || dv.State["brightness"] != 40.0 {
		t.Fatalf("light: %+v", dv.State)
	}
}

func TestToolRefusalsAndPredict(t *testing.T) {
	a, d := setup(t, nil)
	ctx := context.Background()
	if _, err := a.tools.call(ctx, "set_device", map[string]any{"device": "ac-living", "target": 12.0}); err == nil || !strings.Contains(err.Error(), "16～30") {
		t.Fatalf("12 ℃: %v", err)
	}
	res, err := a.tools.call(ctx, "set_device", map[string]any{"device": "入户门锁", "locked": false})
	if err != nil {
		t.Fatal(err)
	}
	if dv, _ := d.Devices.Get("lock-entry"); dv.State["locked"] != true {
		t.Fatal("unlocked without a person")
	}
	if b, _ := json.Marshal(res); !strings.Contains(string(b), "等家里人确认") {
		t.Fatalf("%s", b)
	}
	p, err := a.tools.call(ctx, "predict_temperature", map[string]any{"room": "主卧", "target": 22.0, "mode": "cool"})
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(p); !strings.Contains(string(b), "分钟") {
		t.Fatalf("prediction: %s", b)
	}
	// a free spot for a bookcase in the second bedroom
	srv := billyServer(t)
	fit, err := a.tools.call(ctx, "check_fit", map[string]any{"product": srv.URL + "/us/en/p/billy-bookcase-white-20522046/", "room": "次卧"})
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(fit); !strings.Contains(string(b), "放得下") {
		t.Fatalf("fit: %s", b)
	}
	if lib, _ := d.Layouts.Library(d.Home.ID); len(lib) != 1 || lib[0].Family != "BILLY" {
		t.Fatalf("the link goes into the library: %+v", lib)
	}
}

func TestAgentListsDevices(t *testing.T) {
	llm := extensiontest.Script(
		extensiontest.CallTool("list_devices", map[string]any{"room": "客厅"}),
		extensiontest.Answer("客厅有主灯、吊灯、空调、窗帘、扫地机器人和温湿度传感器。"),
	)
	a, _ := setup(t, llm)
	c, evs := ask(t, a, "客厅有哪些设备")
	calls := kinds(evs, "tool_call")
	if len(calls) != 1 || calls[0].Label != "查了客餐厅的设备" {
		t.Fatalf("calls %+v", calls)
	}
	res := kinds(evs, "tool_result")
	if len(res) != 1 {
		t.Fatalf("results %+v", res)
	}
	if b, _ := json.Marshal(res[0].Result); !strings.Contains(string(b), "客厅主灯") {
		t.Fatalf("the model was shown %s", b)
	}
	done := kinds(evs, "complete")
	if len(done) != 1 || !strings.Contains(done[0].Text, "主灯") {
		t.Fatalf("complete %+v", done)
	}
	// kept: the question, the answer with its step
	if len(c.Messages) != 2 || c.Messages[1].Status != "done" || len(c.Messages[1].Steps) != 1 || c.Title != "客厅有哪些设备" {
		t.Fatalf("kept %+v", c)
	}
}

func TestAgentTurnsOnLight(t *testing.T) {
	llm := extensiontest.Script(
		extensiontest.CallTool("set_device", map[string]any{"device": "客厅主灯", "on": true, "brightness": 40.0}),
		extensiontest.Answer("客厅主灯开了，亮度 40%。"),
	)
	a, d := setup(t, llm)
	ask(t, a, "把客厅灯调到 40%")
	if dv, _ := d.Devices.Get("light-living"); dv.State["on"] != true || dv.State["brightness"] != 40.0 {
		t.Fatalf("light: %+v", dv.State)
	}
}

func TestAgentUnlockWaitsForAPerson(t *testing.T) {
	llm := extensiontest.Script(
		extensiontest.CallTool("set_device", map[string]any{"device": "入户门锁", "locked": false}),
		extensiontest.Answer("开锁的请求已经提交，等你在自动化面板里确认。"),
	)
	a, d := setup(t, llm)
	_, evs := ask(t, a, "给访客开门")
	if dv, _ := d.Devices.Get("lock-entry"); dv.State["locked"] != true {
		t.Fatal("the assistant unlocked the front door")
	}
	pend := 0
	for _, p := range d.Auto.Proposals() {
		if p.Status == "pending" && strings.Contains(strings.Join(p.Steps, ""), "开锁") {
			pend++
		}
	}
	if pend != 1 {
		t.Fatalf("pending approvals: %+v", d.Auto.Proposals())
	}
	if len(kinds(evs, "approval")) != 1 {
		t.Fatalf("no approval event: %+v", evs)
	}
}

func TestAgentImportsAndProposesPlace(t *testing.T) {
	srv := billyServer(t)
	url := srv.URL + "/us/en/p/billy-bookcase-white-20522046/"
	llm := extensiontest.Script(
		extensiontest.CallTool("propose_place", map[string]any{"product": url, "room": "次卧"}),
		extensiontest.Answer("我在次卧找了个靠墙的位置，点「放进去」就放。"),
	)
	a, d := setup(t, llm)
	c, evs := ask(t, a, "这个书柜放进次卧 "+url)
	ps := kinds(evs, "place")
	if len(ps) != 1 {
		t.Fatalf("place events %+v", evs)
	}
	pl := ps[0].Place
	r := d.Home.RoomAt(home.P{pl.X, pl.Z})
	if r == nil || r.ID != "second" || pl.Room != "second" || pl.Product.Family != "BILLY" {
		t.Fatalf("placed at %+v in %v", pl, r)
	}
	if len(pl.Warnings) != 0 {
		t.Fatalf("a proposal with problems: %v", pl.Warnings)
	}
	// nothing was put in the room: the person does that
	if l, _ := d.Layouts.Layout(d.Home.ID); len(l.Items) != 0 {
		t.Fatalf("placed without asking: %+v", l.Items)
	}
	// the check agrees with the spot
	l, _ := d.Layouts.Layout(d.Home.ID)
	l.Items = append(l.Items, layout.Item{ID: "billy", Product: pl.Product, X: pl.X, Z: pl.Z, Rot: pl.Rot})
	for _, w := range layout.Check(d.Home, l) {
		if w.Level == "warn" {
			t.Fatalf("the spot has %s", w.Text)
		}
	}
	if len(c.Messages[1].Places) != 1 {
		t.Fatal("the proposal is not kept with the answer")
	}
}

func TestAgentPredicts(t *testing.T) {
	llm := extensiontest.Script(
		extensiontest.CallTool("predict_temperature", map[string]any{"room": "主卧", "target": 24.0}),
		extensiontest.Answer("主卧现在不到 25 ℃，开空调几分钟就到 24 ℃。"),
	)
	a, _ := setup(t, llm)
	_, evs := ask(t, a, "主卧空调多久能到 24 度")
	res := kinds(evs, "tool_result")
	if len(res) != 1 {
		t.Fatalf("%+v", evs)
	}
	if b, _ := json.Marshal(res[0].Result); !strings.Contains(string(b), "预计") {
		t.Fatalf("%s", b)
	}
}

func TestNotConfigured(t *testing.T) {
	a, _ := setup(t, nil)
	if a.Configured() {
		t.Fatal("no model, but configured")
	}
	ts := httptest.NewServer(a.Handler())
	defer ts.Close()
	res, _ := http.Get(ts.URL + "/api/assistant/status")
	var st map[string]any
	json.NewDecoder(res.Body).Decode(&st)
	if st["configured"] != false || !strings.Contains(st["notice"].(string), "LLM_BASE_URL") {
		t.Fatalf("status %+v", st)
	}
	res, _ = http.Post(ts.URL+"/api/assistant/conversations", "application/json", nil)
	var c Conversation
	json.NewDecoder(res.Body).Decode(&c)
	res, _ = http.Post(ts.URL+"/api/assistant/conversations/"+c.ID+"/messages", "application/json", bytes.NewBufferString(`{"text":"你好"}`))
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != 503 || !strings.Contains(string(b), "还没有配置模型") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	// the rest still works: conversations list and delete
	res, _ = http.Get(ts.URL + "/api/assistant/conversations")
	var list []Summary
	json.NewDecoder(res.Body).Decode(&list)
	if len(list) != 1 {
		t.Fatalf("list %+v", list)
	}
	req, _ := http.NewRequest("DELETE", ts.URL+"/api/assistant/conversations/"+c.ID, nil)
	res, _ = http.DefaultClient.Do(req)
	if res.StatusCode != 200 {
		t.Fatal(res.StatusCode)
	}
}

func TestStreamOverHTTP(t *testing.T) {
	llm := extensiontest.Script(
		extensiontest.CallTool("read_sensors", map[string]any{"room": "客厅"}),
		extensiontest.Answer("客厅 25 ℃ 左右。"),
	)
	a, _ := setup(t, llm)
	ts := httptest.NewServer(a.Handler())
	defer ts.Close()
	res, _ := http.Post(ts.URL+"/api/assistant/conversations", "application/json", nil)
	var c Conversation
	json.NewDecoder(res.Body).Decode(&c)
	res, err := http.Post(ts.URL+"/api/assistant/conversations/"+c.ID+"/messages", "application/json", bytes.NewBufferString(`{"text":"客厅现在多少度"}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	body := string(b)
	if res.Header.Get("Content-Type") != "text/event-stream" || !strings.Contains(body, `"kind":"tool_call"`) || !strings.Contains(body, `"kind":"complete"`) {
		t.Fatalf("%s\n%s", res.Header.Get("Content-Type"), body)
	}
	// reopened from disk, the conversation is still there
	a2, err := New(a.d, a.dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := a2.store.get(c.ID)
	if err != nil || len(got.Messages) != 2 {
		t.Fatalf("%v %+v", err, got)
	}
}

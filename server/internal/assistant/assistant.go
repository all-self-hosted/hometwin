// Package assistant is the 家庭助手: an agent-go loop with tools over the home — rooms,
// furniture, devices, the layout checks, the furniture library, the knowledge graph, the room
// thermal model and the automations. It answers in Chinese, does what the house rules allow,
// and hands anything a person must decide (unlocking the door, putting a new piece in a room)
// back to that person instead of doing it.
package assistant

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	agentpkg "github.com/liliang-cn/agent-go/v3/pkg/agent"
	"github.com/liliang-cn/agent-go/v3/pkg/domain"
	"github.com/liliang-cn/agent-go/v3/pkg/pool"

	"hometwin/internal/automate"
	"hometwin/internal/catalog"
	"hometwin/internal/devices"
	"hometwin/internal/home"
	"hometwin/internal/ikea"
	"hometwin/internal/layout"
	"hometwin/internal/predict"
)

// Knowledge answers questions about the home from its knowledge graph.
type Knowledge interface {
	Ask(ctx context.Context, q string) (text string, nodes []string, err error)
}

// Deps are the parts of the twin the assistant works with.
type Deps struct {
	Home      home.Home
	Layouts   *layout.Store
	Devices   *devices.Hub
	Auto      *automate.Engine
	Estimate  *predict.Estimator
	HTTP      *http.Client     // for reading product pages
	Knowledge Knowledge        // may be nil
	Catalog   *catalog.Catalog // the 3D model catalogue; may be nil
	Log       *slog.Logger
	// LLM is the model; nil reads LLM_BASE_URL, LLM_API_KEY and LLM_MODEL (OpenAI-compatible).
	LLM domain.Generator
}

// Place is a piece of furniture the assistant proposes to put somewhere; the person puts it
// there from the card in the chat.
type Place struct {
	Product  ikea.Product `json:"product"`
	X        float64      `json:"x"`
	Z        float64      `json:"z"`
	Rot      float64      `json:"rot"`
	Room     string       `json:"room"`
	Text     string       `json:"text"`
	Warnings []string     `json:"warnings,omitempty"`
}

// Event is one step of an answer as it is written, for the browser.
type Event struct {
	// Kind: conversation, thinking, tool_call, tool_result, delta, reset, place, approval,
	// highlight, complete, error.
	Kind   string         `json:"kind"`
	ID     string         `json:"id,omitempty"`
	Tool   string         `json:"tool,omitempty"`
	Label  string         `json:"label,omitempty"`
	Args   map[string]any `json:"args,omitempty"`
	Result any            `json:"result,omitempty"`
	Text   string         `json:"text,omitempty"`
	Place  *Place         `json:"place,omitempty"`
	Nodes  []string       `json:"nodes,omitempty"`
}

// ErrNoModel is said when no model is configured.
var ErrNoModel = errors.New("还没有配置模型：设置 LLM_BASE_URL、LLM_API_KEY、LLM_MODEL 后重启")

var shanghai = func() *time.Location {
	if l, err := time.LoadLocation("Asia/Shanghai"); err == nil {
		return l
	}
	return time.FixedZone("CST", 8*3600)
}()

var weekday = []string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}

const prompt = `你是「%s」的家庭助手，帮这家人看家里的情况、控制设备、布置家具。

你只通过工具了解和改变这个家：
- 看：list_rooms（房间、面积、温湿度）、list_furniture（家具、尺寸、靠哪面墙）、list_devices / get_device（设备和状态）、read_sensors（温湿度读数）、layout_warnings（现在布置的问题）、ask_home（家的知识图谱：什么在哪、谁控制什么、房间怎么连）。
- 控制设备：set_device。每次改动都过家里的规则（比如空调只能设 16～30 ℃）。开锁不会直接执行，会变成一条等家里人确认的请求——这时要说清楚「已经提交，等你在自动化面板里确认」，不要说已经开了。
- 家具：import_furniture 读宜家链接（真实尺寸、价格），check_fit 检查放不放得下（碰撞、过道至少 80 cm、挡门、挡窗、进不进得了入户门），find_models 在 3D 模型库里找家具库和宜家都没有的东西（钢琴、绿植、床头柜……，用英文词搜，挑尺寸合适的那个，把 polyfork:<id> 交给 check_fit / propose_place），propose_place 提出放的位置——它只给家里人一张「放进去」的卡片，点了才放，不要说已经放好了。问「放得下吗」就先 check_fit，放得下再 propose_place；放不下就说清楚是哪条问题。
- 预测：predict_temperature 按房间的热模型算空调多久到温。
- 自动化：list_automations（每条的授权等级：只建议 / 有护栏 / 自主，和等确认的请求），run_automation 运行一条（它按自己的等级执行，开锁一定等人确认）。

规则：
- 用中文，简短、口语，不要列一大堆。数字、名字以工具返回为准，不要编。
- 说温度用 ℃，说尺寸用 cm。
- 用户说的房间名可能是口语（客厅就是客餐厅，大卧室是主卧）。
- 不确定是哪个设备就先 list_devices 看一下再操作。`

// Assistant is the home's assistant.
type Assistant struct {
	d     Deps
	dir   string
	store *store
	tools *Tools
	log   *slog.Logger
	// build makes the agent-go service (tests swap it for one over a scripted model).
	build func(b *agentpkg.Builder) (*agentpkg.Service, error)

	mu  sync.Mutex
	svc *agentpkg.Service
}

type emitKey struct{}

// New opens the conversations kept in dir. The model is Deps.LLM, or the one LLM_BASE_URL,
// LLM_API_KEY and LLM_MODEL describe; without either the assistant only says so.
func New(d Deps, dir string) (*Assistant, error) {
	st, err := openStore(dir)
	if err != nil {
		return nil, err
	}
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	if d.HTTP == nil {
		d.HTTP = ikea.NewClient()
	}
	// agent-go keeps its own state (the conversations' history) under AGENTGO_HOME
	if os.Getenv("AGENTGO_HOME") == "" {
		os.Setenv("AGENTGO_HOME", filepath.Join(dir, "agentgo"))
	}
	if d.LLM == nil {
		if key := os.Getenv("LLM_API_KEY"); key != "" {
			p, err := pool.NewClient("llm", os.Getenv("LLM_BASE_URL"), key, os.Getenv("LLM_MODEL"))
			if err != nil {
				return nil, fmt.Errorf("模型配置有误：%w", err)
			}
			d.LLM = p
		}
	}
	a := &Assistant{d: d, dir: dir, store: st, log: log}
	a.tools = newTools(d, a.emitFor)
	a.build = func(b *agentpkg.Builder) (*agentpkg.Service, error) {
		return b.WithDBPath(filepath.Join(dir, "agentgo.db")).Build()
	}
	return a, nil
}

// Configured says whether there is a model to answer with.
func (a *Assistant) Configured() bool { return a.d.LLM != nil }

// Model names the configured model (never the key).
func (a *Assistant) Model() string {
	if !a.Configured() {
		return ""
	}
	if m := os.Getenv("LLM_MODEL"); m != "" {
		return m
	}
	return "已配置"
}

func (a *Assistant) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.svc != nil {
		err := a.svc.Close()
		a.svc = nil
		return err
	}
	return nil
}

func (a *Assistant) emitFor(ctx context.Context, e Event) {
	if fn, ok := ctx.Value(emitKey{}).(func(Event)); ok && fn != nil {
		fn(e)
	}
}

func (a *Assistant) service() (*agentpkg.Service, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.svc != nil {
		return a.svc, nil
	}
	b := agentpkg.New("hometwin").WithLLM(a.d.LLM).WithSystemPrompt(fmt.Sprintf(prompt, a.d.Home.Name)).WithTimezone(shanghai)
	svc, err := a.build(b)
	if err != nil {
		return nil, err
	}
	for _, tl := range a.tools.tools {
		svc.AddToolWithMetadata(tl.name, tl.desc, tl.params, func(ctx context.Context, m map[string]any) (any, error) {
			res, err := tl.run(ctx, args(m))
			if err != nil {
				// a tool's refusal is information for the model, not a failed run
				return map[string]any{"错误": err.Error()}, nil
			}
			return res, nil
		}, agentpkg.ToolMetadata{ReadOnly: !writes[tl.name], ConcurrencySafe: !writes[tl.name]})
	}
	a.svc = svc
	return svc, nil
}

// the tools that change something in the home
var writes = map[string]bool{"set_device": true, "import_furniture": true, "propose_place": true, "run_automation": true}

// Ask puts text to the assistant in conversation conv; emit sees every step. The question and
// the answer, with its steps and proposals, are kept with the conversation.
func (a *Assistant) Ask(ctx context.Context, conv, text string, emit func(Event)) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("说点什么吧")
	}
	if _, err := a.store.get(conv); err != nil {
		return err
	}
	if !a.Configured() {
		return ErrNoModel
	}
	svc, err := a.service()
	if err != nil {
		return err
	}
	if _, err := a.store.add(conv, Message{Role: "user", Text: text, Status: "done"}); err != nil {
		return err
	}
	mi, err := a.store.add(conv, Message{Role: "assistant", Status: "running"})
	if err != nil {
		return err
	}
	var mu sync.Mutex
	var steps []Step
	var places []Place
	var approvals []string
	out := func(e Event) {
		mu.Lock()
		if e.Kind == "place" && e.Place != nil {
			places = append(places, *e.Place)
		}
		if e.Kind == "approval" {
			approvals = append(approvals, e.Text)
		}
		mu.Unlock()
		emit(e)
	}
	n := time.Now().In(shanghai)
	goal := fmt.Sprintf("（现在是 %s %s %s）\n\n%s", n.Format("2006-01-02"), weekday[n.Weekday()], n.Format("15:04"), text)
	runCtx := context.WithValue(ctx, emitKey{}, out)
	ch, err := svc.RunStreamWithOptions(runCtx, goal, agentpkg.WithSessionID(conv), agentpkg.WithMaxTurns(16))
	if err != nil {
		a.store.finish(conv, mi, err.Error(), nil, nil, nil, "failed")
		return err
	}
	own := a.tools.names()
	var answer, partial, failure string
	for ev := range ch {
		// agent-go's own tools (task_complete …) are how a run ends, not steps of the answer
		if (ev.Type == agentpkg.EventTypeToolCall || ev.Type == agentpkg.EventTypeToolResult) && !slices.Contains(own, ev.ToolName) {
			continue
		}
		switch ev.Type {
		case agentpkg.EventTypeThinking:
			out(Event{Kind: "thinking"})
		case agentpkg.EventTypeToolCall:
			label := a.tools.labelOf(ev.ToolName, ev.ToolArgs)
			mu.Lock()
			steps = append(steps, Step{Tool: ev.ToolName, Label: label, Args: ev.ToolArgs})
			mu.Unlock()
			out(Event{Kind: "tool_call", Tool: ev.ToolName, Label: label, Args: ev.ToolArgs})
		case agentpkg.EventTypeToolResult:
			res := compact(ev.ToolResult)
			mu.Lock()
			for i := len(steps) - 1; i >= 0; i-- {
				if steps[i].Tool == ev.ToolName && steps[i].Result == nil {
					steps[i].Result = res
					break
				}
			}
			mu.Unlock()
			out(Event{Kind: "tool_result", Tool: ev.ToolName, Result: res})
		case agentpkg.EventTypePartial:
			partial += ev.Content
			out(Event{Kind: "delta", Text: ev.Content})
		case agentpkg.EventTypeTombstone:
			partial = ""
			out(Event{Kind: "reset"})
		case agentpkg.EventTypeComplete:
			answer = ev.Content
		case agentpkg.EventTypeError:
			failure = ev.Content
		}
	}
	if answer == "" {
		answer = partial
	}
	status := "done"
	switch {
	case ctx.Err() != nil:
		status = "stopped"
	case answer == "" && failure != "":
		status, answer = "failed", failure
	}
	mu.Lock()
	a.store.finish(conv, mi, answer, steps, places, approvals, status)
	mu.Unlock()
	if status == "failed" {
		emit(Event{Kind: "error", Text: answer})
	} else {
		emit(Event{Kind: "complete", Text: answer})
	}
	return nil
}

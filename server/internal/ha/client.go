package ha

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// httpClient talks to HA directly when it is on this machine, through the environment's proxy
// otherwise.
func httpClient() *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = func(r *http.Request) (*url.URL, error) {
		host := r.URL.Hostname()
		if host == "localhost" {
			return nil, nil
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			return nil, nil
		}
		return http.ProxyFromEnvironment(r)
	}
	return &http.Client{Transport: tr, Timeout: 30 * time.Second}
}

// wsURL is the WebSocket API's address for an HA base URL.
func wsURL(base string) (string, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(base), "/"))
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("Home Assistant 地址不对：%q", base)
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("Home Assistant 地址要以 http:// 或 https:// 开头：%q", base)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/websocket"
	return u.String(), nil
}

// ErrAuth is returned when HA refuses the token.
var ErrAuth = errors.New("Home Assistant 不认这个访问令牌（可能过期或被删了），请重新创建一个并存进钥匙串")

// msg is one message of the WebSocket API.
type msg struct {
	ID      int             `json:"id"`
	Type    string          `json:"type"`
	Success bool            `json:"success"`
	Result  json.RawMessage `json:"result"`
	Event   json.RawMessage `json:"event"`
	Error   *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Message   string `json:"message"`
	HAVersion string `json:"ha_version"`
}

// Client is one authenticated connection to HA's WebSocket API.
type Client struct {
	conn    *websocket.Conn
	token   string
	Version string

	wmu     sync.Mutex
	mu      sync.Mutex
	next    int
	pending map[int]chan msg
	subs    map[int]chan json.RawMessage
	done    chan struct{}
	err     error
}

// Dial connects to <base>/api/websocket and authenticates with the token.
func Dial(ctx context.Context, base, token string) (*Client, error) {
	u, err := wsURL(base)
	if err != nil {
		return nil, err
	}
	conn, _, err := websocket.Dial(ctx, u, &websocket.DialOptions{HTTPClient: httpClient()})
	if err != nil {
		return nil, fmt.Errorf("连不上 Home Assistant（%s）：%s", base, scrub(err.Error(), token))
	}
	conn.SetReadLimit(64 << 20)
	c := &Client{conn: conn, token: token, pending: map[int]chan msg{}, subs: map[int]chan json.RawMessage{}, done: make(chan struct{})}
	fail := func(e error) (*Client, error) {
		conn.Close(websocket.StatusNormalClosure, "")
		return nil, e
	}
	m, err := c.read(ctx)
	if err != nil {
		return fail(fmt.Errorf("Home Assistant 没有回应：%s", scrub(err.Error(), token)))
	}
	if m.Type != "auth_required" {
		return fail(fmt.Errorf("这个地址不像 Home Assistant 的 WebSocket（先收到了 %q）", m.Type))
	}
	if err := c.write(ctx, map[string]any{"type": "auth", "access_token": token}); err != nil {
		return fail(fmt.Errorf("发送认证失败：%s", scrub(err.Error(), token)))
	}
	m, err = c.read(ctx)
	if err != nil {
		return fail(fmt.Errorf("认证时断开了：%s", scrub(err.Error(), token)))
	}
	switch m.Type {
	case "auth_ok":
		c.Version = m.HAVersion
	case "auth_invalid":
		return fail(ErrAuth)
	default:
		return fail(fmt.Errorf("认证得到意外的回复 %q", m.Type))
	}
	go c.loop()
	return c, nil
}

func (c *Client) read(ctx context.Context) (msg, error) {
	_, b, err := c.conn.Read(ctx)
	if err != nil {
		return msg{}, err
	}
	var m msg
	if err := json.Unmarshal(b, &m); err != nil {
		return msg{}, fmt.Errorf("看不懂的消息：%v", err)
	}
	return m, nil
}

func (c *Client) write(ctx context.Context, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.conn.Write(ctx, websocket.MessageText, b)
}

// loop reads every message after authentication and hands it to whoever waits for its id.
// HA may batch several messages into one frame as a JSON array.
func (c *Client) loop() {
	var err error
	for {
		var b []byte
		_, b, err = c.conn.Read(context.Background())
		if err != nil {
			break
		}
		var batch []msg
		if bytes.HasPrefix(bytes.TrimSpace(b), []byte("[")) {
			if json.Unmarshal(b, &batch) != nil {
				continue
			}
		} else {
			var m msg
			if json.Unmarshal(b, &m) != nil {
				continue
			}
			batch = []msg{m}
		}
		for _, m := range batch {
			c.route(m)
		}
	}
	c.mu.Lock()
	c.err = fmt.Errorf("和 Home Assistant 的连接断了：%s", scrub(err.Error(), c.token))
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	for id, ch := range c.subs {
		close(ch)
		delete(c.subs, id)
	}
	c.mu.Unlock()
	close(c.done)
}

func (c *Client) route(m msg) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch m.Type {
	case "result", "pong":
		if ch, ok := c.pending[m.ID]; ok {
			ch <- m
			delete(c.pending, m.ID)
		}
	case "event":
		if ch, ok := c.subs[m.ID]; ok {
			select {
			case ch <- m.Event:
			default: // a slow reader loses events rather than blocking the socket
			}
		}
	}
}

// Done is closed when the connection ends; Err says why.
func (c *Client) Done() <-chan struct{} { return c.done }

func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Close ends the connection.
func (c *Client) Close() error { return c.conn.Close(websocket.StatusNormalClosure, "") }

// command sends a numbered command and waits for its result.
func (c *Client) command(ctx context.Context, typ string, fields map[string]any) (json.RawMessage, error) {
	ch := make(chan msg, 1)
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return nil, c.err
	}
	c.next++
	id := c.next
	c.pending[id] = ch
	c.mu.Unlock()
	body := map[string]any{"id": id, "type": typ}
	for k, v := range fields {
		body[k] = v
	}
	if err := c.write(ctx, body); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("发送 %s 失败：%s", typ, scrub(err.Error(), c.token))
	}
	select {
	case m, ok := <-ch:
		if !ok {
			return nil, c.Err()
		}
		if m.Type == "pong" {
			return nil, nil
		}
		if !m.Success {
			if m.Error != nil {
				return nil, fmt.Errorf("Home Assistant 拒绝了 %s：%s（%s）", typ, m.Error.Message, m.Error.Code)
			}
			return nil, fmt.Errorf("Home Assistant 拒绝了 %s", typ)
		}
		return m.Result, nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	case <-c.done:
		return nil, c.Err()
	}
}

// Ping checks the connection is alive.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.command(ctx, "ping", nil)
	return err
}

// State is one entity's state.
type State struct {
	EntityID    string         `json:"entity_id"`
	State       string         `json:"state"`
	Attributes  map[string]any `json:"attributes"`
	LastChanged string         `json:"last_changed"`
}

func (c *Client) GetStates(ctx context.Context) ([]State, error) {
	r, err := c.command(ctx, "get_states", nil)
	if err != nil {
		return nil, err
	}
	var out []State
	return out, json.Unmarshal(r, &out)
}

// HAConfig is the part of get_config the twin shows.
type HAConfig struct {
	Version      string `json:"version"`
	LocationName string `json:"location_name"`
	UnitSystem   struct {
		Temperature string `json:"temperature"`
	} `json:"unit_system"`
}

func (c *Client) GetConfig(ctx context.Context) (HAConfig, error) {
	var out HAConfig
	r, err := c.command(ctx, "get_config", nil)
	if err != nil {
		return out, err
	}
	return out, json.Unmarshal(r, &out)
}

type Area struct {
	AreaID string `json:"area_id"`
	Name   string `json:"name"`
}

type DeviceEntry struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	NameByUser string `json:"name_by_user"`
	AreaID     string `json:"area_id"`
}

type EntityEntry struct {
	EntityID     string `json:"entity_id"`
	DeviceID     string `json:"device_id"`
	AreaID       string `json:"area_id"`
	Name         string `json:"name"`
	OriginalName string `json:"original_name"`
	Platform     string `json:"platform"`
	DisabledBy   string `json:"disabled_by"`
	HiddenBy     string `json:"hidden_by"`
}

func list[T any](ctx context.Context, c *Client, typ string) ([]T, error) {
	r, err := c.command(ctx, typ, nil)
	if err != nil {
		return nil, err
	}
	var out []T
	return out, json.Unmarshal(r, &out)
}

func (c *Client) AreaRegistry(ctx context.Context) ([]Area, error) {
	return list[Area](ctx, c, "config/area_registry/list")
}
func (c *Client) DeviceRegistry(ctx context.Context) ([]DeviceEntry, error) {
	return list[DeviceEntry](ctx, c, "config/device_registry/list")
}
func (c *Client) EntityRegistry(ctx context.Context) ([]EntityEntry, error) {
	return list[EntityEntry](ctx, c, "config/entity_registry/list")
}

// StateChange is one state_changed event.
type StateChange struct {
	EntityID string `json:"entity_id"`
	NewState *State `json:"new_state"`
	OldState *State `json:"old_state"`
}

// SubscribeStateChanged delivers every state_changed event until the connection ends.
func (c *Client) SubscribeStateChanged(ctx context.Context) (<-chan StateChange, error) {
	raw := make(chan json.RawMessage, 512)
	ch := make(chan msg, 1)
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return nil, c.err
	}
	c.next++
	id := c.next
	c.pending[id] = ch
	c.subs[id] = raw
	c.mu.Unlock()
	if err := c.write(ctx, map[string]any{"id": id, "type": "subscribe_events", "event_type": "state_changed"}); err != nil {
		return nil, fmt.Errorf("订阅状态变化失败：%s", scrub(err.Error(), c.token))
	}
	select {
	case m, ok := <-ch:
		if !ok {
			return nil, c.Err()
		}
		if !m.Success {
			return nil, errors.New("Home Assistant 不让订阅状态变化")
		}
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	out := make(chan StateChange, 512)
	go func() {
		defer close(out)
		for b := range raw {
			var ev struct {
				EventType string      `json:"event_type"`
				Data      StateChange `json:"data"`
			}
			if json.Unmarshal(b, &ev) == nil && ev.Data.EntityID != "" {
				out <- ev.Data
			}
		}
	}()
	return out, nil
}

// CallService runs a service (light.turn_on) on a target ({"entity_id": "light.x"}).
func (c *Client) CallService(ctx context.Context, domain, service string, data, target map[string]any) error {
	f := map[string]any{"domain": domain, "service": service}
	if len(data) > 0 {
		f["service_data"] = data
	}
	if len(target) > 0 {
		f["target"] = target
	}
	_, err := c.command(ctx, "call_service", f)
	return err
}

// REST is the one-shot HTTP API, for when the socket is down.
type REST struct {
	Base  string
	Token string
	HTTP  *http.Client
}

func (r REST) do(ctx context.Context, method, path string, body any) ([]byte, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(r.Base, "/")+path, rd)
	if err != nil {
		return nil, fmt.Errorf("Home Assistant 地址不对：%q", r.Base)
	}
	req.Header.Set("Authorization", "Bearer "+r.Token)
	req.Header.Set("Content-Type", "application/json")
	hc := r.HTTP
	if hc == nil {
		hc = httpClient()
	}
	res, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("连不上 Home Assistant：%s", scrub(err.Error(), r.Token))
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	switch {
	case res.StatusCode == 401 || res.StatusCode == 403:
		return nil, ErrAuth
	case res.StatusCode >= 300:
		return nil, fmt.Errorf("Home Assistant 回了 %d：%s", res.StatusCode, scrub(strings.TrimSpace(string(b)), r.Token))
	}
	return b, nil
}

// CallService posts to /api/services/<domain>/<service>; the target goes into the body.
func (r REST) CallService(ctx context.Context, domain, service string, data, target map[string]any) error {
	body := map[string]any{}
	for k, v := range data {
		body[k] = v
	}
	for k, v := range target {
		body[k] = v
	}
	_, err := r.do(ctx, http.MethodPost, "/api/services/"+domain+"/"+service, body)
	return err
}

// State reads /api/states/<entity_id>.
func (r REST) State(ctx context.Context, entity string) (State, error) {
	var s State
	b, err := r.do(ctx, http.MethodGet, "/api/states/"+entity, nil)
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal(b, &s)
}

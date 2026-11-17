package assistant

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Conversation is one chat with the assistant, kept with every step it took.
type Conversation struct {
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	Created  time.Time `json:"created"`
	Updated  time.Time `json:"updated"`
	Messages []Message `json:"messages"`
}

// Message is one turn: what the person asked, or what the assistant answered and did.
type Message struct {
	Role   string  `json:"role"` // user | assistant
	Text   string  `json:"text"`
	Steps  []Step  `json:"steps,omitempty"`
	Places []Place `json:"places,omitempty"`
	// Approvals are what it asked a person to confirm.
	Approvals []string `json:"approvals,omitempty"`
	// Status: done, running, failed, stopped.
	Status string    `json:"status"`
	T      time.Time `json:"t"`
}

// Step is one tool the assistant used.
type Step struct {
	Tool   string         `json:"tool"`
	Label  string         `json:"label"`
	Args   map[string]any `json:"args,omitempty"`
	Result any            `json:"result,omitempty"`
}

// Summary is a conversation without its messages, for the list.
type Summary struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Updated time.Time `json:"updated"`
	Count   int       `json:"count"`
}

// ErrNotFound is returned for a conversation that is not there.
var ErrNotFound = errors.New("没有这段对话")

// store keeps the conversations in one JSON file.
type store struct {
	path  string
	mu    sync.Mutex
	convs map[string]*Conversation
}

func openStore(dir string) (*store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &store{path: filepath.Join(dir, "conversations.json"), convs: map[string]*Conversation{}}
	b, err := os.ReadFile(s.path)
	switch {
	case err == nil:
		var list []*Conversation
		if err := json.Unmarshal(b, &list); err != nil {
			return nil, err
		}
		for _, c := range list {
			s.convs[c.ID] = c
		}
	case !errors.Is(err, os.ErrNotExist):
		return nil, err
	}
	return s, nil
}

// save writes every conversation; the caller holds s.mu.
func (s *store) save() error {
	list := make([]*Conversation, 0, len(s.convs))
	for _, c := range s.convs {
		list = append(list, c)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Created.Before(list[j].Created) })
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func newID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return "c" + hex.EncodeToString(b)
}

func (s *store) create(title string) (Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	c := &Conversation{ID: newID(), Title: title, Created: now, Updated: now, Messages: []Message{}}
	s.convs[c.ID] = c
	return *c, s.save()
}

func (s *store) list() []Summary {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Summary, 0, len(s.convs))
	for _, c := range s.convs {
		out = append(out, Summary{ID: c.ID, Title: c.Title, Updated: c.Updated, Count: len(c.Messages)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out
}

func (s *store) get(id string) (Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.convs[id]
	if !ok {
		return Conversation{}, ErrNotFound
	}
	out := *c
	out.Messages = append([]Message{}, c.Messages...)
	return out, nil
}

func (s *store) remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.convs[id]; !ok {
		return ErrNotFound
	}
	delete(s.convs, id)
	return s.save()
}

// add appends a message and returns its index.
func (s *store) add(id string, m Message) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.convs[id]
	if !ok {
		return 0, ErrNotFound
	}
	if m.T.IsZero() {
		m.T = time.Now()
	}
	c.Messages = append(c.Messages, m)
	c.Updated = m.T
	// the first question names the conversation
	if m.Role == "user" && (c.Title == "" || c.Title == "新对话") {
		r := []rune(m.Text)
		if len(r) > 24 {
			r = append(r[:24], '…')
		}
		c.Title = string(r)
	}
	return len(c.Messages) - 1, s.save()
}

// finish sets what an answer turned out to be.
func (s *store) finish(id string, i int, text string, steps []Step, places []Place, approvals []string, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.convs[id]
	if !ok || i >= len(c.Messages) {
		return ErrNotFound
	}
	m := &c.Messages[i]
	m.Text, m.Steps, m.Places, m.Approvals, m.Status = text, steps, places, approvals, status
	c.Updated = time.Now()
	return s.save()
}

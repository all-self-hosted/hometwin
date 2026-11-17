package layout

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"hometwin/internal/ikea"
)

// Store keeps each home's layout and furniture library as JSON files in one directory.
type Store struct {
	dir string
	mu  sync.Mutex
}

func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

// ErrStale is returned when a layout is saved over a newer one.
var ErrStale = errors.New("布局已经被别处改过了，请刷新后再改")

func (s *Store) path(kind, homeID string) string {
	return filepath.Join(s.dir, fmt.Sprintf("%s-%s.json", kind, homeID))
}

func (s *Store) read(path string, v any) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func (s *Store) write(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Layout is the saved layout of a home (empty when nothing was saved yet).
func (s *Store) Layout(homeID string) (Layout, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l := Layout{Home: homeID, Items: []Item{}}
	err := s.read(s.path("layout", homeID), &l)
	if l.Items == nil {
		l.Items = []Item{}
	}
	return l, err
}

// Save replaces a home's layout. base is the revision the change was made on; a save on an
// older revision than the stored one is refused unless base is negative.
func (s *Store) Save(l Layout, base int) (Layout, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var cur Layout
	if err := s.read(s.path("layout", l.Home), &cur); err != nil {
		return cur, err
	}
	if base >= 0 && base < cur.Rev {
		return cur, ErrStale
	}
	l.Rev = cur.Rev + 1
	if l.Items == nil {
		l.Items = []Item{}
	}
	return l, s.write(s.path("layout", l.Home), l)
}

// Library is the furniture imported or typed in for a home, newest first.
func (s *Store) Library(homeID string) ([]ikea.Product, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []ikea.Product{}
	err := s.read(s.path("library", homeID), &out)
	return out, err
}

// AddToLibrary puts a product at the top of a home's library, replacing one from the same URL.
func (s *Store) AddToLibrary(homeID string, p ikea.Product) ([]ikea.Product, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var lib []ikea.Product
	if err := s.read(s.path("library", homeID), &lib); err != nil {
		return nil, err
	}
	out := []ikea.Product{p}
	for _, q := range lib {
		if p.URL != "" && q.URL == p.URL {
			continue
		}
		out = append(out, q)
	}
	return out, s.write(s.path("library", homeID), out)
}

// RemoveFromLibrary drops the n-th product of the library.
func (s *Store) RemoveFromLibrary(homeID string, n int) ([]ikea.Product, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var lib []ikea.Product
	if err := s.read(s.path("library", homeID), &lib); err != nil {
		return nil, err
	}
	if n < 0 || n >= len(lib) {
		return lib, fmt.Errorf("家具库里没有第 %d 件", n+1)
	}
	lib = append(lib[:n], lib[n+1:]...)
	return lib, s.write(s.path("library", homeID), lib)
}

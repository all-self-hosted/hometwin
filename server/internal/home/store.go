package home

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// file is where the home in use is kept when it is not the sample (an imported floor plan).
func file(dir string) string { return filepath.Join(dir, "home.json") }

// Load is the home in use: the one kept in dir, or the sample when none is.
func Load(dir string) (Home, error) {
	b, err := os.ReadFile(file(dir))
	if errors.Is(err, os.ErrNotExist) {
		return Sample(), nil
	}
	if err != nil {
		return Home{}, err
	}
	var h Home
	if err := json.Unmarshal(b, &h); err != nil {
		return Home{}, fmt.Errorf("%s: %w", file(dir), err)
	}
	if errs := h.Check(); len(errs) > 0 {
		return Home{}, fmt.Errorf("%s: %v", file(dir), errs)
	}
	return h, nil
}

// Use makes h the home in use; the sample is used by forgetting any other.
func Use(dir string, h Home) error {
	if h.ID == Sample().ID {
		if err := os.Remove(file(dir)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if errs := h.Check(); len(errs) > 0 {
		return fmt.Errorf("home: %v", errs)
	}
	b, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return err
	}
	tmp := file(dir) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, file(dir))
}

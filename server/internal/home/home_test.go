package home

import "testing"

func TestSample(t *testing.T) {
	h := Sample()
	if errs := h.Check(); len(errs) > 0 {
		t.Fatal(errs)
	}
	total := 0.0
	for _, r := range h.Rooms {
		a := Area(r.Poly)
		total += a
		t.Logf("%s %.1f m²", r.Name, a)
	}
	if total < 70 || total > 89 {
		t.Fatalf("rooms add up to %.1f m²", total)
	}
	if r := h.RoomAt(P{1.5, 6}); r == nil || r.ID != "master" {
		t.Fatalf("(1.5,6) is in %v", r)
	}
	if _, ok := h.EntryDoor(); !ok {
		t.Fatal("no entry door")
	}
}

func TestLoadUse(t *testing.T) {
	dir := t.TempDir()
	h, err := Load(dir)
	if err != nil || h.ID != Sample().ID {
		t.Fatalf("empty dir: %v %v", h.ID, err)
	}
	other := Sample()
	other.ID, other.Name = "other", "别的家"
	if err := Use(dir, other); err != nil {
		t.Fatal(err)
	}
	if h, _ := Load(dir); h.ID != "other" {
		t.Fatalf("got %s", h.ID)
	}
	if err := Use(dir, Sample()); err != nil {
		t.Fatal(err)
	}
	if h, _ := Load(dir); h.ID != Sample().ID {
		t.Fatalf("got %s after going back", h.ID)
	}
}

package ontology

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"

	"hometwin/internal/devices"
	"hometwin/internal/home"
	"hometwin/internal/layout"
)

// Store is a home's knowledge graph: one CortexDB file, loaded again whenever the home, its
// furniture or its devices' placement change, inferred from once per load.
type Store struct {
	path string

	mu       sync.RWMutex
	db       *cortexdb.DB
	hash     string
	fs       *facts
	given    map[string]bool // the facts as given at the last load
	explicit int
	inferred int
	// structural (SHACL) and layout violations as of the last load
	structural []Violation
	layoutV    []Violation
	// the live readings and since when each live constraint has been broken, per sensor
	devs   map[string]devices.Device
	since  map[string]time.Time
	live   []Violation
	liveOn map[string][]string // constraint id → sensor node ids it applies to
}

// Violation is something that breaks one of the ontology's constraints.
type Violation struct {
	Constraint string `json:"constraint"`
	// Kind: structural (SHACL), live (a reading), layout (where furniture stands).
	Kind string `json:"kind"`
	// Node is the thing that breaks it (room:bath, item:3, device:sensor-bath).
	Node string `json:"node"`
	Name string `json:"name"`
	Text string `json:"text"`
	// Why says which rule and which facts.
	Why   string   `json:"why"`
	Value *float64 `json:"value,omitempty"`
	Since int64    `json:"since,omitempty"`
}

// Open opens (or creates) the knowledge graph file at path. Nothing is in it until Sync.
func Open(ctx context.Context, path string) (*Store, error) {
	s := &Store{path: path, devs: map[string]devices.Device{}, since: map[string]time.Time{}, liveOn: map[string][]string{}}
	return s, nil
}

func openDB(path string) (*cortexdb.DB, error) {
	db, err := cortexdb.Open(cortexdb.DefaultConfig(path), cortexdb.WithAutoInference(false))
	if err != nil {
		return nil, err
	}
	// what is asked of it is what it was given and what follows from that, not the property
	// graph CortexDB keeps beside every RDF term
	db.Graph().SetPropertyGraphProjection(false)
	return db, nil
}

func removeDB(path string) {
	for _, f := range []string{path, path + "-wal", path + "-shm"} {
		os.Remove(f)
	}
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db != nil {
		err := s.db.Close()
		s.db = nil
		return err
	}
	return nil
}

// Sync loads the home again when anything the graph holds changed: a fresh file, the schema
// and the facts, inference run once (explicitly, never in the background), the structural
// constraints checked with SHACL and the layout's own checks recorded.
func (s *Store) Sync(ctx context.Context, h home.Home, l layout.Layout, devs []devices.Device) error {
	fs := compile(h, l, devs)
	raw, _ := json.Marshal(fs.triples)
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])

	lv := layoutViolations(h, l, fs)

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range devs {
		s.devs[d.ID] = d
	}
	s.layoutV = lv
	if s.db != nil && s.hash == hash {
		return nil
	}
	t0 := time.Now()
	db := s.db
	if db == nil {
		// a fresh file with the ontology itself, then the facts
		removeDB(s.path)
		var err error
		if db, err = openDB(s.path); err != nil {
			return err
		}
		if _, err := db.UpsertKnowledgeGraph(ctx, cortexdb.KnowledgeGraphUpsertRequest{Triples: append(schema(), fs.triples...)}); err != nil {
			db.Close()
			return err
		}
	} else {
		// only what changed: facts no longer true go, new ones come in
		now := map[string]bool{}
		var added []graph.RDFTriple
		for _, t := range fs.triples {
			k := tripleKey(t)
			now[k] = true
			if !s.given[k] {
				added = append(added, t)
			}
		}
		var removed []graph.RDFTriple
		for _, t := range s.fs.triples {
			if !now[tripleKey(t)] {
				removed = append(removed, t)
			}
		}
		if len(removed) > 0 {
			if _, err := db.DeleteKnowledgeGraph(ctx, cortexdb.KnowledgeGraphDeleteRequest{Triples: removed}); err != nil {
				return err
			}
		}
		if len(added) > 0 {
			if _, err := db.UpsertKnowledgeGraph(ctx, cortexdb.KnowledgeGraphUpsertRequest{Triples: added}); err != nil {
				return err
			}
		}
	}
	tUp := time.Since(t0)
	// inference runs here, once, over everything given: never in the background
	inf, err := db.RefreshKnowledgeGraphInference(ctx, cortexdb.KnowledgeGraphInferenceRefreshRequest{Mode: cortexdb.KnowledgeGraphInferenceRefreshModeFull})
	if err != nil {
		return err
	}
	tInf := time.Since(t0)
	rep, err := db.ValidateKnowledgeGraphSHACL(ctx, cortexdb.KnowledgeGraphSHACLValidateRequest{Shapes: shapes()})
	if err != nil {
		return err
	}
	SyncTiming = fmt.Sprintf("facts %v, inference %v, SHACL %v", tUp.Round(time.Millisecond), (tInf - tUp).Round(time.Millisecond), (time.Since(t0) - tInf).Round(time.Millisecond))
	s.given = map[string]bool{}
	for _, t := range fs.triples {
		s.given[tripleKey(t)] = true
	}
	s.db, s.hash, s.fs = db, hash, fs
	s.explicit, s.inferred = inf.Result.ExplicitCount, inf.Result.InferredCount
	s.structural = structural(rep.Report, fs)
	// which sensors each live constraint watches: of its class, or measuring a room of it
	s.liveOn = map[string][]string{}
	for _, c := range Constraints {
		if c.Field == "" {
			continue
		}
		for id, class := range fs.types {
			if !strings.HasPrefix(id, "device:") || fs.props[id]["kind"] != devices.Sensor || class == "" {
				continue
			}
			if s.isA(ctx, id, c.Class) {
				s.liveOn[c.ID] = append(s.liveOn[c.ID], id)
				continue
			}
			for _, room := range s.objects(ctx, id, "measures") {
				if s.isA(ctx, room, c.Class) {
					s.liveOn[c.ID] = append(s.liveOn[c.ID], id)
					break
				}
			}
		}
	}
	return nil
}

// SyncTiming is how long the phases of the last reload took.
var SyncTiming string

func tripleKey(t graph.RDFTriple) string {
	return t.Subject.Value + "\x00" + t.Predicate.Value + "\x00" + t.Object.String()
}

// Stats are how many facts were given and how many inferred at the last load.
func (s *Store) Stats() (explicit, inferred int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.explicit, s.inferred
}

// structural is what SHACL found, as people read it.
func structural(rep graph.SHACLReport, fs *facts) []Violation {
	out := []Violation{}
	seen := map[string]bool{}
	for _, r := range rep.Results {
		focus, _ := idOf(r.FocusNode)
		var c *Constraint
		for i := range Constraints {
			if k := &Constraints[i]; k.Path != "" && r.Source.Kind == graph.RDFTermBlankNode && r.Source.Value == fmt.Sprintf("shape%d", i) {
				c = k
			}
		}
		if c == nil || focus == "" {
			continue
		}
		name := fs.names[focus]
		text := strings.ReplaceAll(c.Say, "{name}", name)
		why := fmt.Sprintf("约束「%s」：每个%s都要有「%s」%s", c.ID, classLabel(c.Class), relLabel(c.Path), classLabel(c.ValueClass))
		if c.Inverse {
			why = fmt.Sprintf("约束「%s」：每个%s都要被至少一个%s「%s」", c.ID, classLabel(c.Class), classLabel(c.ValueClass), relLabel(c.Path))
		}
		why += fmt.Sprintf("；%s 是%s，但图里找不到这样的事实", name, classLabel(fs.types[focus]))
		if k := c.ID + "\x00" + focus; !seen[k] {
			seen[k] = true
			out = append(out, Violation{Constraint: c.ID, Kind: "structural", Node: focus, Name: name, Text: text, Why: why})
		}
	}
	return out
}

// layoutViolations are the layout's own checks (aisles, door swings, collisions …), attached to
// the furniture they are about.
func layoutViolations(h home.Home, l layout.Layout, fs *facts) []Violation {
	out := []Violation{}
	rule := map[string]string{
		"aisle": "通道至少 80 cm", "door": "门的开启范围不能挡", "collide": "家具不能重叠", "wall": "家具不能进墙",
		"window": "高家具不挡窗", "outside": "家具要在房间里", "entry": "搬得进入户门",
	}
	for _, w := range layout.Check(h, l) {
		if w.Level != "warn" {
			continue
		}
		id := "item:" + w.Item
		c := rule[w.Kind]
		if c == "" {
			c = w.Kind
		}
		out = append(out, Violation{Constraint: c, Kind: "layout", Node: id, Name: fs.names[id], Text: fs.names[id] + "：" + w.Text,
			Why: fmt.Sprintf("约束「%s」：按户型的墙、门和家具的真实尺寸算出来的", c)})
	}
	return out
}

// Observe takes the devices' current readings and says which live constraints are broken: a
// reading out of its limit for at least the constraint's time.
func (s *Store) Observe(devs []devices.Device, now time.Time) []Violation {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range devs {
		s.devs[d.ID] = d
	}
	out := []Violation{}
	for _, c := range Constraints {
		if c.Field == "" {
			continue
		}
		for _, sid := range s.liveOn[c.ID] {
			d, ok := s.devs[strings.TrimPrefix(sid, "device:")]
			if !ok {
				continue
			}
			v, ok := d.State[c.Field].(float64)
			key := c.ID + "\x00" + sid
			bad := ok && ((c.Max != nil && v > *c.Max) || (c.Min != nil && v < *c.Min))
			if !bad {
				delete(s.since, key)
				continue
			}
			at, ok := s.since[key]
			if !ok {
				s.since[key], at = now, now
			}
			held := now.Sub(at).Seconds()
			if held < c.For {
				continue
			}
			room := d.Room
			if s.fs != nil {
				if n, ok := s.fs.names["room:"+d.Room]; ok {
					room = n
				}
			}
			text := strings.NewReplacer("{room}", room, "{name}", d.Name, "{value}", fmt.Sprintf("%.0f", v), "{minutes}", fmt.Sprintf("%d", int(math.Max(1, math.Floor(held/60))))).Replace(c.Say)
			limit := ""
			if c.Max != nil {
				limit = fmt.Sprintf("高于 %g", *c.Max)
			} else if c.Min != nil {
				limit = fmt.Sprintf("低于 %g", *c.Min)
			}
			why := fmt.Sprintf("约束「%s」：%s 测量 %s，%s 是%s；读数 %s %s 持续 %.0f 秒以上", c.ID, d.Name, room, room, classLabel(c.Class), c.Field, limit, c.For)
			if s.fs != nil && s.fs.types[sid] == c.Class {
				why = fmt.Sprintf("约束「%s」：%s 是%s；读数 %s %s 持续 %.0f 秒以上", c.ID, d.Name, classLabel(c.Class), c.Field, limit, c.For)
			}
			val := v
			out = append(out, Violation{Constraint: c.ID, Kind: "live", Node: sid, Name: d.Name, Text: text, Why: why, Value: &val, Since: at.UnixMilli()})
		}
	}
	s.live = out
	return out
}

// Check is every constraint broken now: structural (SHACL, as of the last load), layout, and
// live (as of the last Observe).
func (s *Store) Check(ctx context.Context) ([]Violation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := append([]Violation{}, s.structural...)
	out = append(out, s.layoutV...)
	return append(out, s.live...), nil
}

// ---- reading the graph (callers hold s.mu) ----

func (s *Store) find(ctx context.Context, sub, pred, obj *graph.RDFTerm) []graph.RDFTriple {
	if s.db == nil {
		return nil
	}
	r, err := s.db.FindKnowledgeGraph(ctx, cortexdb.KnowledgeGraphFindRequest{Pattern: graph.TriplePattern{Subject: sub, Predicate: pred, Object: obj, Graph: &homeGraph}})
	if err != nil {
		return nil
	}
	return r.Triples
}

// objects are the things id relates to over rel (inferred ones included), without itself.
func (s *Store) objects(ctx context.Context, id, rel string) []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range s.find(ctx, ref(thing(id)), ref(ht(rel)), nil) {
		if o, ns := idOf(t.Object); ns == nsThing && o != id && !seen[o] {
			seen[o] = true
			out = append(out, o)
		}
	}
	return out
}

// subjects are the things relating to id over rel, without itself.
func (s *Store) subjects(ctx context.Context, rel, id string) []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range s.find(ctx, nil, ref(ht(rel)), ref(thing(id))) {
		if o, ns := idOf(t.Subject); ns == nsThing && o != id && !seen[o] {
			seen[o] = true
			out = append(out, o)
		}
	}
	return out
}

// isA says whether id is of class (subclasses count: the inference typed it).
func (s *Store) isA(ctx context.Context, id, class string) bool {
	return len(s.find(ctx, ref(thing(id)), &iriType, ref(ht(class)))) > 0
}

// ofClass is everything of a class.
func (s *Store) ofClass(ctx context.Context, class string) []string {
	var out []string
	for _, t := range s.find(ctx, nil, &iriType, ref(ht(class))) {
		if o, ns := idOf(t.Subject); ns == nsThing {
			out = append(out, o)
		}
	}
	return out
}

func (s *Store) name(id string) string {
	if s.fs == nil {
		return id
	}
	if n, ok := s.fs.names[id]; ok {
		return n
	}
	return id
}

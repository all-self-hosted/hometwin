package ontology

import (
	"context"
	"sort"
)

// Node is one thing of the home, typed with the English name of its most specific class
// (Room, Bedroom, Light, Furniture …, the ht: local name) and Props holding its Chinese class
// label (classLabel) and its properties.
type Node struct {
	ID    string         `json:"id"`
	Type  string         `json:"type"`
	Name  string         `json:"name"`
	Props map[string]any `json:"props,omitempty"`
}

// Edge is one fact between two things, given or inferred.
type Edge struct {
	From  string         `json:"from"`
	Rel   string         `json:"rel"`
	To    string         `json:"to"`
	Props map[string]any `json:"props,omitempty"`
}

// Graph is every thing and every fact between things, inferred ones marked.
func (s *Store) Graph(ctx context.Context) ([]Node, []Edge, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return []Node{}, []Edge{}, nil
	}
	nodes := []Node{}
	for _, id := range sortedKeys(s.fs.names) {
		props := map[string]any{"classLabel": classLabel(s.fs.types[id])}
		for k, v := range s.fs.props[id] {
			props[k] = v
		}
		if st := s.state(id); st != nil {
			props["state"] = st
		}
		nodes = append(nodes, Node{ID: id, Type: s.fs.types[id], Name: s.fs.names[id], Props: props})
	}
	edges := []Edge{}
	for _, t := range s.find(ctx, nil, nil, nil) {
		from, fns := idOf(t.Subject)
		rel, rns := idOf(t.Predicate)
		to, tns := idOf(t.Object)
		if fns != nsThing || rns != NS || tns != nsThing || from == to {
			continue
		}
		edges = append(edges, Edge{From: from, Rel: rel, To: to, Props: map[string]any{"label": relLabel(rel), "inferred": t.Inferred}})
	}
	sort.SliceStable(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		return edges[i].Rel < edges[j].Rel
	})
	return nodes, edges, nil
}

// JSONLD is the home's graph as JSON-LD: every thing with all its classes (RealEstateCore's
// among them, by inference) and its relations, inferred ones included.
func (s *Store) JSONLD(ctx context.Context) (map[string]any, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := []map[string]any{}
	at := map[string]map[string]any{}
	if s.db != nil {
		for _, id := range sortedKeys(s.fs.names) {
			o := map[string]any{"@id": "ht:thing/" + id, "rdfs:label": s.fs.names[id]}
			for k, v := range s.fs.props[id] {
				o["ht:"+k] = v
			}
			items = append(items, o)
			at[id] = o
		}
		for _, t := range s.find(ctx, nil, nil, nil) {
			from, fns := idOf(t.Subject)
			o := at[from]
			if fns != nsThing || o == nil {
				continue
			}
			to, tns := idOf(t.Object)
			switch {
			case t.Predicate.Value == iriType.Value && (tns == NS || tns == nsREC):
				prefix := "ht:"
				if tns == nsREC {
					prefix = "rec:"
				}
				types, _ := o["@type"].([]string)
				o["@type"] = append(types, prefix+to)
			case tns == nsThing:
				rel, rns := idOf(t.Predicate)
				if rns != NS || from == to {
					continue
				}
				k := "ht:" + rel
				list, _ := o[k].([]map[string]string)
				o[k] = append(list, map[string]string{"@id": "ht:thing/" + to})
			}
		}
	}
	return map[string]any{
		"@context": map[string]string{"ht": NS, "rec": nsREC, "rdfs": nsRDFS},
		"@graph":   items,
	}, nil
}

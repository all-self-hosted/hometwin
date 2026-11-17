package ontology

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// resolve finds a thing by its node id or its name.
func (s *Store) resolve(x string) (string, bool) {
	if s.fs == nil {
		return "", false
	}
	if _, ok := s.fs.names[x]; ok {
		return x, true
	}
	for id, n := range s.fs.names {
		if n == x {
			return id, true
		}
	}
	return "", false
}

// Explain says, in Chinese, why a fact holds: that it was given, or the chain of facts and rules
// it was inferred from ("客厅空调 控制 客餐厅；客餐厅 位于 家 → 客厅空调 能影响 家"). subject and
// object are node ids or names; predicate a relation's id or its Chinese label, or "是" with a
// class for class membership.
func (s *Store) Explain(ctx context.Context, subject, predicate, object string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return "", fmt.Errorf("家的知识图谱还没有加载")
	}
	sub, ok := s.resolve(subject)
	if !ok {
		return "", fmt.Errorf("图里没有「%s」", subject)
	}
	var pred, obj graph.RDFTerm
	var objName string
	if predicate == "是" || predicate == "type" {
		cls := ""
		for _, c := range Classes {
			if c.ID == object || c.Label == object {
				cls = c.ID
			}
		}
		if cls == "" {
			return "", fmt.Errorf("没有「%s」这个类", object)
		}
		pred, obj, objName = iriType, ht(cls), classLabel(cls)
	} else {
		rel, ok := relByLabel(predicate)
		if !ok {
			return "", fmt.Errorf("没有「%s」这种关系", predicate)
		}
		o, ok := s.resolve(object)
		if !ok {
			return "", fmt.Errorf("图里没有「%s」", object)
		}
		pred, obj, objName = ht(rel), thing(o), s.name(o)
		predicate = relLabel(rel)
	}
	ts := s.find(ctx, ref(thing(sub)), &pred, &obj)
	if len(ts) == 0 {
		return fmt.Sprintf("图里没有「%s %s %s」这条事实，也推不出来", s.name(sub), predicate, objName), nil
	}
	t := ts[0]
	fact := s.statement(t)
	if !t.Inferred {
		return fact + "：这是给定的事实（来自户型、布局或设备）", nil
	}
	steps := s.why(ctx, t.ID)
	if len(steps) == 0 {
		return fact + "：这是推理得出的", nil
	}
	return fact + "：因为 " + strings.Join(steps, "；再 "), nil
}

// why is what an inferred fact follows from, a step a line, from what was given to the fact.
func (s *Store) why(ctx context.Context, id string) []string {
	r, err := s.db.ExplainKnowledgeGraphInference(ctx, cortexdb.KnowledgeGraphInferenceExplainRequest{TripleID: id, Depth: 12})
	if err != nil || len(r.Trace) == 0 {
		return nil
	}
	entry := map[string]graph.RDFSInferenceTraceEntry{}
	kids := map[string][]string{}
	for _, e := range r.Trace {
		if _, ok := entry[e.TripleID]; !ok {
			entry[e.TripleID] = e
		}
		if e.ParentTripleID != "" && !slices.Contains(kids[e.ParentTripleID], e.TripleID) {
			kids[e.ParentTripleID] = append(kids[e.ParentTripleID], e.TripleID)
		}
	}
	var steps []string
	seen := map[string]bool{}
	var walk func(id string)
	walk = func(id string) {
		e := entry[id]
		if e.Explanation.Explicit || seen[id] {
			return
		}
		seen[id] = true
		var facts []graph.RDFTriple
		var notes []string
		for _, k := range kids[id] {
			walk(k)
			t := entry[k].Explanation.Triple
			st := s.statement(t)
			switch {
			case st == "":
			case s.chainable(t):
				facts = append(facts, t)
			case !slices.Contains(notes, st):
				notes = append(notes, st)
			}
		}
		concl := s.statement(e.Explanation.Triple)
		if concl == "" || len(facts)+len(notes) == 0 {
			return
		}
		var lead []string
		for at := e.Explanation.Triple.Subject.Value; len(facts) > 0; {
			i := slices.IndexFunc(facts, func(t graph.RDFTriple) bool { return t.Subject.Value == at })
			if i < 0 {
				i = 0
			}
			lead = append(lead, s.statement(facts[i]))
			at = facts[i].Object.Value
			facts = slices.Delete(facts, i, i+1)
		}
		step := strings.Join(lead, "，") + " → " + concl
		if len(lead) == 0 {
			step = strings.Join(notes, "，") + " → " + concl
		} else if len(notes) > 0 {
			step += "（" + strings.Join(notes, "，") + "）"
		}
		if !slices.Contains(steps, step) {
			steps = append(steps, step)
		}
	}
	walk(r.Trace[0].TripleID)
	return steps
}

// chainable: a fact about things or classes, not about the ontology's relations.
func (s *Store) chainable(t graph.RDFTriple) bool {
	if _, ns := idOf(t.Predicate); ns == NS {
		if _, ons := idOf(t.Object); ons == nsThing {
			return true
		}
	}
	_, ons := idOf(t.Object)
	return (t.Predicate.Value == iriType.Value || t.Predicate.Value == iriSubClassOf.Value) && (ons == NS || ons == nsREC)
}

// label is how a term reads: a thing's name, a class's or relation's Chinese label.
func (s *Store) label(t graph.RDFTerm) string {
	id, ns := idOf(t)
	switch ns {
	case nsThing:
		return s.name(id)
	case NS:
		for _, c := range Classes {
			if c.ID == id {
				return c.Label
			}
		}
		return relLabel(id)
	case nsREC:
		return "rec:" + id
	}
	return ""
}

// statement is a triple as people read it ("" for what is only how RDF writes things down).
func (s *Store) statement(t graph.RDFTriple) string {
	sl, ol := s.label(t.Subject), s.label(t.Object)
	p := t.Predicate.Value
	switch {
	case p == iriType.Value && ol != "" && !strings.HasPrefix(t.Object.Value, nsOWL):
		return sl + " 是 " + ol
	case p == iriType.Value && t.Object.Value == nsOWL+"TransitiveProperty":
		return "「" + sl + "」可以传递"
	case p == iriType.Value && t.Object.Value == nsOWL+"SymmetricProperty":
		return "「" + sl + "」是相互的"
	case p == iriSubClassOf.Value && ol != "" && sl != ol:
		return sl + " 是一种 " + ol
	case p == iriSubPropOf.Value && ol != "" && sl != ol:
		return "「" + sl + "」也算「" + ol + "」"
	case p == nsOWL+"inverseOf":
		return "「" + sl + "」反过来是「" + ol + "」"
	case p == nsOWL+"propertyChainAxiom":
		if id := chains()[t.Object.Value]; id != "" {
			return "规则「" + id + "」"
		}
		return "「" + sl + "」的规则"
	}
	if _, ns := idOf(t.Predicate); ns == NS && sl != "" && ol != "" {
		return sl + " " + s.label(t.Predicate) + " " + ol
	}
	return ""
}

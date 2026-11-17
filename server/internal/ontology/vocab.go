// Package ontology is the home as a knowledge graph in CortexDB, under an ontology written in
// RDF: what kinds of things a home has (classes, with Chinese labels and RealEstateCore
// superclasses), how they relate (relations with their OWL characteristics), rules (property
// chains), and constraints (SHACL for the structure, live ones for readings, the layout's own
// checks). The geometry is worked out in Go (which wall a bookcase stands against, which rooms a
// door joins); the facts go into CortexDB, which infers the rest once per load, keeps for every
// inferred fact what it follows from, and answers the questions.
package ontology

import (
	"fmt"
	"net/url"
	"strings"
	"unicode"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

const (
	nsRDF  = "http://www.w3.org/1999/02/22-rdf-syntax-ns#"
	nsRDFS = "http://www.w3.org/2000/01/rdf-schema#"
	nsOWL  = "http://www.w3.org/2002/07/owl#"
	nsSH   = "http://www.w3.org/ns/shacl#"
	nsXSD  = "http://www.w3.org/2001/XMLSchema#"
	nsREC  = "https://w3id.org/rec/"

	// NS is the namespace of HomeTwin's own classes and relations (ht:Room, ht:locatedIn).
	NS      = "urn:hometwin:ont:"
	nsThing = "urn:hometwin:thing:"
	nsShape = "urn:hometwin:shape:"
	// Graph is the named graph everything is in.
	Graph = "urn:hometwin:home"
)

var (
	iriType       = graph.NewIRI(nsRDF + "type")
	iriSubClassOf = graph.NewIRI(nsRDFS + "subClassOf")
	iriSubPropOf  = graph.NewIRI(nsRDFS + "subPropertyOf")
	iriLabel      = graph.NewIRI(nsRDFS + "label")
	homeGraph     = graph.NewIRI(Graph)
)

// Class is one kind of thing.
type Class struct {
	ID    string   // ht:<ID>
	Label string   // 卧室
	Sub   []string // superclasses (ht ids)
	REC   string   // the RealEstateCore class it is a kind of, if any
}

// Classes: 空间 → 家, 房间 (卧室 …), 户外; 建筑构件 → 墙, 墙面, 门, 窗; 家具 → …; 设备 → …;
// 产品; 数据点.
var Classes = []Class{
	{"Space", "空间", nil, "Space"},
	{"Home", "家", []string{"Space"}, "Apartment"},
	{"Room", "房间", []string{"Space"}, "Room"},
	{"Bedroom", "卧室", []string{"Room"}, "Bedroom"},
	{"LivingRoom", "客厅", []string{"Room"}, "LivingRoom"},
	{"Kitchen", "厨房", []string{"Room"}, "Kitchen"},
	{"Bathroom", "卫生间", []string{"Room"}, "Bathroom"},
	{"Balcony", "阳台", []string{"Room"}, "Balcony"},
	{"Entry", "玄关", []string{"Room"}, "Hallway"},
	{"Outside", "户外", []string{"Space"}, ""},
	{"BuildingElement", "建筑构件", nil, "BuildingElement"},
	{"Wall", "墙", []string{"BuildingElement"}, "Wall"},
	{"WallFace", "墙面", []string{"BuildingElement"}, ""},
	{"Door", "门", []string{"BuildingElement"}, "Door"},
	{"Window", "窗", []string{"BuildingElement"}, "Window"},
	{"Furniture", "家具", nil, "Furniture"},
	{"Bed", "床", []string{"Furniture"}, ""},
	{"Sofa", "沙发", []string{"Furniture"}, ""},
	{"Armchair", "扶手椅", []string{"Furniture"}, ""},
	{"Storage", "柜类", []string{"Furniture"}, ""},
	{"Bookcase", "书柜", []string{"Storage"}, ""},
	{"Wardrobe", "衣柜", []string{"Storage"}, ""},
	{"Drawers", "抽屉柜", []string{"Storage"}, ""},
	{"TVBench", "电视柜", []string{"Storage"}, ""},
	{"TableKind", "桌", []string{"Furniture"}, ""},
	{"Desk", "书桌", []string{"TableKind"}, ""},
	{"Table", "桌子", []string{"TableKind"}, ""},
	{"Chair", "椅", []string{"Furniture"}, ""},
	{"Shelf", "搁架", []string{"Furniture"}, ""},
	{"FloorLamp", "灯具", []string{"Furniture"}, ""},
	{"Rug", "地毯", []string{"Furniture"}, ""},
	{"OtherFurniture", "其他家具", []string{"Furniture"}, ""},
	{"Fixture", "固定设施", nil, ""},
	{"Device", "设备", nil, "Equipment"},
	{"Light", "照明", []string{"Device"}, "LightingEquipment"},
	{"AirConditioner", "空调", []string{"Device"}, "HVACEquipment"},
	{"DoorLock", "门锁", []string{"Device"}, "AccessControlEquipment"},
	{"Curtain", "窗帘", []string{"Device"}, "ShadingEquipment"},
	{"RobotVacuum", "扫地机", []string{"Device"}, ""},
	{"Sensor", "传感器", []string{"Device"}, "Sensor"},
	{"FridgeSensor", "冷藏温度计", []string{"Sensor"}, ""},
	{"Product", "产品", nil, ""},
	{"Point", "数据点", nil, "Point"},
}

// Relation is one way things relate.
type Relation struct {
	ID         string
	Label      string
	Transitive bool
	Symmetric  bool
	Inverse    string // ht id of its inverse
	Sub        string // the relation it is a narrower kind of
}

var Relations = []Relation{
	{ID: "locatedIn", Label: "位于", Transitive: true},
	{ID: "partOf", Label: "属于", Transitive: true},
	{ID: "against", Label: "靠墙"},
	{ID: "backAgainst", Label: "背靠", Sub: "against"},
	{ID: "controls", Label: "控制", Inverse: "controlledBy", Sub: "affects"},
	{ID: "controlledBy", Label: "被控制"},
	{ID: "illuminates", Label: "照亮", Sub: "controls"},
	{ID: "cleans", Label: "打扫", Sub: "controls"},
	{ID: "secures", Label: "锁住", Sub: "controls"},
	{ID: "covers", Label: "遮住", Sub: "controls"},
	{ID: "measures", Label: "测量"},
	{ID: "affects", Label: "能影响"},
	{ID: "adjacentTo", Label: "相邻", Symmetric: true},
	{ID: "leadsTo", Label: "通往", Symmetric: true},
	{ID: "connects", Label: "连通", Inverse: "hasDoor"},
	{ID: "hasDoor", Label: "有门"},
	{ID: "bounds", Label: "围合", Inverse: "hasWall"},
	{ID: "hasWall", Label: "有墙"},
	{ID: "faces", Label: "朝向"},
	{ID: "modelOf", Label: "是型号"},
	{ID: "hasPoint", Label: "有数据点"},
	{ID: "mountedOn", Label: "安装在"},
	{ID: "deviceIn", Label: "设备位于", Sub: "locatedIn"},
	{ID: "homeDevice", Label: "是这个家的设备"},
	{ID: "near", Label: "挨着", Symmetric: true},
}

// Rule is a property chain: X If[0] Y, Y If[1] Z … give X Then Z.
type Rule struct {
	ID   string
	If   []string
	Then string
	Note string
}

var Rules = []Rule{
	{"家的设备", []string{"deviceIn", "locatedIn"}, "homeDevice", "设备位于房间、房间位于家，它就是这个家的设备"},
	{"控制所在的房间", []string{"controls", "locatedIn"}, "affects", "控制的东西在哪个房间，就能影响那个房间"},
	{"控制门两边的房间", []string{"controls", "connects"}, "affects", "控制一扇门，就能影响门两边的房间"},
	{"有门就通往", []string{"hasDoor", "connects"}, "leadsTo", "两个房间有同一扇门，就互相通往"},
	{"同墙相邻", []string{"hasWall", "bounds"}, "adjacentTo", "两个房间共用一面墙，就相邻"},
}

// A Constraint is checked on every load (SHACL) or on every reading (live).
type Constraint struct {
	ID string
	// structural: things of Class must have Min values over Path (Inverse: walked backwards) of
	// ValueClass
	Class, Path, ValueClass string
	Inverse                 bool
	Say                     string
	// live: a reading of Field over Max (or under Min) for at least For seconds, on sensors of
	// Class or measuring a room of Class
	Field    string
	Max, Min *float64
	For      float64
}

func f(v float64) *float64 { return &v }

var Constraints = []Constraint{
	{ID: "房间要有照明", Class: "Room", Path: "illuminates", Inverse: true, ValueClass: "Light", Say: "{name}没有照明：本体要求每个房间至少有一盏灯照亮它"},
	{ID: "卧室要有门", Class: "Bedroom", Path: "hasDoor", ValueClass: "Door", Say: "{name}没有门：本体要求每个卧室至少有一扇门通往它"},
	{ID: "卫生间要通风", Class: "Bathroom", Field: "humidity", Max: f(80), For: 120, Say: "{room}湿度 {value}% 已经超过 80% {minutes} 分钟了，开窗或开排风扇通通风"},
	{ID: "冷藏温度", Class: "FridgeSensor", Field: "temp", Max: f(8), For: 300, Say: "{name}温度 {value} ℃，超过 8 ℃ 已经 {minutes} 分钟，检查冰箱门有没有关好"},
}

// esc keeps a name readable in an IRI: letters (Chinese too), digits and -._~ as they are, the
// rest percent-encoded.
func esc(name string) string {
	var b strings.Builder
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("-_.~", r) {
			b.WriteRune(r)
			continue
		}
		for _, c := range []byte(string(r)) {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func ht(id string) graph.RDFTerm         { return graph.NewIRI(NS + id) }
func thing(id string) graph.RDFTerm      { return graph.NewIRI(nsThing + esc(id)) }
func rec(id string) graph.RDFTerm        { return graph.NewIRI(nsREC + id) }
func literal(v string) graph.RDFTerm     { return graph.NewLiteral(v) }
func number(v float64) graph.RDFTerm     { return graph.NewTypedLiteral(fmt.Sprint(v), nsXSD+"decimal") }
func ref(t graph.RDFTerm) *graph.RDFTerm { return &t }

// idOf is the node id (room:living) a thing IRI stands for, or the ht id of a class or relation;
// ns says which.
func idOf(t graph.RDFTerm) (id, ns string) {
	if t.Kind != graph.RDFTermIRI {
		return "", ""
	}
	if rest, ok := strings.CutPrefix(t.Value, nsThing); ok {
		if v, err := url.PathUnescape(rest); err == nil {
			return v, nsThing
		}
		return rest, nsThing
	}
	if rest, ok := strings.CutPrefix(t.Value, NS); ok {
		return rest, NS
	}
	if rest, ok := strings.CutPrefix(t.Value, nsREC); ok {
		return rest, nsREC
	}
	return "", ""
}

func classLabel(id string) string {
	for _, c := range Classes {
		if c.ID == id {
			return c.Label
		}
	}
	return id
}

func relLabel(id string) string {
	for _, r := range Relations {
		if r.ID == id {
			return r.Label
		}
	}
	return id
}

// relByLabel finds a relation by its id or its Chinese label.
func relByLabel(s string) (string, bool) {
	for _, r := range Relations {
		if r.ID == s || r.Label == s {
			return r.ID, true
		}
	}
	return "", false
}

// schema is the ontology itself as triples: classes, relations, rules.
func schema() []graph.RDFTriple {
	var out []graph.RDFTriple
	add := func(s, p, o graph.RDFTerm) {
		out = append(out, graph.RDFTriple{Subject: s, Predicate: p, Object: o, Graph: &homeGraph})
	}
	for _, c := range Classes {
		ci := ht(c.ID)
		add(ci, iriType, graph.NewIRI(nsRDFS+"Class"))
		add(ci, iriLabel, literal(c.Label))
		for _, s := range c.Sub {
			add(ci, iriSubClassOf, ht(s))
		}
		if c.REC != "" {
			add(ci, iriSubClassOf, rec(c.REC))
		}
	}
	for _, r := range Relations {
		ri := ht(r.ID)
		add(ri, iriType, graph.NewIRI(nsOWL+"ObjectProperty"))
		add(ri, iriLabel, literal(r.Label))
		if r.Transitive {
			add(ri, iriType, graph.NewIRI(nsOWL+"TransitiveProperty"))
		}
		if r.Symmetric {
			add(ri, iriType, graph.NewIRI(nsOWL+"SymmetricProperty"))
		}
		if r.Inverse != "" {
			add(ri, graph.NewIRI(nsOWL+"inverseOf"), ht(r.Inverse))
		}
		if r.Sub != "" {
			add(ri, iriSubPropOf, ht(r.Sub))
		}
	}
	for i, r := range Rules {
		cells := make([]graph.RDFTerm, len(r.If))
		for j := range r.If {
			cells[j] = graph.NewBlankNode(fmt.Sprintf("rule%d-%d", i, j))
		}
		add(ht(r.Then), graph.NewIRI(nsOWL+"propertyChainAxiom"), cells[0])
		for j, link := range r.If {
			add(cells[j], graph.NewIRI(nsRDF+"first"), ht(link))
			next := graph.NewIRI(nsRDF + "nil")
			if j+1 < len(cells) {
				next = cells[j+1]
			}
			add(cells[j], graph.NewIRI(nsRDF+"rest"), next)
		}
		add(cells[0], iriLabel, literal(r.ID))
	}
	return out
}

// chains are the rules by the blank node that heads their chain.
func chains() map[string]string {
	out := map[string]string{}
	for i, r := range Rules {
		out[fmt.Sprintf("rule%d-0", i)] = r.ID
	}
	return out
}

// shapes are the structural constraints as SHACL.
func shapes() []graph.RDFTriple {
	var out []graph.RDFTriple
	sh := func(s, p, o graph.RDFTerm) { out = append(out, graph.RDFTriple{Subject: s, Predicate: p, Object: o}) }
	for i, c := range Constraints {
		if c.Path == "" {
			continue
		}
		shape, prop := graph.NewIRI(nsShape+esc(c.ID)), graph.NewBlankNode(fmt.Sprintf("shape%d", i))
		sh(shape, iriType, graph.NewIRI(nsSH+"NodeShape"))
		sh(shape, graph.NewIRI(nsSH+"targetClass"), ht(c.Class))
		sh(shape, graph.NewIRI(nsSH+"property"), prop)
		if c.Inverse {
			path := graph.NewBlankNode(fmt.Sprintf("path%d", i))
			sh(prop, graph.NewIRI(nsSH+"path"), path)
			sh(path, graph.NewIRI(nsSH+"inversePath"), ht(c.Path))
		} else {
			sh(prop, graph.NewIRI(nsSH+"path"), ht(c.Path))
		}
		sh(prop, graph.NewIRI(nsSH+"minCount"), graph.NewTypedLiteral("1", nsXSD+"integer"))
		if c.ValueClass != "" {
			sh(prop, graph.NewIRI(nsSH+"class"), ht(c.ValueClass))
		}
	}
	return out
}

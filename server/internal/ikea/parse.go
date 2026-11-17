package ikea

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Parse reads a product page. pageURL is where it came from (after redirects); it fills URL and,
// when the page has none, the article number.
func Parse(page []byte, pageURL string) (Product, error) {
	p := Product{Source: "ikea", URL: pageURL}
	ld, haveLD := findLDProduct(page)
	cn := parseCN(page)
	og := metaTags(page)

	switch {
	case haveLD:
		p.Name = ldString(ld["name"])
		p.Article = ldString(ld["sku"])
		if p.Article == "" {
			p.Article = ldString(ld["mpn"])
		}
		p.Color = ldString(ld["color"])
		p.Image = ldImage(ld["image"])
		p.Price, p.Currency = ldPrice(ld["offers"])
		if u := ldString(ld["url"]); u != "" && p.URL == "" {
			p.URL = u
		}
	case cn.name != "":
		p.Name = cn.name
		p.Price, p.Currency = cn.price, "CNY"
	default:
		p.Name = og["og:title"]
		if p.Name == "" {
			p.Name = titleTag(page)
		}
	}
	p.Name = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(p.Name), "- IKEA"))
	if p.Image == "" {
		p.Image = og["og:image"]
	}
	if p.Article == "" {
		p.Article = articleFromURL(pageURL)
	}
	p.Family = family(p.Name)

	// What kind of furniture: the name says it best, the category when the name does not.
	category := ldString(ld["category"])
	p.Type = DetectType(typePart(p.Name))
	if p.Type == "box" && category != "" {
		p.Type = DetectType(category)
	}
	if p.Type == "box" {
		p.Type = DetectType(p.Name + " " + og["og:description"])
	}
	p.TypeLabel = TypeLabel(p.Type)

	// Size: the Measurements panel, then the structured data, then the name.
	panel, havePanel := choosePanel(parsePanels(page), ld, p)
	if havePanel {
		p.Measures = panel.measures
		p.Packages = panel.packages
	} else if len(cn.measures) > 0 {
		p.Measures = cn.measures
		p.Packages = cn.packages
	}
	if w, d, h, ok := dimsFromMeasures(p.Measures, p.Type); ok {
		p.W, p.D, p.H = w, d, h
		p.DimsFrom = "measurements"
	} else if w, d, h, ok := dimsFromLD(ld); ok {
		p.W, p.D, p.H = w, d, h
		p.DimsFrom = "jsonld"
	} else if w, d, h, ok := dimsFromName(p.Name+" "+ldString(ld["description"]), p.Type); ok {
		p.W, p.D, p.H = w, d, h
		p.DimsFrom = "name"
	}

	if p.Color == "" {
		p.Color = colorPart(p.Name)
	}
	p.ColorHex = ColorHex(colorSource(p), p.Type)

	if p.Name == "" {
		return p, errors.New("页面上没有找到商品名称，可能不是商品页")
	}
	if p.W <= 0 || p.D <= 0 || p.H <= 0 {
		return p, errors.New("页面上没有找到这件家具的尺寸，可以手动填写长宽高")
	}
	return p, nil
}

// colorSource picks the text the colour is read from: for upholstered pieces the structured
// colour names the fabric; otherwise the colour part of the name is more precise.
func colorSource(p Product) string {
	name := colorPart(p.Name)
	if p.Type == "sofa" || p.Type == "armchair" {
		return p.Color + " " + name
	}
	return name + " " + p.Color
}

// --- JSON-LD ---

var ldRe = regexp.MustCompile(`(?is)<script[^>]*type=["']application/ld\+json["'][^>]*>(.*?)</script>`)

func isProduct(m map[string]any) bool {
	switch t := m["@type"].(type) {
	case string:
		return t == "Product"
	case []any:
		for _, x := range t {
			if s, _ := x.(string); s == "Product" {
				return true
			}
		}
	}
	return false
}

// findLDProduct returns the page's own Product: a top-level one when there is one (category
// listings nest many Products inside an ItemList), else the first found anywhere.
func findLDProduct(page []byte) (map[string]any, bool) {
	var nested map[string]any
	for _, m := range ldRe.FindAllSubmatch(page, -1) {
		var v any
		if json.Unmarshal(bytes.TrimSpace(m[1]), &v) != nil {
			continue
		}
		var tops []any
		switch t := v.(type) {
		case []any:
			tops = t
		case map[string]any:
			tops = []any{t}
			if g, ok := t["@graph"].([]any); ok {
				tops = append(tops, g...)
			}
		}
		for _, x := range tops {
			if o, ok := x.(map[string]any); ok && isProduct(o) {
				return o, true
			}
		}
		if nested == nil {
			nested = firstProduct(v)
		}
	}
	return nested, nested != nil
}

func firstProduct(v any) map[string]any {
	switch t := v.(type) {
	case map[string]any:
		if isProduct(t) {
			return t
		}
		for _, x := range t {
			if p := firstProduct(x); p != nil {
				return p
			}
		}
	case []any:
		for _, x := range t {
			if p := firstProduct(x); p != nil {
				return p
			}
		}
	}
	return nil
}

func ldString(v any) string {
	switch t := v.(type) {
	case string:
		return html.UnescapeString(strings.TrimSpace(t))
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case map[string]any:
		return ldString(t["name"])
	}
	return ""
}

func ldImage(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		for _, x := range t {
			if s := ldImage(x); s != "" {
				return s
			}
		}
	case map[string]any:
		if s := ldString(t["contentUrl"]); s != "" {
			return s
		}
		return ldString(t["url"])
	}
	return ""
}

func ldNumber(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case string:
		f, _ := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(t), ",", ""), 64)
		return f
	}
	return 0
}

func ldPrice(v any) (float64, string) {
	switch t := v.(type) {
	case []any:
		for _, x := range t {
			if p, c := ldPrice(x); p > 0 {
				return p, c
			}
		}
	case map[string]any:
		cur := ldString(t["priceCurrency"])
		if p := ldNumber(t["price"]); p > 0 {
			return p, cur
		}
		if p := ldNumber(t["lowPrice"]); p > 0 {
			return p, cur
		}
		if p, c := ldPrice(t["priceSpecification"]); p > 0 {
			if c == "" {
				c = cur
			}
			return p, c
		}
	}
	return 0, ""
}

func dimsFromLD(ld map[string]any) (w, d, h float64, ok bool) {
	if ld == nil {
		return
	}
	w, ok1 := ParseLength(ldString(ld["width"]))
	d, ok2 := ParseLength(ldString(ld["depth"]))
	h, ok3 := ParseLength(ldString(ld["height"]))
	return w, d, h, ok1 && ok2 && ok3 && w > 0 && d > 0 && h > 0
}

// --- the Measurements panel (ikea.com embeds it in the page's SSR data) ---

type panelData struct {
	measures []Measure
	packages []Package
}

// parsePanels reads every Measurements panel on the page: besides the product's own, a page can
// carry another product's (a combination suggested next to it).
func parsePanels(page []byte) []panelData {
	key := []byte(`"measurementsProps":`)
	var out []panelData
	for off := 0; ; {
		i := bytes.Index(page[off:], key)
		if i < 0 {
			return out
		}
		off += i + len(key)
		if p, ok := parsePanelAt(page[off:]); ok {
			out = append(out, p)
		}
	}
}

// choosePanel picks the product's own panel: the one whose size agrees with the structured
// data or with the size in the name, else the last one.
func choosePanel(ps []panelData, ld map[string]any, p Product) (panelData, bool) {
	if len(ps) == 0 {
		return panelData{}, false
	}
	if len(ps) == 1 {
		return ps[0], true
	}
	near := func(a, b float64) bool { return math.Abs(a-b) <= 2 }
	if w, d, h, ok := dimsFromLD(ld); ok {
		for _, c := range ps {
			if cw, cd, ch, ok := dimsFromMeasures(c.measures, p.Type); ok && near(cw, w) && near(cd, d) && near(ch, h) {
				return c, true
			}
		}
	}
	if w, _, _, ok := dimsFromName(p.Name, p.Type); ok {
		for _, c := range ps {
			if cw, _, _, ok := dimsFromMeasures(c.measures, p.Type); ok && near(cw, w) {
				return c, true
			}
		}
	}
	return ps[len(ps)-1], true
}

func parsePanelAt(rest []byte) (panelData, bool) {
	var raw struct {
		Measurements []struct {
			Name    string `json:"name"`
			Measure string `json:"measure"`
		} `json:"measurements"`
		Packaging struct {
			ContentProps struct {
				Packages []struct {
					Quantity struct {
						Value any `json:"value"`
					} `json:"quantity"`
					MeasurementGroups []struct {
						Measurements []struct {
							Type  string  `json:"type"`
							Text  string  `json:"text"`
							Value float64 `json:"value"`
						} `json:"measurements"`
					} `json:"measurementGroups"`
				} `json:"packages"`
			} `json:"contentProps"`
		} `json:"packaging"`
	}
	if err := json.NewDecoder(bytes.NewReader(rest)).Decode(&raw); err != nil {
		return panelData{}, false
	}
	var out panelData
	for _, m := range raw.Measurements {
		out.measures = append(out.measures, measure(m.Name, html.UnescapeString(m.Measure)))
	}
	for _, pk := range raw.Packaging.ContentProps.Packages {
		n := int(ldNumber(pk.Quantity.Value))
		if n < 1 {
			n = 1
		}
		for _, g := range pk.MeasurementGroups {
			var b Package
			b.N = n
			for _, m := range g.Measurements {
				switch m.Type {
				case "width":
					b.W, _ = ParseLength(m.Text)
				case "height":
					b.H, _ = ParseLength(m.Text)
				case "length":
					b.L, _ = ParseLength(m.Text)
				case "diameter":
					dia, _ := ParseLength(m.Text)
					if b.W == 0 {
						b.W = dia
					}
					if b.H == 0 {
						b.H = dia
					}
				case "weight":
					b.Weight, _ = parseMass(m.Text)
				}
			}
			if b.W > 0 || b.H > 0 || b.L > 0 {
				out.packages = append(out.packages, b)
			}
		}
	}
	return out, len(out.measures) > 0
}

var cnKeys = map[string]string{
	"宽度": "width", "深度": "depth", "高度": "height", "长度": "length", "直径": "diameter",
	"座宽": "seatWidth", "座深": "seatDepth", "座高": "seatHeight", "座椅宽度": "seatWidth", "座椅深度": "seatDepth", "座椅高度": "seatHeight",
	"扶手高度": "armrestHeight", "床头板高度": "headboardHeight", "床尾板高度": "footboardHeight",
	"床垫长度": "mattressLength", "床垫宽度": "mattressWidth", "家具下方高度": "heightUnderFurniture",
	"每块搁板最大可承重": "maxLoadShelf", "最大承重": "maxLoad", "抽屉深度（内部）": "drawerDepthInside",
	"桌面高度": "tableTopHeight", "储物容量": "storageCapacity", "拉出式抽屉": "drawerPullOut", "灯罩直径": "shadeDiameter", "底座直径": "baseDiameter",
}

// measureKey gives a page's measurement name a stable English key: "Seat height" → seatHeight.
func measureKey(name string) string {
	if k, ok := cnKeys[name]; ok {
		return k
	}
	var words []string
	for _, w := range strings.FieldsFunc(strings.ToLower(name), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		words = append(words, w)
	}
	if len(words) == 0 {
		return name
	}
	for i := 1; i < len(words); i++ {
		r := []rune(words[i])
		r[0] = unicode.ToUpper(r[0])
		words[i] = string(r)
	}
	return strings.Join(words, "")
}

func measure(name, text string) Measure {
	m := Measure{Key: measureKey(name), Name: name, Text: text}
	if cm, ok := ParseLength(text); ok {
		m.CM = cm
	} else if kg, ok := parseMass(text); ok {
		m.KG = kg
	}
	return m
}

// dimsFromMeasures takes width, depth and height from the panel. Beds and tables often give a
// length instead of a depth; round things give a diameter.
func dimsFromMeasures(ms []Measure, typ string) (w, d, h float64, ok bool) {
	get := func(keys ...string) float64 {
		for _, k := range keys {
			for _, m := range ms {
				if m.Key == k && m.CM > 0 {
					return m.CM
				}
			}
		}
		return 0
	}
	w, d, h = get("width"), get("depth"), get("height", "headboardHeight", "heightOfHeadboard", "maxHeight")
	if l := get("length"); d == 0 && l > 0 {
		switch {
		case typ == "bed":
			d = l
		case w == 0:
			w = l
		default:
			w, d = max(w, l), min(w, l)
		}
	}
	if dia := get("diameter", "shadeDiameter", "baseDiameter"); dia > 0 {
		if w == 0 {
			w = dia
		}
		if d == 0 {
			d = dia
		}
	}
	return w, d, h, w > 0 && d > 0 && h > 0
}

// dimsFromName reads W×D×H from the name. A two-part size is W×D for things one looks down on
// (desks, tables, beds, rugs) and W×H for the rest; the missing one is a typical value.
func dimsFromName(s, typ string) (w, d, h float64, ok bool) {
	v, err := ParseSize(s)
	if err != nil {
		return
	}
	if len(v) == 3 {
		return v[0], v[1], v[2], true
	}
	if len(v) != 2 {
		return
	}
	switch typ {
	case "desk", "table", "bed", "rug":
		return v[0], v[1], typicalHeight[typ], true
	}
	return v[0], typicalDepth(typ), v[1], true
}

var typicalHeight = map[string]float64{"desk": 75, "table": 74, "bed": 40, "rug": 1}

func typicalDepth(typ string) float64 {
	switch typ {
	case "drawers", "tvbench":
		return 48
	case "wardrobe":
		return 58
	case "bookcase", "shelf":
		return 28
	case "sofa", "armchair":
		return 90
	}
	return 50
}

// --- ikea.cn (Nuxt pages without JSON-LD) ---

type cnData struct {
	name     string
	price    float64
	measures []Measure
	packages []Package
}

var (
	cnH1Re    = regexp.MustCompile(`(?s)<h1[^>]*class="name"[^>]*>(.*?)</h1>`)
	cnDesRe   = regexp.MustCompile(`(?s)<p[^>]*class="des[ "][^>]*>(.*?)</p>`)
	cnPriceRe = regexp.MustCompile(`i-price__integer">([\d,]+)</span>(?:<span class="i-price__decimal"><span class="i-price__separator">.</span>(\d+))?`)
	cnItemRe  = regexp.MustCompile(`(?s)<div class="section__detail-item"[^>]*><div[^>]*>([^<]*)</div><div[^>]*>([^<]*)</div></div>`)
	tagRe     = regexp.MustCompile(`<[^>]*>`)
)

func text(s string) string {
	return strings.TrimSpace(html.UnescapeString(tagRe.ReplaceAllString(s, "")))
}

func parseCN(page []byte) cnData {
	var c cnData
	h1 := cnH1Re.FindSubmatch(page)
	if h1 == nil {
		return c
	}
	c.name = text(string(h1[1]))
	if des := cnDesRe.FindSubmatch(page); des != nil {
		c.name += " " + text(string(des[1]))
	}
	if m := cnPriceRe.FindSubmatch(page); m != nil {
		s := strings.ReplaceAll(string(m[1]), ",", "")
		if len(m[2]) > 0 {
			s += "." + string(m[2])
		}
		c.price, _ = strconv.ParseFloat(s, 64)
	}
	// 商品尺寸 lists the product; 包装信息 follows with one group per box, each opening with 包装数量.
	sizeAt := bytes.Index(page, []byte(">商品尺寸<"))
	packAt := bytes.Index(page, []byte(">包装信息<"))
	if sizeAt < 0 {
		return c
	}
	var box *Package
	for _, loc := range cnItemRe.FindAllSubmatchIndex(page, -1) {
		if loc[0] < sizeAt {
			continue
		}
		k, v := text(string(page[loc[2]:loc[3]])), text(string(page[loc[4]:loc[5]]))
		if packAt < 0 || loc[0] < packAt {
			c.measures = append(c.measures, measure(k, v))
			continue
		}
		switch k {
		case "包装数量":
			n, _ := strconv.Atoi(v)
			if n < 1 {
				n = 1
			}
			c.packages = append(c.packages, Package{N: n})
			box = &c.packages[len(c.packages)-1]
		case "宽度", "高度", "长度", "直径":
			if box == nil {
				c.packages = append(c.packages, Package{N: 1})
				box = &c.packages[len(c.packages)-1]
			}
			cm, _ := ParseLength(v)
			switch k {
			case "宽度":
				box.W = cm
			case "高度":
				box.H = cm
			case "长度":
				box.L = cm
			default:
				box.W, box.H = cm, cm
			}
		case "重量", "净重":
			if box != nil && (k == "重量" || box.Weight == 0) {
				box.Weight, _ = parseMass(v)
			}
		}
	}
	return c
}

// --- plain HTML ---

var (
	metaRe  = regexp.MustCompile(`(?is)<meta\s[^>]*>`)
	attrRe  = regexp.MustCompile(`(?is)([a-z:-]+)\s*=\s*"([^"]*)"`)
	titleRe = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
)

// metaTags maps og:* (and description) meta tags to their content, either attribute order.
func metaTags(page []byte) map[string]string {
	out := map[string]string{}
	for _, tag := range metaRe.FindAll(page, -1) {
		var key, content string
		for _, a := range attrRe.FindAllSubmatch(tag, -1) {
			switch strings.ToLower(string(a[1])) {
			case "property", "name":
				key = strings.ToLower(string(a[2]))
			case "content":
				content = html.UnescapeString(string(a[2]))
			}
		}
		if key != "" && content != "" {
			if _, seen := out[key]; !seen {
				out[key] = content
			}
		}
	}
	return out
}

func titleTag(page []byte) string {
	if m := titleRe.FindSubmatch(page); m != nil {
		return text(string(m[1]))
	}
	return ""
}

var articleRe = regexp.MustCompile(`[sS]?(\d{8})/?(?:[?#].*)?$`)

// articleFromURL turns ".../billy-bookcase-white-s39440593/" into 394.405.93.
func articleFromURL(u string) string {
	m := articleRe.FindStringSubmatch(u)
	if m == nil {
		return ""
	}
	d := m[1]
	return fmt.Sprintf("%s.%s.%s", d[:3], d[3:6], d[6:])
}

// family is the series name: the leading all-capitals word (BILLY, POÄNG).
func family(name string) string {
	f := strings.Fields(name)
	if len(f) == 0 {
		return ""
	}
	w := strings.TrimRight(f[0], ",")
	letters := 0
	for _, r := range w {
		if unicode.IsLower(r) {
			return ""
		}
		if unicode.IsLetter(r) {
			letters++
		}
	}
	if letters < 2 {
		return ""
	}
	return w
}

// typePart is the name without its colour and size: "KIVIK Sofa - Tibbleby beige/gray" → "KIVIK Sofa".
func typePart(name string) string {
	if i := strings.Index(name, " - "); i >= 0 {
		return name[:i]
	}
	if i := strings.Index(name, ", "); i >= 0 { // "BILLY, Bookcase, white, 80x28x202 cm"
		parts := strings.Split(name, ", ")
		if len(parts) > 1 {
			return parts[0] + " " + parts[1]
		}
		return name[:i]
	}
	return name
}

// colorPart is the colour words of the name: "MALM Bed frame - dark-brown veneer Queen" →
// "dark-brown veneer Queen"; "书架，白色，40x28x106 厘米" → "白色".
func colorPart(name string) string {
	if i := strings.Index(name, " - "); i >= 0 {
		s := name[i+3:]
		if loc := size3Re.FindStringIndex(normSize(s)); loc != nil {
			s = normSize(s)[:loc[0]]
		}
		return strings.TrimSpace(s)
	}
	parts := strings.FieldsFunc(name, func(r rune) bool { return r == '，' || r == ',' })
	if len(parts) >= 3 {
		return strings.TrimSpace(parts[len(parts)-2])
	}
	return ""
}

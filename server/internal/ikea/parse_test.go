package ikea

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	f, err := os.Open("testdata/" + name + ".html.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func near(a, b float64) bool { return math.Abs(a-b) <= 0.6 }

func TestParseFixtures(t *testing.T) {
	cases := []struct {
		file, url   string
		typ, family string
		w, d, h     float64
		from        string
		price       float64
		currency    string
		article     string
		color       string
		packages    int
		measureKey  string
		measureCM   float64
	}{
		{"billy", "https://www.ikea.com/us/en/p/billy-bookcase-white-20522046/", "bookcase", "BILLY", 80.0, 27.9, 201.9, "measurements", 79, "USD", "205.220.46", "#f2f1ec", 1, "", 0},
		{"kivik", "https://www.ikea.com/us/en/p/kivik-sofa-tibbleby-beige-gray-s39440593/", "sofa", "KIVIK", 228.0, 94.9, 82.9, "measurements", 799, "USD", "394.405.93", "#cdbfa8", 3, "seatHeight", 45.1},
		{"malm", "https://www.ikea.com/us/en/p/malm-bed-frame-dark-brown-veneer-s09574367/", "bed", "MALM", 168.0, 211.1, 100.0, "measurements", 249, "USD", "095.743.67", "#5a3b28", 3, "mattressWidth", 152.1},
		{"micke", "https://www.ikea.com/us/en/p/micke-desk-white-80213074/", "desk", "MICKE", 105.1, 49.8, 74.9, "measurements", 109.99, "USD", "802.130.74", "#f2f1ec", 1, "", 0},
		{"micke-2panels", "https://www.ikea.com/us/en/p/micke-desk-white-80213074/", "desk", "MICKE", 105.1, 49.8, 74.9, "measurements", 109.99, "USD", "802.130.74", "#f2f1ec", 1, "", 0},
		{"poang", "https://www.ikea.com/us/en/p/poaeng-armchair-birch-veneer-knisa-light-beige-s59305928/", "armchair", "POÄNG", 67.9, 81.9, 100.0, "measurements", 149, "USD", "593.059.28", "#cdbfa8", 2, "seatHeight", 41.9},
		{"gb-brimnes", "https://www.ikea.com/gb/en/p/brimnes-wardrobe-with-3-doors-white-40407922/", "wardrobe", "BRIMNES", 117, 50, 190, "measurements", 179, "GBP", "404.079.22", "#f2f1ec", 0, "", 0},
		{"gb-hemnes", "https://www.ikea.com/gb/en/p/hemnes-tv-bench-white-stain-light-brown-50413526/", "tvbench", "HEMNES", 148, 47, 57, "measurements", 179, "GBP", "504.135.26", "#e8e2d6", 2, "", 0},
		{"cn-billy", "https://www.ikea.cn/cn/zh/p/billy-bi-li-shu-jia-bai-se-90522038/", "bookcase", "BILLY", 40, 28, 106, "measurements", 199, "CNY", "905.220.38", "#f2f1ec", 1, "", 0},
		{"cn-malm", "https://www.ikea.cn/cn/zh/p/malm-ma-er-mu-liu-dou-chou-ti-gui-bai-se-40354645/", "drawers", "MALM", 80, 48, 123, "measurements", 999, "CNY", "403.546.45", "#f2f1ec", 3, "", 0},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			p, err := Parse(fixture(t, c.file), c.url)
			if err != nil {
				t.Fatalf("Parse: %v (%+v)", err, p)
			}
			if p.Type != c.typ || p.TypeLabel != TypeLabel(c.typ) {
				t.Errorf("type = %s/%s, want %s", p.Type, p.TypeLabel, c.typ)
			}
			if p.Family != c.family {
				t.Errorf("family = %q, want %q", p.Family, c.family)
			}
			if !near(p.W, c.w) || !near(p.D, c.d) || !near(p.H, c.h) {
				t.Errorf("size = %v×%v×%v, want %v×%v×%v", p.W, p.D, p.H, c.w, c.d, c.h)
			}
			if p.DimsFrom != c.from {
				t.Errorf("dimsFrom = %s, want %s", p.DimsFrom, c.from)
			}
			if math.Abs(p.Price-c.price) > 0.001 || p.Currency != c.currency {
				t.Errorf("price = %v %s, want %v %s", p.Price, p.Currency, c.price, c.currency)
			}
			if p.Article != c.article {
				t.Errorf("article = %q, want %q", p.Article, c.article)
			}
			if p.ColorHex != c.color {
				t.Errorf("colorHex = %s (color %q), want %s", p.ColorHex, p.Color, c.color)
			}
			if !strings.HasPrefix(p.Image, "https://") {
				t.Errorf("image = %q", p.Image)
			}
			if c.packages > 0 && len(p.Packages) < c.packages {
				t.Errorf("packages = %d, want at least %d: %+v", len(p.Packages), c.packages, p.Packages)
			}
			for _, b := range p.Packages {
				if b.N < 1 || (b.W <= 0 && b.L <= 0) {
					t.Errorf("odd package %+v", b)
				}
			}
			if c.measureKey != "" {
				found := false
				for _, m := range p.Measures {
					if m.Key == c.measureKey {
						found = true
						if !near(m.CM, c.measureCM) {
							t.Errorf("%s = %v cm, want %v", m.Key, m.CM, c.measureCM)
						}
					}
				}
				if !found {
					t.Errorf("no measure %s in %+v", c.measureKey, p.Measures)
				}
			}
		})
	}
}

func TestParseLoadsAndPackages(t *testing.T) {
	p, err := Parse(fixture(t, "billy"), "https://www.ikea.com/us/en/p/billy-bookcase-white-20522046/")
	if err != nil {
		t.Fatal(err)
	}
	var load *Measure
	for i := range p.Measures {
		if p.Measures[i].Key == "maxLoadShelf" {
			load = &p.Measures[i]
		}
	}
	if load == nil || !near(load.KG, 29.9) || load.CM != 0 {
		t.Errorf("max load/shelf = %+v, want about 29.9 kg", load)
	}
	// 11 ¾ × 5 × 81 ¼ in, 88 lb 1 oz
	b := p.Packages[0]
	if !near(b.W, 29.8) || !near(b.H, 12.7) || !near(b.L, 206.4) || !near(b.Weight, 39.9) {
		t.Errorf("package = %+v", b)
	}
}

func TestParseNameOnly(t *testing.T) {
	page := `<html><head><meta property="og:title" content="BILLY, Bookcase, white, 31 1/2x11x79 1/2 &quot;">
<meta content="https://example.com/b.jpg" property="og:image"></head></html>`
	p, err := Parse([]byte(page), "https://www.ikea.com/us/en/p/billy-bookcase-white-00263850/")
	if err != nil {
		t.Fatal(err)
	}
	if p.Type != "bookcase" || p.DimsFrom != "name" || !near(p.W, 80) || !near(p.D, 27.9) || !near(p.H, 201.9) {
		t.Errorf("%+v", p)
	}
	if p.Image != "https://example.com/b.jpg" || p.Article != "002.638.50" || p.ColorHex != "#f2f1ec" {
		t.Errorf("image/article/color: %q %q %q", p.Image, p.Article, p.ColorHex)
	}
}

func TestParseCategoryListing(t *testing.T) {
	// A category page only has Products nested in an ItemList; the first one is taken.
	page := `<script type="application/ld+json">{"@type":"ItemList","itemListElement":[{"@type":"ListItem","item":{"@type":"Product","name":"BILLY, Bookcase, white, 31 1/2x11x79 1/2 \"","offers":{"@type":"Offer","priceSpecification":[{"@type":"UnitPriceSpecification","price":79,"priceCurrency":"USD"}]}}}]}</script>`
	p, err := Parse([]byte(page), "https://www.ikea.com/us/en/cat/billy-bookcases-58288/")
	if err != nil {
		t.Fatal(err)
	}
	if p.Price != 79 || p.Currency != "USD" || !near(p.H, 201.9) {
		t.Errorf("%+v", p)
	}
}

func TestParseNoSize(t *testing.T) {
	if _, err := Parse([]byte(`<html><title>Something</title></html>`), "https://example.com/x"); err == nil {
		t.Error("want an error for a page without a size")
	}
}

func TestParseSize(t *testing.T) {
	cases := []struct {
		in   string
		want []float64
	}{
		{`31 1/2x11x79 1/2 "`, []float64{80, 27.9, 201.9}},
		{`80x28x202 cm`, []float64{80, 28, 202}},
		{`80×28×202 厘米`, []float64{80, 28, 202}},
		{`41 3/8x19 5/8 "`, []float64{105.1, 49.8}},
		{`80x100 cm`, []float64{80, 100}},
		{`BRIMNES Wardrobe with 3 doors - white 117x50x190 cm`, []float64{117, 50, 190}},
		{`書架 1200x300x1800 mm`, []float64{120, 30, 180}},
		{`37 ½x11 ¾x5 "`, []float64{95.3, 29.8, 12.7}},
		{`80 x 28 x 202 cm`, []float64{80, 28, 202}},
		{`41.5x20 in`, []float64{105.4, 50.8}},
	}
	for _, c := range cases {
		got, err := ParseSize(c.in)
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if len(got) != len(c.want) {
			t.Errorf("%q = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if !near(got[i], c.want[i]) {
				t.Errorf("%q = %v, want %v", c.in, got, c.want)
				break
			}
		}
	}
	for _, bad := range []string{"", "white", "80 cm", "axbxc cm", "KIVIK Sofa - Tibbleby beige/gray"} {
		if v, err := ParseSize(bad); err == nil {
			t.Errorf("%q: want an error, got %v", bad, v)
		}
	}
}

func TestParseLength(t *testing.T) {
	cases := map[string]float64{
		`37 ½ "`:   95.3,
		`31 1/2 "`: 80,
		`11 "`:     27.9,
		`117 cm`:   117,
		`40 厘米`:    40,
		`1200 mm`:  120,
		`1/2 "`:    1.3,
		`79 1/2″`:  201.9,
	}
	for in, want := range cases {
		got, ok := ParseLength(in)
		if !ok || !near(got, want) {
			t.Errorf("%q = %v %v, want %v", in, got, ok, want)
		}
	}
	for _, bad := range []string{"66 lb", "white", ""} {
		if v, ok := ParseLength(bad); ok {
			t.Errorf("%q: want no length, got %v", bad, v)
		}
	}
	if kg, ok := parseMass("88 lb 1 oz"); !ok || !near(kg, 39.95) {
		t.Errorf("88 lb 1 oz = %v", kg)
	}
	if kg, ok := parseMass("29.50 kg"); !ok || kg != 29.5 {
		t.Errorf("29.50 kg = %v", kg)
	}
}

func TestDetectType(t *testing.T) {
	cases := map[string]string{
		"BILLY Bookcase":                "bookcase",
		"KIVIK Sofa":                    "sofa",
		"KIVIK Sectional, 4-seat":       "sofa",
		"POÄNG Armchair":                "armchair",
		"ADDE Chair":                    "chair",
		"MALM Bed frame":                "bed",
		"MALM Storage bed":              "bed",
		"MICKE Desk":                    "desk",
		"LACK Coffee table":             "table",
		"MELLTORP Dining table":         "table",
		"BRIMNES Wardrobe with 3 doors": "wardrobe",
		"MALM Chest of 6 drawers":       "drawers",
		"HEMNES Nightstand":             "drawers",
		"HEMNES TV bench":               "tvbench",
		"BESTÅ TV unit":                 "tvbench",
		"LACK Wall shelf":               "shelf",
		"KALLAX Shelf unit":             "shelf",
		"HEKTAR Floor lamp":             "lamp",
		"FORSÅ Work lamp":               "lamp",
		"STOCKHOLM Rug, flatwoven":      "rug",
		"BILLY Bookcase with doors":     "bookcase",
		"MARKUS Office chair":           "chair",
		"SKUBB Storage case":            "box",
		"BILLY 毕利 书架，白色":                "bookcase",
		"KIVIK 奇维 三人沙发":                 "sofa",
		"POÄNG 波昂 扶手椅":                  "armchair",
		"MALM 马尔姆 高床架":                  "bed",
		"MALM 马尔姆 床头柜":                  "drawers",
		"MICKE 米克 书桌":                   "desk",
		"LACK 拉克 茶几":                    "table",
		"BRIMNES 百灵 双门衣柜":               "wardrobe",
		"MALM 马尔姆 六斗抽屉柜":                "drawers",
		"HEMNES 汉尼斯 电视柜":                "tvbench",
		"ADDE 阿德 椅子":                    "chair",
		"LACK 拉克 搁架":                    "shelf",
		"HEKTAR 赫克塔 落地灯":                "lamp",
		"STOCKHOLM 斯德哥尔摩 地毯":            "rug",
	}
	for in, want := range cases {
		if got := DetectType(in); got != want {
			t.Errorf("DetectType(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestColorHex(t *testing.T) {
	cases := []struct{ in, typ, want string }{
		{"white", "bookcase", "#f2f1ec"},
		{"dark-brown veneer Queen", "bed", "#5a3b28"},
		{"black-brown", "bookcase", "#3a2e29"},
		{"beige, gray Tibbleby beige/gray", "sofa", "#cdbfa8"},
		{"white stain/light brown", "tvbench", "#e8e2d6"},
		{"白色", "bookcase", "#f2f1ec"},
		{"", "sofa", "#c9c2b6"},
		{"", "box", "#e9e7e1"},
	}
	for _, c := range cases {
		if got := ColorHex(c.in, c.typ); got != c.want {
			t.Errorf("ColorHex(%q) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestFetch(t *testing.T) {
	page := fixture(t, "micke")
	mux := http.NewServeMux()
	mux.HandleFunc("/us/en/p/micke-desk-white-80213074/", func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.UserAgent(), "Chrome") {
			t.Errorf("user agent %q", r.UserAgent())
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(page)
	})
	mux.HandleFunc("/us/en/p/gone-00000000/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/us/en/cat/desks-20649/", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/us/en/cat/desks-20649/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html><title>Desks</title></html>"))
	})
	mux.HandleFunc("/missing/", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	mux.HandleFunc("/plain/", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("<html><p>hello</p></html>")) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	ctx := context.Background()

	p, err := Fetch(ctx, srv.Client(), srv.URL+"/us/en/p/micke-desk-white-80213074/")
	if err != nil {
		t.Fatal(err)
	}
	if p.Type != "desk" || !near(p.W, 105.1) || !strings.HasSuffix(p.URL, "/us/en/p/micke-desk-white-80213074/") {
		t.Errorf("%+v", p)
	}

	for _, c := range []struct{ path, want string }{
		{"/us/en/p/gone-00000000/", "分类页"},
		{"/missing/", "404"},
		{"/plain/", "不是单个商品页"},
	} {
		if _, err := Fetch(ctx, srv.Client(), srv.URL+c.path); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want it to mention %s", c.path, err, c.want)
		}
	}
	for _, bad := range []string{"", "ikea.com/p/x", "ftp://ikea.com/x", "javascript:alert(1)"} {
		if _, err := Fetch(ctx, srv.Client(), bad); err == nil || !strings.Contains(err.Error(), "链接不对") {
			t.Errorf("%q: err = %v", bad, err)
		}
	}
}

func TestFetchTooLarge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(bytes.Repeat([]byte("a"), maxPage+10))
	}))
	defer srv.Close()
	if _, err := Fetch(context.Background(), srv.Client(), srv.URL); err == nil || !strings.Contains(err.Error(), "太大") {
		t.Errorf("err = %v", err)
	}
}

func TestNewClientUsesProxyEnv(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://192.0.2.1:7890")
	t.Setenv("NO_PROXY", "")
	tr := NewClient().Transport.(*http.Transport)
	req, _ := http.NewRequest("GET", "https://www.ikea.com/", nil)
	u, err := tr.Proxy(req)
	if err != nil || u == nil || u.Host != "192.0.2.1:7890" {
		t.Errorf("proxy = %v, %v", u, err)
	}
}

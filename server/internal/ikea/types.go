package ikea

import (
	"regexp"
	"strings"
)

type typeRule struct {
	typ string
	re  *regexp.Regexp
}

// Order matters: a desk lamp is a lamp, an armchair is not a chair, a sofa bed is a sofa, a
// bookcase with drawers is a bookcase, a bed with drawers is a bed, a desk is not a table.
var typeRules = []typeRule{
	{"lamp", regexp.MustCompile(`(?i)\blamps?\b|\blighting\b|落地灯|台灯|吊灯|灯具|灯`)},
	{"rug", regexp.MustCompile(`(?i)\brugs?\b|\bcarpet|地毯|地垫`)},
	{"drawers", regexp.MustCompile(`(?i)\bnightstand|\bbedside table|床头柜`)},
	{"armchair", regexp.MustCompile(`(?i)\barm ?chairs?\b|\brecliner|\bwing chair|扶手椅|躺椅`)},
	{"sofa", regexp.MustCompile(`(?i)\bsofas?\b|\bloveseat|\bsectional|\bcouch|\bsettee|沙发`)},
	{"tvbench", regexp.MustCompile(`(?i)\btv (bench|unit|stand|storage)|\bmedia (console|unit)|电视柜|电视机柜|电视机组合柜`)},
	{"wardrobe", regexp.MustCompile(`(?i)\bwardrobes?\b|\barmoire|\bcloset|衣柜|衣橱`)},
	{"bookcase", regexp.MustCompile(`(?i)\bbook ?cases?\b|\bbook ?shel(f|ves)\b|书柜|书架|书橱`)},
	{"chair", regexp.MustCompile(`(?i)\bchairs?\b|\bstools?\b|\bbench\b|椅|凳`)},
	{"desk", regexp.MustCompile(`(?i)\bdesks?\b|\bworkstation|书桌|写字台|电脑桌|办公桌`)},
	{"bed", regexp.MustCompile(`(?i)\bbed( frame)?s?\b|\bdaybed|\bbunk|床`)},
	{"drawers", regexp.MustCompile(`(?i)\bchest of (\d+ )?drawers\b|\bdrawers?\b|\bdresser|抽屉柜|斗柜`)},
	{"shelf", regexp.MustCompile(`(?i)\bshel(f|ves|ving)\b|\bshelving unit|搁架|置物架|层架|壁架|搁板`)},
	{"table", regexp.MustCompile(`(?i)\btables?\b|桌|茶几|餐台`)},
}

// DetectType names the model to build for a product name or category.
func DetectType(s string) string {
	for _, r := range typeRules {
		if r.re.MatchString(s) {
			return r.typ
		}
	}
	return "box"
}

var typeLabels = map[string]string{
	"bookcase": "书柜", "sofa": "沙发", "armchair": "扶手椅", "bed": "床", "desk": "书桌", "table": "桌子",
	"chair": "椅子", "wardrobe": "衣柜", "drawers": "抽屉柜", "tvbench": "电视柜", "shelf": "搁架",
	"lamp": "灯", "rug": "地毯", "box": "家具",
}

// Types are the kinds the twin builds models for, box being the fallback.
var Types = []string{"bookcase", "sofa", "armchair", "bed", "desk", "table", "chair", "wardrobe", "drawers", "tvbench", "shelf", "lamp", "rug", "box"}

// TypeLabel is a kind's Chinese name.
func TypeLabel(t string) string {
	if l, ok := typeLabels[t]; ok {
		return l
	}
	return "家具"
}

// colours by name, longest names first where one contains another
var colors = []struct{ name, hex string }{
	{"white stained oak", "#e3d3b8"}, {"white stain", "#e8e2d6"}, {"black-brown", "#3a2e29"}, {"black brown", "#3a2e29"},
	{"dark-brown", "#5a3b28"}, {"dark brown", "#5a3b28"}, {"light brown", "#b08a62"}, {"light-brown", "#b08a62"},
	{"light gray", "#c9c9c4"}, {"light grey", "#c9c9c4"}, {"dark gray", "#5f6062"}, {"dark grey", "#5f6062"},
	{"light beige", "#d9cdb8"}, {"dark blue", "#2f4a6b"}, {"light blue", "#a9c0d6"}, {"dark green", "#3f5a43"},
	{"anthracite", "#45474a"}, {"walnut", "#6b4a33"}, {"birch", "#d9bd8f"}, {"oak", "#cfae80"}, {"pine", "#dcc08f"},
	{"bamboo", "#d6b77f"}, {"acacia", "#a8774a"}, {"beech", "#d8b48a"}, {"teak", "#9a6a3e"}, {"rattan", "#c9a46e"},
	{"white", "#f2f1ec"}, {"black", "#2b2b2b"}, {"brown", "#5a3b28"}, {"beige", "#cdbfa8"}, {"gray", "#9c9c98"},
	{"grey", "#9c9c98"}, {"blue", "#4a6a8f"}, {"green", "#6f8f6a"}, {"pink", "#d8a7a7"}, {"yellow", "#e2c25b"},
	{"red", "#b5483f"}, {"orange", "#d9824a"}, {"turquoise", "#4f9a9a"}, {"glass", "#cfdde3"}, {"natural", "#d8c3a0"},
	{"silver", "#b9bcc0"}, {"gold", "#c9a85a"},
	{"白色橡木", "#e3d3b8"}, {"黑褐色", "#3a2e29"}, {"深褐色", "#5a3b28"}, {"浅褐色", "#b08a62"}, {"浅灰色", "#c9c9c4"},
	{"深灰色", "#5f6062"}, {"浅米色", "#d9cdb8"}, {"深蓝色", "#2f4a6b"}, {"胡桃木", "#6b4a33"}, {"橡木", "#cfae80"},
	{"桦木", "#d9bd8f"}, {"松木", "#dcc08f"}, {"竹", "#d6b77f"}, {"白色", "#f2f1ec"}, {"黑色", "#2b2b2b"},
	{"褐色", "#5a3b28"}, {"棕色", "#5a3b28"}, {"米色", "#cdbfa8"}, {"灰色", "#9c9c98"}, {"蓝色", "#4a6a8f"},
	{"绿色", "#6f8f6a"}, {"粉色", "#d8a7a7"}, {"粉红色", "#d8a7a7"}, {"黄色", "#e2c25b"}, {"红色", "#b5483f"},
	{"橙色", "#d9824a"}, {"玻璃", "#cfdde3"}, {"白", "#f2f1ec"}, {"黑", "#2b2b2b"}, {"灰", "#9c9c98"},
}

// ColorHex is the paint for the model: the colour named first in s, else a default for the kind.
func ColorHex(s, typ string) string {
	low := strings.ToLower(s)
	best, at, length := "", -1, 0
	for _, c := range colors {
		i := strings.Index(low, c.name)
		if i < 0 {
			continue
		}
		if at < 0 || i < at || (i == at && len(c.name) > length) {
			best, at, length = c.hex, i, len(c.name)
		}
	}
	if best != "" {
		return best
	}
	switch typ {
	case "sofa", "armchair", "bed", "rug":
		return "#c9c2b6"
	case "bookcase", "desk", "table", "chair", "wardrobe", "drawers", "tvbench", "shelf":
		return "#d2b48c"
	}
	return "#e9e7e1"
}

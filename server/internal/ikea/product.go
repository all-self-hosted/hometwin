// Package ikea reads a furniture product page (IKEA's, or any page with the same JSON-LD) into
// what the twin needs: name, kind, colour, photo, price and the real size in centimetres.
package ikea

// Product is one piece of furniture as the twin knows it. Sizes are centimetres.
type Product struct {
	// Source is "ikea" for an imported page, "manual" for one typed in, "polyfork" for a
	// model picked from Polyfork's catalogue.
	Source string `json:"source"`
	URL    string `json:"url,omitempty"`
	// Name is the full product name as the shop writes it ("BILLY Bookcase - white 80x28x202 cm").
	Name string `json:"name"`
	// Family is the series name in capitals (BILLY, KIVIK).
	Family string `json:"family,omitempty"`
	// Type is the model the twin builds: bookcase, sofa, armchair, bed, desk, table, chair,
	// wardrobe, drawers, tvbench, shelf, lamp, rug, or box for anything else.
	Type string `json:"type"`
	// TypeLabel is the kind in Chinese (书柜).
	TypeLabel string `json:"typeLabel"`
	Color     string `json:"color,omitempty"`
	// ColorHex is the colour the model is painted with.
	ColorHex string  `json:"colorHex"`
	Image    string  `json:"image,omitempty"`
	Article  string  `json:"article,omitempty"`
	Price    float64 `json:"price,omitempty"`
	Currency string  `json:"currency,omitempty"`
	W        float64 `json:"w"`
	D        float64 `json:"d"`
	H        float64 `json:"h"`
	// DimsFrom says where the size came from: measurements (the page's Measurements panel),
	// jsonld (width/depth/height of the structured data), name (W×D×H in the name), manual.
	DimsFrom string `json:"dimsFrom"`
	// Measures are the Measurements panel's lines (Seat height 45 cm, …), converted to cm.
	Measures []Measure `json:"measures,omitempty"`
	// Packages are the flat-pack boxes, to check against the entrance door.
	Packages []Package `json:"packages,omitempty"`
	// Model is a ready 3D model (GLB) to show instead of a built one; Credit is its licence line.
	Model  string `json:"model,omitempty"`
	Credit string `json:"credit,omitempty"`
}

// Measure is one line of the Measurements panel.
type Measure struct {
	Key  string  `json:"key"`  // width, depth, height, seatHeight, … (stable, English)
	Name string  `json:"name"` // as written on the page
	Text string  `json:"text"` // as written on the page, units included
	CM   float64 `json:"cm"`
	// KG is set instead of CM for loads and weights.
	KG float64 `json:"kg,omitempty"`
}

// Package is one box the product ships in; sizes in cm, weight in kg.
type Package struct {
	W      float64 `json:"w"`
	H      float64 `json:"h"`
	L      float64 `json:"l"`
	Weight float64 `json:"kg,omitempty"`
	N      int     `json:"n"`
}

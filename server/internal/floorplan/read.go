package floorplan

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"regexp"
	"strings"

	"github.com/liliang-cn/agent-go/v3/pkg/domain"
)

// Reading is what the model reads off a plan. Numbers and names are trusted; pixel positions
// only roughly, and only after they are mapped onto the dimension chains found in the image.
type Reading struct {
	Chains  map[string]Chain `json:"chains"` // top, bottom, left, right
	Rooms   []RoomR          `json:"rooms"`
	Walls   []WallR          `json:"walls"`
	Doors   []DoorR          `json:"doors"`
	Windows []WindowR        `json:"windows"`
}

// Chain is one dimension chain: the line's position across it and its segments in order
// (top/bottom left to right, left/right top to bottom).
type Chain struct {
	Line     float64 `json:"line_px"`
	Segments []Seg   `json:"segments"`
}

type Seg struct {
	MM   float64 `json:"mm"`
	From float64 `json:"from_px"`
	To   float64 `json:"to_px"`
}

type RoomR struct {
	Name string       `json:"name"`
	Kind string       `json:"kind"`
	Area float64      `json:"area_m2"`
	Poly [][2]float64 `json:"poly_px"`
}

type WallR struct {
	A        [2]float64 `json:"a_px"`
	B        [2]float64 `json:"b_px"`
	Exterior bool       `json:"exterior"`
}

type DoorR struct {
	Between   []string   `json:"between"`
	Center    [2]float64 `json:"center_px"`
	Width     float64    `json:"width_px"`
	Kind      string     `json:"kind"`
	OpensInto string     `json:"opens_into"`
}

type WindowR struct {
	Room string     `json:"room"`
	A    [2]float64 `json:"a_px"`
	B    [2]float64 `json:"b_px"`
}

const readPrompt = `This is an apartment floor plan image (%d x %d pixels, origin at the top left). North is up. Dimension chains in millimetres run along the sides.
Return JSON only, with these keys:

"chains": {"top": …, "bottom": …, "left": …, "right": …} — each {"line_px": the pixel y (top/bottom) or x (left/right) of that dimension line, "segments": [{"mm": number, "from_px": number, "to_px": number}, …]}: the segments of the chain in order (top/bottom left to right, left/right top to bottom), with the pixel x (top/bottom) or pixel y (left/right) of the segment's two tick marks. Leave out a side that has no chain. When a side has two rows of chains, use the one nearest the plan.

"rooms": list of {"name": label as printed (e.g. 卧室A), "kind": one of living|bedroom|kitchen|bath|balcony|entry, "area_m2": printed area or 0, "poly_px": [[x,y],...] the room's outline along its walls' centre lines, corners in order, rectilinear}. Every bit of floor belongs to exactly one room; an unlabelled corridor or hall that is not part of a labelled room is a room named 过道 (kind entry).

"walls": list of {"a_px": [x,y], "b_px": [x,y], "exterior": bool} — centre lines of every wall, straight segments, horizontal or vertical, including short wall stubs.

"doors": list of {"between": [room name, room name or "outside"], "center_px": [x,y], "width_px": number, "kind": "swing"|"sliding"|"opening", "opens_into": room name} — every door, including the entrance door (between a room and "outside").

"windows": list of {"room": room name, "a_px": [x,y], "b_px": [x,y]} — window openings drawn in exterior walls (thin parallel lines), including balcony glazing.

Use pixel coordinates of this image. Read every printed number exactly.`

// fence matches the ``` wrapper some models put around JSON despite being asked not to.
var fence = regexp.MustCompile("(?s)^\\s*```[a-zA-Z]*\\s*(.*?)\\s*```\\s*$")

// ParseReading reads the model's answer.
func ParseReading(text string) (Reading, error) {
	text = strings.TrimSpace(text)
	if m := fence.FindStringSubmatch(text); m != nil {
		text = m[1]
	}
	if i, j := strings.Index(text, "{"), strings.LastIndex(text, "}"); i >= 0 && j > i {
		text = text[i : j+1]
	}
	var r Reading
	if err := json.Unmarshal([]byte(text), &r); err != nil {
		return r, fmt.Errorf("模型的回答不是有效的 JSON：%w", err)
	}
	if len(r.Rooms) == 0 {
		return r, errors.New("模型在图上没有认出房间")
	}
	return r, nil
}

// pngPart is an image for the model.
func pngPart(img image.Image) (domain.MessagePart, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return domain.MessagePart{}, err
	}
	return domain.MessagePart{Type: domain.MessagePartTypeImage, Image: &domain.MessageImage{
		Base64: base64.StdEncoding.EncodeToString(buf.Bytes()), MIMEType: "image/png", Detail: "high"}}, nil
}

// ask puts one question with one image to the model and returns its text.
func ask(ctx context.Context, llm domain.Generator, img image.Image, question string, jsonOut bool) (string, error) {
	part, err := pngPart(img)
	if err != nil {
		return "", err
	}
	opts := &domain.GenerationOptions{Temperature: 0, MaxTokens: 16000}
	if jsonOut {
		opts.ResponseFormat = &domain.ResponseFormat{Type: "json_object"}
	}
	msgs := []domain.Message{{Role: "user", Parts: []domain.MessagePart{domain.TextPart(question), part}}}
	res, err := llm.GenerateWithTools(ctx, msgs, nil, opts)
	if err != nil {
		return "", err
	}
	return res.Content, nil
}

// Read asks the model to read a plan (already cropped to the drawing). It returns the raw
// answer too, so it can be kept and replayed.
func Read(ctx context.Context, llm domain.Generator, img image.Image) (Reading, string, error) {
	b := img.Bounds()
	text, err := ask(ctx, llm, img, fmt.Sprintf(readPrompt, b.Dx(), b.Dy()), true)
	if err != nil {
		return Reading{}, "", fmt.Errorf("模型没有读出这张图：%w", err)
	}
	r, err := ParseReading(text)
	return r, text, err
}

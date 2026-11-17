package scan

import (
	"bytes"
	"errors"
	"image/jpeg"
)

// Import turns a downloaded scan into a home and its textured model: textures are the
// chunks' JPEGs, turns says where north is (ChooseTurns can tell from a floor plan), and
// the room names come from the scan's own layout when opt.Hints is empty.
func Import(m Mesh, w Work, layout []LayoutRoom, textures [][]byte, turns int, opt Options) (Result, []byte, error) {
	f, err := Orient(m, turns)
	if err != nil {
		return Result{}, nil, err
	}
	if len(opt.Hints) == 0 {
		opt.Hints = HintsFromScan(w, layout, f)
	}
	if len(opt.Windows) == 0 {
		opt.Windows = WindowsFromScan(w, f)
	}
	if opt.Textures == nil {
		for _, b := range textures {
			im, err := jpeg.Decode(bytes.NewReader(b))
			if err != nil {
				im = nil // the finish is then left to the room kinds
			}
			opt.Textures = append(opt.Textures, im)
		}
	}
	res, err := Plan(m, f, opt)
	if err != nil {
		return Result{}, nil, err
	}
	var glb bytes.Buffer
	// The cutaway view never shows the ceilings.
	if err := WriteGLB(m, f, textures, &glb, GLBOptions{MaxY: f.Ceiling - 0.15}); err != nil {
		return Result{}, nil, err
	}
	return res, glb.Bytes(), nil
}

// ChooseTurns finds where north is by laying a floor plan's rooms (hints in a north-up
// frame from the north-west corner) over the scan turned each of the four ways: the turn
// whose rooms take the most of the plan's names, then whose named rooms are largest.
func ChooseTurns(m Mesh, hints []RoomHint) (int, error) {
	if len(hints) == 0 {
		return 0, errors.New("没有户型图房间，定不了朝向")
	}
	best, bestN, bestA := 0, -1, 0.0
	for t := range 4 {
		f, err := Orient(m, t)
		if err != nil {
			return 0, err
		}
		res, err := Plan(m, f, Options{Hints: hints})
		if err != nil {
			continue
		}
		names := map[string]bool{}
		for _, h := range hints {
			names[h.Name] = true
		}
		n, a := 0, 0.0
		for i, r := range res.Home.Rooms {
			if names[r.Name] {
				n++
				a += res.Rooms[i].Area
			}
		}
		if n > bestN || (n == bestN && a > bestA) {
			best, bestN, bestA = t, n, a
		}
	}
	return best, nil
}

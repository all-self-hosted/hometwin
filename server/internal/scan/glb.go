package scan

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"math"
)

// GLBOptions for WriteGLB.
type GLBOptions struct {
	// MaxY drops the triangles lying wholly above this height (m above the main floor),
	// the ceilings the cutaway view does not want. 0 keeps everything.
	MaxY float64
}

// WriteGLB writes the scan as a binary glTF 2.0 in the home's frame (metres, y up, x east,
// z south), one primitive per chunk with its JPEG texture embedded. The textures carry the
// scan's lighting, so the materials are unlit. textures[i] is chunk i's JPEG; a missing one
// leaves that chunk a plain grey.
func WriteGLB(m Mesh, f Frame, textures [][]byte, w io.Writer, opt GLBOptions) error {
	type acc struct {
		BufferView    int       `json:"bufferView"`
		ComponentType int       `json:"componentType"`
		Count         int       `json:"count"`
		Type          string    `json:"type"`
		Min           []float64 `json:"min,omitempty"`
		Max           []float64 `json:"max,omitempty"`
	}
	type view struct {
		Buffer     int `json:"buffer"`
		ByteOffset int `json:"byteOffset"`
		ByteLength int `json:"byteLength"`
		Target     int `json:"target,omitempty"`
	}
	type texRef struct {
		Index int `json:"index"`
	}
	type pbr struct {
		BaseColorTexture *texRef   `json:"baseColorTexture,omitempty"`
		BaseColorFactor  []float64 `json:"baseColorFactor,omitempty"`
		Metallic         float64   `json:"metallicFactor"`
		Roughness        float64   `json:"roughnessFactor"`
	}
	type material struct {
		Name        string         `json:"name"`
		PBR         pbr            `json:"pbrMetallicRoughness"`
		DoubleSided bool           `json:"doubleSided"`
		Extensions  map[string]any `json:"extensions"`
	}
	type prim struct {
		Attributes map[string]int `json:"attributes"`
		Indices    int            `json:"indices"`
		Material   int            `json:"material"`
	}
	type image struct {
		BufferView int    `json:"bufferView"`
		MimeType   string `json:"mimeType"`
	}
	type texture struct {
		Sampler int `json:"sampler"`
		Source  int `json:"source"`
	}

	var bin bytes.Buffer
	var views []view
	var accs []acc
	addView := func(b []byte, target int) int {
		for bin.Len()%4 != 0 {
			bin.WriteByte(0)
		}
		views = append(views, view{ByteOffset: bin.Len(), ByteLength: len(b), Target: target})
		bin.Write(b)
		return len(views) - 1
	}

	var prims []prim
	var mats []material
	var images []image
	var texs []texture
	for ci, ch := range m.Chunks {
		nv := len(ch.Pos) / 3
		pos := make([][3]float64, nv)
		for i := range nv {
			pos[i] = f.Point(float64(ch.Pos[3*i]), float64(ch.Pos[3*i+1]), float64(ch.Pos[3*i+2]))
		}
		// Keep the triangles under MaxY and the vertices they use.
		remap := make([]int32, nv)
		for i := range remap {
			remap[i] = -1
		}
		var idx []uint32
		var keep []int
		for i := 0; i+2 < len(ch.Index); i += 3 {
			a, b, c := ch.Index[i], ch.Index[i+1], ch.Index[i+2]
			if opt.MaxY > 0 && math.Min(pos[a][1], math.Min(pos[b][1], pos[c][1])) > opt.MaxY {
				continue
			}
			for _, v := range []uint32{a, b, c} {
				if remap[v] < 0 {
					remap[v] = int32(len(keep))
					keep = append(keep, int(v))
				}
				idx = append(idx, uint32(remap[v]))
			}
		}
		if len(idx) == 0 {
			continue
		}
		pb := make([]byte, 0, 12*len(keep))
		mn := []float64{math.Inf(1), math.Inf(1), math.Inf(1)}
		mx := []float64{math.Inf(-1), math.Inf(-1), math.Inf(-1)}
		for _, v := range keep {
			for k := range 3 {
				x := float32(pos[v][k])
				pb = binary.LittleEndian.AppendUint32(pb, math.Float32bits(x))
				mn[k] = math.Min(mn[k], float64(x))
				mx[k] = math.Max(mx[k], float64(x))
			}
		}
		attrs := map[string]int{}
		accs = append(accs, acc{BufferView: addView(pb, 34962), ComponentType: 5126, Count: len(keep), Type: "VEC3", Min: mn, Max: mx})
		attrs["POSITION"] = len(accs) - 1
		if len(ch.UV) == 2*nv {
			ub := make([]byte, 0, 8*len(keep))
			for _, v := range keep {
				ub = binary.LittleEndian.AppendUint32(ub, math.Float32bits(ch.UV[2*v]))
				ub = binary.LittleEndian.AppendUint32(ub, math.Float32bits(1-ch.UV[2*v+1])) // glTF's v runs down
			}
			accs = append(accs, acc{BufferView: addView(ub, 34962), ComponentType: 5126, Count: len(keep), Type: "VEC2"})
			attrs["TEXCOORD_0"] = len(accs) - 1
		}
		var ib []byte
		ct := 5125 // uint32
		if len(keep) < 1<<16 {
			ct = 5123 // uint16
			for _, i := range idx {
				ib = binary.LittleEndian.AppendUint16(ib, uint16(i))
			}
		} else {
			for _, i := range idx {
				ib = binary.LittleEndian.AppendUint32(ib, i)
			}
		}
		accs = append(accs, acc{BufferView: addView(ib, 34963), ComponentType: ct, Count: len(idx), Type: "SCALAR"})

		mat := material{Name: ch.Texture, DoubleSided: true, PBR: pbr{Metallic: 0, Roughness: 1},
			Extensions: map[string]any{"KHR_materials_unlit": map[string]any{}}}
		if ci < len(textures) && len(textures[ci]) > 0 && attrs["TEXCOORD_0"] > 0 {
			images = append(images, image{BufferView: addView(textures[ci], 0), MimeType: "image/jpeg"})
			texs = append(texs, texture{Sampler: 0, Source: len(images) - 1})
			mat.PBR.BaseColorTexture = &texRef{Index: len(texs) - 1}
		} else {
			mat.PBR.BaseColorFactor = []float64{0.8, 0.8, 0.8, 1}
		}
		mats = append(mats, mat)
		prims = append(prims, prim{Attributes: attrs, Indices: len(accs) - 1, Material: len(mats) - 1})
	}
	if len(prims) == 0 {
		return errors.New("glb: nothing left to write")
	}
	for bin.Len()%4 != 0 {
		bin.WriteByte(0)
	}

	doc := map[string]any{
		"asset":          map[string]any{"version": "2.0", "generator": "hometwin scan"},
		"extensionsUsed": []string{"KHR_materials_unlit"},
		"scene":          0,
		"scenes":         []any{map[string]any{"nodes": []int{0}}},
		"nodes":          []any{map[string]any{"name": "scan", "mesh": 0}},
		"meshes":         []any{map[string]any{"name": "scan", "primitives": prims}},
		"materials":      mats,
		"accessors":      accs,
		"bufferViews":    views,
		"buffers":        []any{map[string]any{"byteLength": bin.Len()}},
	}
	if len(images) > 0 {
		doc["images"] = images
		doc["textures"] = texs
		doc["samplers"] = []any{map[string]any{"magFilter": 9729, "minFilter": 9987, "wrapS": 33071, "wrapT": 33071}}
	}
	js, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	for len(js)%4 != 0 {
		js = append(js, ' ')
	}
	total := 12 + 8 + len(js) + 8 + bin.Len()
	var hdr []byte
	hdr = binary.LittleEndian.AppendUint32(hdr, 0x46546C67) // "glTF"
	hdr = binary.LittleEndian.AppendUint32(hdr, 2)
	hdr = binary.LittleEndian.AppendUint32(hdr, uint32(total))
	hdr = binary.LittleEndian.AppendUint32(hdr, uint32(len(js)))
	hdr = binary.LittleEndian.AppendUint32(hdr, 0x4E4F534A) // "JSON"
	hdr = append(hdr, js...)
	hdr = binary.LittleEndian.AppendUint32(hdr, uint32(bin.Len()))
	hdr = binary.LittleEndian.AppendUint32(hdr, 0x004E4942) // "BIN\0"
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	_, err = w.Write(bin.Bytes())
	return err
}

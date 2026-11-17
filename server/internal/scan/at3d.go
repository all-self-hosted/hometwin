// Package scan imports a home from a 贝壳/如视 (Realsee) VR share link: it reads the share
// page, downloads the textured scan model (.at3d), turns it into a glTF for the 3D view and
// works out the walls, doors, windows, rooms and floor levels from the mesh.
//
// The scan's own frame is metres with z up. Frame turns it into HomeTwin's: x east, y up,
// z south, walls along the axes, the origin at the north-west corner.
package scan

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// MaxModel caps the decompressed model; the flats seen so far are 1–2 MB.
const MaxModel = 256 << 20

// Mesh is a decoded scan: one chunk per texture.
type Mesh struct {
	Chunks []Chunk
}

type Chunk struct {
	Name    string
	Texture string    // file name of its texture, e.g. texture_0.jpg
	Pos     []float32 // x, y, z per vertex, metres, z up
	UV      []float32 // u, v per vertex, v up (OBJ style)
	Index   []uint32  // triangles
}

func (m Mesh) Vertices() int {
	n := 0
	for _, c := range m.Chunks {
		n += len(c.Pos) / 3
	}
	return n
}

func (m Mesh) Triangles() int {
	n := 0
	for _, c := range m.Chunks {
		n += len(c.Index) / 3
	}
	return n
}

// Decode reads an .at3d file: uint32 version (1), uint32 length of the rest, then an LZMA
// stream holding a protobuf message. Its field 1 repeats per chunk:
//
//	1 geometry { 1 packed f32 xyz, 2 packed f32 uv }
//	2 faces    { 1 packed varint indices, 2 packed f32 face normals }
//	3 name, 4 texture file name
func Decode(b []byte) (Mesh, error) {
	if len(b) < 8+13 {
		return Mesh{}, errors.New("at3d: file too short")
	}
	if v := binary.LittleEndian.Uint32(b); v != 1 {
		return Mesh{}, fmt.Errorf("at3d: unknown version %d", v)
	}
	if n := binary.LittleEndian.Uint32(b[4:]); int(n) > len(b)-8 {
		return Mesh{}, errors.New("at3d: file truncated")
	}
	raw, err := unLZMA(b[8:], MaxModel)
	if err != nil {
		return Mesh{}, fmt.Errorf("at3d: %w", err)
	}
	var m Mesh
	err = pbFields(raw, func(f int, v []byte) error {
		if f != 1 {
			return nil
		}
		c, err := decodeChunk(v)
		if err != nil {
			return fmt.Errorf("at3d chunk %d: %w", len(m.Chunks), err)
		}
		m.Chunks = append(m.Chunks, c)
		return nil
	})
	if err != nil {
		return Mesh{}, err
	}
	if len(m.Chunks) == 0 {
		return Mesh{}, errors.New("at3d: no geometry")
	}
	return m, nil
}

func decodeChunk(b []byte) (Chunk, error) {
	var c Chunk
	err := pbFields(b, func(f int, v []byte) error {
		switch f {
		case 1:
			return pbFields(v, func(f int, v []byte) error {
				switch f {
				case 1:
					c.Pos = floats(v)
				case 2:
					c.UV = floats(v)
				}
				return nil
			})
		case 2:
			return pbFields(v, func(f int, v []byte) error {
				if f == 1 {
					idx, err := varints(v)
					c.Index = idx
					return err
				}
				return nil
			})
		case 3:
			c.Name = string(v)
		case 4:
			c.Texture = string(v)
		}
		return nil
	})
	if err != nil {
		return c, err
	}
	nv := len(c.Pos) / 3
	switch {
	case len(c.Pos)%3 != 0 || nv == 0:
		return c, errors.New("no vertices")
	case len(c.UV) != 0 && len(c.UV) != 2*nv:
		return c, errors.New("uv count does not match the vertices")
	case len(c.Index)%3 != 0:
		return c, errors.New("index count is not a multiple of 3")
	}
	for _, i := range c.Index {
		if int(i) >= nv {
			return c, errors.New("index out of range")
		}
	}
	return c, nil
}

// pbFields walks a protobuf message and calls fn for each length-delimited field; other
// wire types are skipped.
func pbFields(b []byte, fn func(field int, v []byte) error) error {
	for i := 0; i < len(b); {
		key, n := binary.Uvarint(b[i:])
		if n <= 0 {
			return errors.New("protobuf: bad key")
		}
		i += n
		field, wire := int(key>>3), key&7
		switch wire {
		case 0:
			_, n := binary.Uvarint(b[i:])
			if n <= 0 {
				return errors.New("protobuf: bad varint")
			}
			i += n
		case 1:
			i += 8
		case 5:
			i += 4
		case 2:
			l, n := binary.Uvarint(b[i:])
			if n <= 0 || l > uint64(len(b)-i-n) {
				return errors.New("protobuf: bad length")
			}
			i += n
			if err := fn(field, b[i:i+int(l)]); err != nil {
				return err
			}
			i += int(l)
		default:
			return fmt.Errorf("protobuf: wire type %d", wire)
		}
		if i > len(b) {
			return errors.New("protobuf: truncated")
		}
	}
	return nil
}

func floats(b []byte) []float32 {
	out := make([]float32, len(b)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
	}
	return out
}

func varints(b []byte) ([]uint32, error) {
	out := make([]uint32, 0, len(b))
	for i := 0; i < len(b); {
		v, n := binary.Uvarint(b[i:])
		if n <= 0 || v > math.MaxUint32 {
			return nil, errors.New("protobuf: bad packed varint")
		}
		out = append(out, uint32(v))
		i += n
	}
	return out, nil
}

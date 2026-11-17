package scan

import (
	"encoding/binary"
	"math"
)

// A minimal .at3d writer for the tests: the protobuf the decoder reads, compressed by an
// LZMA encoder that only ever emits literals. That is a valid LZMA-alone stream (any
// decoder takes it), just a poorly compressed one, and it is short enough to keep here.

// encodeAt3d writes m the way Decode reads it.
func encodeAt3d(m Mesh) []byte {
	var msg []byte
	for _, c := range m.Chunks {
		var geo, faces, ch []byte
		geo = pbBytes(geo, 1, f32s(c.Pos))
		geo = pbBytes(geo, 2, f32s(c.UV))
		var idx []byte
		for _, i := range c.Index {
			idx = binary.AppendUvarint(idx, uint64(i))
		}
		faces = pbBytes(faces, 1, idx)
		ch = pbBytes(ch, 1, geo)
		ch = pbBytes(ch, 2, faces)
		ch = pbBytes(ch, 3, []byte(c.Name))
		ch = pbBytes(ch, 4, []byte(c.Texture))
		msg = pbBytes(msg, 1, ch)
	}
	z := lzmaLiterals(msg)
	out := binary.LittleEndian.AppendUint32(nil, 1)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(z)))
	return append(out, z...)
}

// pbBytes appends a length-delimited protobuf field.
func pbBytes(b []byte, field int, v []byte) []byte {
	b = binary.AppendUvarint(b, uint64(field)<<3|2)
	b = binary.AppendUvarint(b, uint64(len(v)))
	return append(b, v...)
}

func f32s(v []float32) []byte {
	out := make([]byte, 0, 4*len(v))
	for _, f := range v {
		out = binary.LittleEndian.AppendUint32(out, math.Float32bits(f))
	}
	return out
}

// rangeEnc is the LZMA range encoder (LzmaEnc.c: RangeEnc_EncodeBit, RangeEnc_ShiftLow).
type rangeEnc struct {
	low       uint64
	rng       uint32
	cache     byte
	cacheSize int
	out       []byte
}

func (e *rangeEnc) shiftLow() {
	if uint32(e.low) < 0xFF000000 || e.low>>32 != 0 {
		carry := byte(e.low >> 32)
		temp := e.cache
		for {
			e.out = append(e.out, temp+carry)
			temp = 0xFF
			if e.cacheSize--; e.cacheSize == 0 {
				break
			}
		}
		e.cache = byte(uint32(e.low) >> 24)
	}
	e.cacheSize++
	e.low = uint64(uint32(e.low) << 8)
}

func (e *rangeEnc) bit(p *uint16, b uint32) {
	bound := (e.rng >> lzmaProbBits) * uint32(*p)
	if b == 0 {
		e.rng = bound
		*p += ((1 << lzmaProbBits) - *p) >> lzmaMoveBits
	} else {
		e.low += uint64(bound)
		e.rng -= bound
		*p -= *p >> lzmaMoveBits
	}
	for e.rng < lzmaTop {
		e.rng <<= 8
		e.shiftLow()
	}
}

// lzmaLiterals compresses data as an LZMA-alone stream (lc=3 lp=0 pb=2, size in the header,
// no end marker) made of literals only: the state machine never leaves state 0.
func lzmaLiterals(data []byte) []byte {
	const lc, pb = 3, 2
	hdr := []byte{byte(lc + pb*45)}
	hdr = binary.LittleEndian.AppendUint32(hdr, 1<<16)
	hdr = binary.LittleEndian.AppendUint64(hdr, uint64(len(data)))
	e := &rangeEnc{rng: 0xFFFFFFFF, cacheSize: 1, out: hdr}
	isMatch := newProbs(lzmaStates << lzmaPosBitsMax)
	lit := newProbs(0x300 << lc)
	prev := byte(0)
	for i, c := range data {
		e.bit(&isMatch[i&(1<<pb-1)], 0) // state 0: a literal
		probs := lit[0x300*int(prev>>(8-lc)):]
		sym := uint32(1)
		for k := 7; k >= 0; k-- {
			b := uint32(c>>k) & 1
			e.bit(&probs[sym], b)
			sym = sym<<1 | b
		}
		prev = c
	}
	for range 5 {
		e.shiftLow()
	}
	return e.out
}

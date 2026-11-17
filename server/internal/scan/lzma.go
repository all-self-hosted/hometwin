package scan

import (
	"encoding/binary"
	"errors"
)

// A compact LZMA1 ("LZMA alone", .lzma) decoder after the reference decoder in the LZMA SDK
// (LzmaSpec.cpp). The standard library has none, and the scan models are LZMA streams.

const (
	lzmaProbBits   = 11
	lzmaProbInit   = 1 << (lzmaProbBits - 1)
	lzmaMoveBits   = 5
	lzmaTop        = 1 << 24
	lzmaStates     = 12
	lzmaPosBitsMax = 4
	lzmaLenStates  = 4
	lzmaAlignBits  = 4
	lzmaEndPosIdx  = 14
	lzmaFullDist   = 1 << (lzmaEndPosIdx >> 1)
	lzmaMatchMin   = 2
)

var errLZMA = errors.New("lzma: corrupt data")

type rangeDec struct {
	in    []byte
	pos   int
	rng   uint32
	code  uint32
	short bool // read past the end
}

func (r *rangeDec) byte() byte {
	if r.pos >= len(r.in) {
		r.short = true
		return 0
	}
	b := r.in[r.pos]
	r.pos++
	return b
}

func (r *rangeDec) init() bool {
	r.rng = 0xFFFFFFFF
	if r.byte() != 0 {
		return false
	}
	for range 4 {
		r.code = r.code<<8 | uint32(r.byte())
	}
	return r.code != r.rng
}

func (r *rangeDec) normalize() {
	if r.rng < lzmaTop {
		r.rng <<= 8
		r.code = r.code<<8 | uint32(r.byte())
	}
}

func (r *rangeDec) direct(n int) uint32 {
	var res uint32
	for ; n > 0; n-- {
		r.rng >>= 1
		r.code -= r.rng
		t := 0 - (r.code >> 31)
		r.code += r.rng & t
		r.normalize()
		res = res<<1 + t + 1
	}
	return res
}

func (r *rangeDec) bit(p *uint16) uint32 {
	v := uint32(*p)
	bound := (r.rng >> lzmaProbBits) * v
	var b uint32
	if r.code < bound {
		v += ((1 << lzmaProbBits) - v) >> lzmaMoveBits
		r.rng = bound
	} else {
		v -= v >> lzmaMoveBits
		r.code -= bound
		r.rng -= bound
		b = 1
	}
	*p = uint16(v)
	r.normalize()
	return b
}

func newProbs(n int) []uint16 {
	p := make([]uint16, n)
	for i := range p {
		p[i] = lzmaProbInit
	}
	return p
}

// bitTree decodes n bits most significant first; probs has 1<<n entries.
func (r *rangeDec) bitTree(probs []uint16, n int) uint32 {
	m := uint32(1)
	for range n {
		m = m<<1 + r.bit(&probs[m])
	}
	return m - 1<<n
}

func (r *rangeDec) bitTreeRev(probs []uint16, n int) uint32 {
	m, sym := uint32(1), uint32(0)
	for i := range n {
		b := r.bit(&probs[m])
		m = m<<1 + b
		sym |= b << i
	}
	return sym
}

type lenDec struct {
	choice, choice2 uint16
	low, mid        [1 << lzmaPosBitsMax][]uint16
	high            []uint16
}

func newLenDec() *lenDec {
	d := &lenDec{choice: lzmaProbInit, choice2: lzmaProbInit, high: newProbs(1 << 8)}
	for i := range d.low {
		d.low[i], d.mid[i] = newProbs(1<<3), newProbs(1<<3)
	}
	return d
}

func (d *lenDec) decode(r *rangeDec, posState uint32) uint32 {
	if r.bit(&d.choice) == 0 {
		return r.bitTree(d.low[posState], 3)
	}
	if r.bit(&d.choice2) == 0 {
		return 8 + r.bitTree(d.mid[posState], 3)
	}
	return 16 + r.bitTree(d.high, 8)
}

// unLZMA decodes a whole .lzma stream (13-byte header, then data). The output may not grow
// past limit bytes.
func unLZMA(in []byte, limit int) ([]byte, error) {
	if len(in) < 13 {
		return nil, errLZMA
	}
	d := int(in[0])
	if d >= 9*5*5 {
		return nil, errors.New("lzma: bad properties")
	}
	lc, lp, pb := uint32(d%9), uint32(d/9%5), uint32(d/45)
	dict := binary.LittleEndian.Uint32(in[1:5])
	size := binary.LittleEndian.Uint64(in[5:13])
	known := size != ^uint64(0)
	if known && size > uint64(limit) {
		return nil, errors.New("lzma: stream too large")
	}
	if dict < 1<<12 {
		dict = 1 << 12
	}

	r := &rangeDec{in: in[13:]}
	if !r.init() {
		return nil, errLZMA
	}
	out := make([]byte, 0, min(limit, max(len(in)*3, 1<<16)))

	lit := newProbs(0x300 << (lc + lp))
	posSlot := [lzmaLenStates][]uint16{}
	for i := range posSlot {
		posSlot[i] = newProbs(1 << 6)
	}
	posDec := newProbs(1 + lzmaFullDist - lzmaEndPosIdx)
	align := newProbs(1 << lzmaAlignBits)
	isMatch := newProbs(lzmaStates << lzmaPosBitsMax)
	isRep := newProbs(lzmaStates)
	isRepG0 := newProbs(lzmaStates)
	isRepG1 := newProbs(lzmaStates)
	isRepG2 := newProbs(lzmaStates)
	isRep0Long := newProbs(lzmaStates << lzmaPosBitsMax)
	lens, repLens := newLenDec(), newLenDec()

	var state, rep0, rep1, rep2, rep3 uint32
	pbMask, lpMask := uint32(1)<<pb-1, uint32(1)<<lp-1
	for {
		if r.short {
			return nil, errors.New("lzma: truncated stream")
		}
		if known && uint64(len(out)) == size {
			// The end marker is optional once the size is reached.
			if r.code == 0 {
				return out, nil
			}
		}
		if len(out) > limit {
			return nil, errors.New("lzma: stream too large")
		}
		posState := uint32(len(out)) & pbMask
		if r.bit(&isMatch[state<<lzmaPosBitsMax+posState]) == 0 {
			prev := uint32(0)
			if len(out) > 0 {
				prev = uint32(out[len(out)-1])
			}
			ls := (uint32(len(out))&lpMask)<<lc + prev>>(8-lc)
			probs := lit[0x300*ls : 0x300*(ls+1)]
			sym := uint32(1)
			if state >= 7 {
				if int(rep0) >= len(out) {
					return nil, errLZMA
				}
				mb := uint32(out[len(out)-int(rep0)-1])
				for sym < 0x100 {
					mbit := mb >> 7 & 1
					mb <<= 1
					b := r.bit(&probs[(1+mbit)<<8+sym])
					sym = sym<<1 | b
					if mbit != b {
						break
					}
				}
			}
			for sym < 0x100 {
				sym = sym<<1 | r.bit(&probs[sym])
			}
			out = append(out, byte(sym))
			switch {
			case state < 4:
				state = 0
			case state < 10:
				state -= 3
			default:
				state -= 6
			}
			continue
		}

		var n uint32
		if r.bit(&isRep[state]) != 0 {
			if len(out) == 0 {
				return nil, errLZMA
			}
			if r.bit(&isRepG0[state]) == 0 {
				if r.bit(&isRep0Long[state<<lzmaPosBitsMax+posState]) == 0 {
					if state < 7 {
						state = 9
					} else {
						state = 11
					}
					out = append(out, out[len(out)-int(rep0)-1])
					continue
				}
			} else {
				var dist uint32
				if r.bit(&isRepG1[state]) == 0 {
					dist = rep1
				} else {
					if r.bit(&isRepG2[state]) == 0 {
						dist = rep2
					} else {
						dist = rep3
						rep3 = rep2
					}
					rep2 = rep1
				}
				rep1 = rep0
				rep0 = dist
			}
			n = repLens.decode(r, posState)
			if state < 7 {
				state = 8
			} else {
				state = 11
			}
		} else {
			rep3, rep2, rep1 = rep2, rep1, rep0
			n = lens.decode(r, posState)
			if state < 7 {
				state = 7
			} else {
				state = 10
			}
			// Distance.
			ls := min(n, lzmaLenStates-1)
			slot := r.bitTree(posSlot[ls], 6)
			if slot < 4 {
				rep0 = slot
			} else {
				nb := int(slot>>1) - 1
				dist := (2 | slot&1) << nb
				if slot < lzmaEndPosIdx {
					dist += r.bitTreeRev(posDec[dist-slot:], nb)
				} else {
					dist += r.direct(nb-lzmaAlignBits) << lzmaAlignBits
					dist += r.bitTreeRev(align, lzmaAlignBits)
				}
				rep0 = dist
			}
			if rep0 == 0xFFFFFFFF { // end marker
				if r.code != 0 || (known && uint64(len(out)) != size) {
					return nil, errLZMA
				}
				return out, nil
			}
			if rep0 >= dict || int(rep0) >= len(out) {
				return nil, errLZMA
			}
		}
		n += lzmaMatchMin
		if known && uint64(len(out))+uint64(n) > size {
			return nil, errLZMA
		}
		src := len(out) - int(rep0) - 1
		for i := range int(n) {
			out = append(out, out[src+i])
		}
	}
}

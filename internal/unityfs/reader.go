package unityfs

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

var errShort = errors.New("unityfs: unexpected end of data")

// reader is a cursor over a byte slice with a sticky error: after the first
// short read every call returns zero values and err stays set, so parsers
// check it once at the end instead of after every field.
type reader struct {
	b   []byte
	pos int
	le  bool
	err error
}

func (r *reader) take(n int) []byte {
	if r.err != nil {
		return nil
	}
	if n < 0 || r.pos+n > len(r.b) {
		r.err = fmt.Errorf("%w (need %d at offset %d of %d)", errShort, n, r.pos, len(r.b))
		return nil
	}
	s := r.b[r.pos : r.pos+n]
	r.pos += n
	return s
}

func (r *reader) u8() byte {
	if s := r.take(1); s != nil {
		return s[0]
	}
	return 0
}

func (r *reader) u16() uint16 {
	s := r.take(2)
	if s == nil {
		return 0
	}
	if r.le {
		return binary.LittleEndian.Uint16(s)
	}
	return binary.BigEndian.Uint16(s)
}

func (r *reader) u32() uint32 {
	s := r.take(4)
	if s == nil {
		return 0
	}
	if r.le {
		return binary.LittleEndian.Uint32(s)
	}
	return binary.BigEndian.Uint32(s)
}

func (r *reader) u64() uint64 {
	s := r.take(8)
	if s == nil {
		return 0
	}
	if r.le {
		return binary.LittleEndian.Uint64(s)
	}
	return binary.BigEndian.Uint64(s)
}

func (r *reader) i32() int32 { return int32(r.u32()) }
func (r *reader) i64() int64 { return int64(r.u64()) }

func (r *reader) f32() float64 { return float64(math.Float32frombits(r.u32())) }
func (r *reader) f64() float64 { return math.Float64frombits(r.u64()) }

// cstr reads a NUL-terminated string.
func (r *reader) cstr() string {
	if r.err != nil {
		return ""
	}
	for i := r.pos; i < len(r.b); i++ {
		if r.b[i] == 0 {
			s := string(r.b[r.pos:i])
			r.pos = i + 1
			return s
		}
	}
	r.err = fmt.Errorf("%w (unterminated string at offset %d)", errShort, r.pos)
	return ""
}

func (r *reader) align(n int) {
	if rem := r.pos % n; rem != 0 {
		r.take(n - rem)
	}
}

package unityfs

import "fmt"

// lz4Block decodes one raw LZ4 block (no frame header) that expands to exactly
// dstSize bytes. UnityFS uses LZ4 and LZ4HC, which share this block format.
func lz4Block(src []byte, dstSize int) ([]byte, error) {
	dst := make([]byte, 0, dstSize)
	length := func(i *int, n int) (int, error) {
		if n != 15 {
			return n, nil
		}
		for {
			if *i >= len(src) {
				return 0, errShort
			}
			b := src[*i]
			*i++
			n += int(b)
			if b != 255 {
				return n, nil
			}
		}
	}
	for i := 0; i < len(src); {
		tok := src[i]
		i++
		lit, err := length(&i, int(tok>>4))
		if err != nil {
			return nil, fmt.Errorf("lz4: %w", err)
		}
		if i+lit > len(src) {
			return nil, fmt.Errorf("lz4: literal run past end of input")
		}
		dst = append(dst, src[i:i+lit]...)
		i += lit
		if i >= len(src) { // the last sequence carries literals only
			break
		}
		if i+2 > len(src) {
			return nil, fmt.Errorf("lz4: truncated match offset")
		}
		off := int(src[i]) | int(src[i+1])<<8
		i += 2
		if off == 0 || off > len(dst) {
			return nil, fmt.Errorf("lz4: bad match offset %d at output %d", off, len(dst))
		}
		ml, err := length(&i, int(tok&15))
		if err != nil {
			return nil, fmt.Errorf("lz4: %w", err)
		}
		ml += 4
		start := len(dst) - off
		if off >= ml {
			dst = append(dst, dst[start:start+ml]...)
			continue
		}
		for k := 0; k < ml; k++ { // overlapping match repeats the window
			dst = append(dst, dst[start+k])
		}
	}
	if len(dst) != dstSize {
		return nil, fmt.Errorf("lz4: decoded %d bytes, want %d", len(dst), dstSize)
	}
	return dst, nil
}

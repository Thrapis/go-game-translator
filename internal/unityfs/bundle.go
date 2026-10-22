// Package unityfs reads Unity asset bundles (UnityFS) and the serialized
// files inside them, with a generic type-tree driven object decoder. It is
// read-only and has no dependency outside the standard library.
//
// Supported: bundle format 6+ with no/LZ4/LZ4HC compression, serialized file
// format 17+ (Unity 2017.x to Unity 6). LZMA-compressed bundles are rejected.
package unityfs

import (
	"bytes"
	"fmt"
	"os"
)

// Compression kinds in the low bits of the bundle and block flags.
const (
	compNone = 0
	compLZMA = 1
	compLZ4  = 2
	compLZ4H = 3
)

// Bundle is a decompressed UnityFS archive: a set of named nodes, each either
// a serialized file or a raw resource stream (.resS).
type Bundle struct {
	Version       uint32
	PlayerVersion string // always "5.x.x"
	UnityVersion  string
	Nodes         []Node

	data []byte
}

// Node is one file inside a bundle.
type Node struct {
	Path   string
	Offset int64
	Size   int64
	Flags  uint32
}

// IsSerialized reports whether the node is a serialized asset file (as
// opposed to a resource stream).
func (n Node) IsSerialized() bool { return n.Flags&4 != 0 }

// Open reads and decompresses the bundle at path.
func Open(path string) (*Bundle, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	bu, err := Parse(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return bu, nil
}

// Parse decompresses a UnityFS bundle held in memory.
func Parse(b []byte) (*Bundle, error) {
	r := &reader{b: b}
	if sig := r.cstr(); sig != "UnityFS" {
		return nil, fmt.Errorf("unityfs: not a UnityFS bundle (signature %q)", sig)
	}
	bu := &Bundle{Version: r.u32()}
	bu.PlayerVersion = r.cstr()
	bu.UnityVersion = r.cstr()
	r.i64() // total size
	compSize := int(r.u32())
	rawSize := int(r.u32())
	flags := r.u32()
	if r.err != nil {
		return nil, r.err
	}

	if bu.Version >= 7 {
		r.align(16)
	}
	var info []byte
	if flags&0x80 != 0 { // block info stored at the end of the file
		if compSize > len(b) {
			return nil, errShort
		}
		info = b[len(b)-compSize:]
	} else {
		info = r.take(compSize)
	}
	if r.err != nil {
		return nil, r.err
	}
	info, err := decompress(info, rawSize, int(flags&0x3F))
	if err != nil {
		return nil, fmt.Errorf("block info: %w", err)
	}
	if flags&0x80 == 0 && flags&0x200 != 0 { // padding before the data blocks
		r.align(16)
	}

	ir := &reader{b: info}
	ir.take(16) // uncompressed data hash
	type block struct {
		raw, comp int
		flags     uint16
	}
	blocks := make([]block, ir.u32())
	for i := range blocks {
		blocks[i] = block{raw: int(ir.u32()), comp: int(ir.u32()), flags: ir.u16()}
	}
	nodes := make([]Node, ir.u32())
	for i := range nodes {
		nodes[i] = Node{Offset: ir.i64(), Size: ir.i64(), Flags: ir.u32(), Path: ir.cstr()}
	}
	if ir.err != nil {
		return nil, fmt.Errorf("block info: %w", ir.err)
	}

	var total int
	for _, bl := range blocks {
		total += bl.raw
	}
	data := make([]byte, 0, total)
	for i, bl := range blocks {
		chunk := r.take(bl.comp)
		if r.err != nil {
			return nil, fmt.Errorf("block %d: %w", i, r.err)
		}
		out, err := decompress(chunk, bl.raw, int(bl.flags&0x3F))
		if err != nil {
			return nil, fmt.Errorf("block %d: %w", i, err)
		}
		data = append(data, out...)
	}
	for _, n := range nodes {
		if n.Offset < 0 || n.Size < 0 || n.Offset+n.Size > int64(len(data)) {
			return nil, fmt.Errorf("node %q (%d+%d) outside decompressed data (%d)", n.Path, n.Offset, n.Size, len(data))
		}
	}
	bu.Nodes, bu.data = nodes, data
	return bu, nil
}

func decompress(src []byte, rawSize, kind int) ([]byte, error) {
	switch kind {
	case compNone:
		if len(src) != rawSize {
			return nil, fmt.Errorf("stored size %d, want %d", len(src), rawSize)
		}
		return src, nil
	case compLZ4, compLZ4H:
		return lz4Block(src, rawSize)
	case compLZMA:
		return nil, fmt.Errorf("LZMA-compressed bundles are not supported")
	}
	return nil, fmt.Errorf("unknown compression %d", kind)
}

// Data returns the bytes of a node. The slice aliases the bundle's memory.
func (b *Bundle) Data(n Node) []byte { return b.data[n.Offset : n.Offset+n.Size] }

// Files parses every serialized file in the bundle.
func (b *Bundle) Files() ([]*File, error) {
	var out []*File
	for _, n := range b.Nodes {
		if !n.IsSerialized() {
			continue
		}
		f, err := ParseFile(b.Data(n))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", n.Path, err)
		}
		f.Name = n.Path
		out = append(out, f)
	}
	return out, nil
}

// SetNode replaces the contents of the node at path.
func (b *Bundle) SetNode(path string, data []byte) error {
	for i := range b.Nodes {
		if b.Nodes[i].Path != path {
			continue
		}
		var out []byte
		for j, n := range b.Nodes {
			for len(out)%16 != 0 {
				out = append(out, 0)
			}
			d := b.Data(n)
			if j == i {
				d = data
			}
			b.Nodes[j].Offset = int64(len(out))
			b.Nodes[j].Size = int64(len(d))
			out = append(out, d...)
		}
		b.data = out
		return nil
	}
	return fmt.Errorf("unityfs: no node %q in bundle", path)
}

// blockSize is the uncompressed chunk size Marshal splits the data into.
const blockSize = 128 << 10

// Marshal serialises the bundle with no compression (block info and data are
// stored as-is), which every UnityFS reader accepts. The result is larger
// than the LZ4 original but needs no compressor.
func (b *Bundle) Marshal() []byte {
	info := &out{}
	info.Write(make([]byte, 16)) // data hash, unused by the runtime
	nBlocks := (len(b.data) + blockSize - 1) / blockSize
	info.u32(uint32(nBlocks))
	for left := len(b.data); left > 0; left -= blockSize {
		n := min(left, blockSize)
		info.u32(uint32(n))
		info.u32(uint32(n))
		info.u16(compNone)
	}
	info.u32(uint32(len(b.Nodes)))
	for _, n := range b.Nodes {
		info.u64(uint64(n.Offset))
		info.u64(uint64(n.Size))
		info.u32(n.Flags)
		info.cstr(n.Path)
	}

	h := &out{}
	h.cstr("UnityFS")
	h.u32(b.Version)
	h.cstr(b.PlayerVersion)
	h.cstr(b.UnityVersion)
	sizePos := h.Len()
	h.u64(0) // total size, patched below
	h.u32(uint32(info.Len()))
	h.u32(uint32(info.Len()))
	h.u32(0x40) // block info and directory combined, uncompressed
	if b.Version >= 7 {
		for h.Len()%16 != 0 {
			h.WriteByte(0)
		}
	}
	h.Write(info.Bytes())
	h.Write(b.data)
	res := h.Bytes()
	for i := 0; i < 8; i++ {
		res[sizePos+i] = byte(uint64(len(res)) >> (8 * (7 - i)))
	}
	return res
}

// out is a big-endian byte builder for bundle headers.
type out struct{ bytes.Buffer }

func (o *out) u16(v uint16)  { o.Write([]byte{byte(v >> 8), byte(v)}) }
func (o *out) u32(v uint32)  { o.u16(uint16(v >> 16)); o.u16(uint16(v)) }
func (o *out) u64(v uint64)  { o.u32(uint32(v >> 32)); o.u32(uint32(v)) }
func (o *out) cstr(s string) { o.WriteString(s); o.WriteByte(0) }

package unityfs

import "fmt"

// Class IDs of the Unity object types this package knows by name.
const (
	ClassTextAsset    = 49
	ClassMonoBehavior = 114
)

// File is a parsed serialized asset file: its type table and object index.
type File struct {
	Name         string
	Version      uint32
	UnityVersion string
	Types        []*Type
	Objects      []*Object

	data []byte
	le   bool
}

// Type is one entry of the file's type table. Tree is nil when the build
// stripped type trees, in which case objects of the type cannot be decoded.
type Type struct {
	ClassID     int32
	ScriptID    [16]byte // MonoBehaviour script hash; identifies the C# class
	HasScriptID bool
	Tree        *TypeNode
}

// Object is one asset in a File.
type Object struct {
	PathID  int64
	ClassID int32
	Size    int64

	typ      *Type
	file     *File
	off      int64
	tableOff int // position of this object's byte-start field in the file
}

// ParseFile parses a serialized file (format 17+).
func ParseFile(b []byte) (*File, error) {
	r := &reader{b: b} // header starts big-endian
	r.u32()            // metadata size
	r.u32()            // file size
	f := &File{Version: r.u32(), data: b}
	dataOff := int64(r.u32())
	var endian byte
	if f.Version >= 9 {
		endian = r.u8()
		r.take(3)
	}
	if f.Version >= 22 {
		r.u32()
		r.i64()
		dataOff = r.i64()
		r.i64()
	}
	if r.err != nil {
		return nil, r.err
	}
	if f.Version < 17 {
		return nil, fmt.Errorf("unityfs: serialized file version %d is not supported (need 17+)", f.Version)
	}
	f.le = endian == 0
	r.le = f.le

	f.UnityVersion = r.cstr()
	r.i32() // target platform
	hasTree := r.u8() != 0
	nTypes := int(r.i32())
	if r.err != nil || nTypes < 0 || nTypes > len(b) {
		return nil, fmt.Errorf("unityfs: bad type count: %v", r.err)
	}
	for i := 0; i < nTypes; i++ {
		t := readType(r, f.Version, hasTree, false)
		if r.err != nil {
			return nil, fmt.Errorf("type %d: %w", i, r.err)
		}
		f.Types = append(f.Types, t)
	}
	nObjs := int(r.i32())
	if r.err != nil || nObjs < 0 || nObjs > len(b) {
		return nil, fmt.Errorf("unityfs: bad object count: %v", r.err)
	}
	for i := 0; i < nObjs; i++ {
		r.align(4)
		o := &Object{file: f, PathID: r.i64()}
		o.tableOff = r.pos
		var start int64
		if f.Version >= 22 {
			start = r.i64()
		} else {
			start = int64(r.u32())
		}
		o.off = dataOff + start
		o.Size = int64(r.u32())
		ti := int(r.i32())
		if r.err != nil {
			return nil, fmt.Errorf("object %d: %w", i, r.err)
		}
		if ti < 0 || ti >= len(f.Types) {
			return nil, fmt.Errorf("object %d: type index %d out of range", i, ti)
		}
		o.typ, o.ClassID = f.Types[ti], f.Types[ti].ClassID
		if o.off < 0 || o.off+o.Size > int64(len(b)) {
			return nil, fmt.Errorf("object %d: data %d+%d outside file", i, o.off, o.Size)
		}
		f.Objects = append(f.Objects, o)
	}
	return f, nil
}

func readType(r *reader, ver uint32, hasTree, isRef bool) *Type {
	t := &Type{ClassID: r.i32()}
	r.u8() // is stripped
	scriptIdx := int16(r.u16())
	if (isRef && scriptIdx >= 0) || t.ClassID == ClassMonoBehavior {
		copy(t.ScriptID[:], r.take(16))
		t.HasScriptID = true
	}
	r.take(16) // old type hash
	if !hasTree {
		return t
	}
	nNodes := int(r.i32())
	strLen := int(r.i32())
	if r.err != nil || nNodes < 0 || nNodes > 1<<20 || strLen < 0 {
		if r.err == nil {
			r.err = fmt.Errorf("unityfs: bad type tree size %d/%d", nNodes, strLen)
		}
		return t
	}
	type raw struct {
		level                      byte
		typeOff, nameOff           uint32
		byteSize, index, metaFlags int32
	}
	nodes := make([]raw, nNodes)
	for i := range nodes {
		r.u16() // node version
		nodes[i].level = r.u8()
		r.u8() // type flags
		nodes[i].typeOff = r.u32()
		nodes[i].nameOff = r.u32()
		nodes[i].byteSize = r.i32()
		nodes[i].index = r.i32()
		nodes[i].metaFlags = r.i32()
		if ver >= 19 {
			r.u64() // ref type hash
		}
	}
	strs := r.take(strLen)
	if ver >= 21 {
		if isRef {
			r.cstr()
			r.cstr()
			r.cstr()
		} else if n := int(r.i32()); n >= 0 && n <= len(r.b) {
			r.take(4 * n) // type dependencies
		}
	}
	if r.err != nil {
		return t
	}
	str := func(off uint32) string {
		if off&0x80000000 != 0 {
			return commonString(off & 0x7FFFFFFF)
		}
		if int(off) >= len(strs) {
			return ""
		}
		end := int(off)
		for end < len(strs) && strs[end] != 0 {
			end++
		}
		return string(strs[off:end])
	}
	// The flat list is a pre-order walk; levels rebuild the tree.
	var stack []*TypeNode
	for _, n := range nodes {
		tn := &TypeNode{Type: str(n.typeOff), Name: str(n.nameOff), ByteSize: n.byteSize, MetaFlags: n.metaFlags}
		for len(stack) > int(n.level) {
			stack = stack[:len(stack)-1]
		}
		if len(stack) == 0 {
			if t.Tree == nil {
				t.Tree = tn
			}
		} else {
			p := stack[len(stack)-1]
			p.Children = append(p.Children, tn)
		}
		stack = append(stack, tn)
	}
	return t
}

// Raw returns the object's serialized bytes.
func (o *Object) Raw() []byte { return o.file.data[o.off : o.off+o.Size] }

// ScriptID returns the MonoBehaviour script hash, if the object has one.
func (o *Object) ScriptID() ([16]byte, bool) { return o.typ.ScriptID, o.typ.HasScriptID }

// HasTree reports whether the object can be decoded by Read.
func (o *Object) HasTree() bool { return o.typ.Tree != nil }

// Read decodes the object through its type tree into nested map[string]any,
// []any, []byte, string, bool, int64, uint64 and float64 values.
func (o *Object) Read() (map[string]any, error) {
	if o.typ.Tree == nil {
		return nil, fmt.Errorf("class %d has no type tree (stripped build)", o.ClassID)
	}
	r := &reader{b: o.Raw(), le: o.file.le}
	v := readValue(r, o.typ.Tree)
	if r.err != nil {
		return nil, r.err
	}
	m, _ := v.(map[string]any)
	return m, nil
}

// Replace returns a copy of the file in which object o has the given
// serialized bytes. The new data is appended after the existing data and the
// object's table entry is repointed to it; the old bytes become unreferenced
// padding. Nothing else in the file moves, so other objects and their ids are
// untouched.
func (f *File) Replace(o *Object, raw []byte) ([]byte, error) {
	if o.file != f {
		return nil, fmt.Errorf("unityfs: object does not belong to this file")
	}
	out := append([]byte(nil), f.data...)
	for len(out)%8 != 0 {
		out = append(out, 0)
	}
	dataOff := o.off - o.start()
	start := int64(len(out)) - dataOff
	out = append(out, raw...)

	put := func(at int, v uint64, n int) {
		for i := 0; i < n; i++ {
			shift := 8 * i
			if !f.le {
				shift = 8 * (n - 1 - i)
			}
			out[at+i] = byte(v >> shift)
		}
	}
	width := 4
	if f.Version >= 22 {
		width = 8
	}
	if width == 4 && start > 0xFFFFFFFF {
		return nil, fmt.Errorf("unityfs: file too large for a 32-bit object offset")
	}
	put(o.tableOff, uint64(start), width)
	put(o.tableOff+width, uint64(len(raw)), 4)
	// File size lives in the (always big-endian) header.
	if f.Version >= 22 {
		for i := 0; i < 8; i++ {
			out[24+i] = byte(uint64(len(out)) >> (8 * (7 - i)))
		}
	} else {
		for i := 0; i < 4; i++ {
			out[4+i] = byte(uint64(len(out)) >> (8 * (3 - i)))
		}
	}
	return out, nil
}

// start is the object's offset relative to the data area, recovered from the
// table so Replace does not need to store the data offset separately.
func (o *Object) start() int64 {
	r := &reader{b: o.file.data, pos: o.tableOff, le: o.file.le}
	if o.file.Version >= 22 {
		return r.i64()
	}
	return int64(r.u32())
}

// TextAsset reads a TextAsset's name and script from its fixed layout
// {m_Name string, m_Script string}, so it also works on player files built
// without type trees (resources.assets). A type tree, when present, must
// agree with that layout, and the two strings must fill the object exactly;
// otherwise the layout assumption is wrong and an error is returned.
func (o *Object) TextAsset() (name string, script []byte, err error) {
	if o.ClassID != ClassTextAsset {
		return "", nil, fmt.Errorf("unityfs: object %d is class %d, not a TextAsset", o.PathID, o.ClassID)
	}
	if t := o.typ.Tree; t != nil && (len(t.Children) != 2 ||
		t.Children[0].Name != "m_Name" || t.Children[1].Name != "m_Script" ||
		t.Children[0].Type != "string" || t.Children[1].Type != "string") {
		return "", nil, fmt.Errorf("unityfs: TextAsset %d has an unexpected type tree", o.PathID)
	}
	r := &reader{b: o.Raw(), le: o.file.le}
	name = string(r.take(int(r.u32())))
	r.align(4)
	script = r.take(int(r.u32()))
	r.align(4)
	if r.err != nil {
		return "", nil, r.err
	}
	if r.pos != len(r.b) {
		return "", nil, fmt.Errorf("unityfs: TextAsset %d has %d bytes after m_Script", o.PathID, len(r.b)-r.pos)
	}
	return name, script, nil
}

// ReplaceTextAsset returns a copy of the file in which the TextAsset o has
// the given script (its m_Script bytes) and everything else unchanged.
func (f *File) ReplaceTextAsset(o *Object, script []byte) ([]byte, error) {
	if _, _, err := o.TextAsset(); err != nil {
		return nil, err
	}
	r := &reader{b: o.Raw(), le: f.le}
	n := int(r.u32())
	r.take(n)
	r.align(4)
	raw := append([]byte(nil), o.Raw()[:r.pos]...)
	var ln [4]byte
	if f.le {
		ln = [4]byte{byte(len(script)), byte(len(script) >> 8), byte(len(script) >> 16), byte(len(script) >> 24)}
	} else {
		ln = [4]byte{byte(len(script) >> 24), byte(len(script) >> 16), byte(len(script) >> 8), byte(len(script))}
	}
	raw = append(raw, ln[:]...)
	raw = append(raw, script...)
	for len(raw)%4 != 0 {
		raw = append(raw, 0)
	}
	return f.Replace(o, raw)
}

// Write serialises a value shaped like the result of Read back into the
// object's binary form, using the object's type tree.
func (o *Object) Write(v map[string]any) ([]byte, error) {
	if o.typ.Tree == nil {
		return nil, fmt.Errorf("class %d has no type tree (stripped build)", o.ClassID)
	}
	w := &writer{le: o.file.le}
	if err := writeValue(w, o.typ.Tree, v); err != nil {
		return nil, err
	}
	return w.b, nil
}

// ReplaceObject returns a copy of the file in which object o holds the
// serialised form of v (a value shaped like the result of o.Read).
func (f *File) ReplaceObject(o *Object, v map[string]any) ([]byte, error) {
	raw, err := o.Write(v)
	if err != nil {
		return nil, err
	}
	return f.Replace(o, raw)
}

package unityfs

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestLZ4Block(t *testing.T) {
	// "abcabcabcabcX": 3 literals, an overlapping match (offset 3, length 9), 1 literal.
	enc := []byte{0x35, 'a', 'b', 'c', 0x03, 0x00, 0x10, 'X'}
	got, err := lz4Block(enc, 13)
	if err != nil {
		t.Fatal(err)
	}
	if want := "abcabcabcabcX"; string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if _, err := lz4Block(enc, 12); err == nil {
		t.Error("wrong expected size must be an error")
	}
	if _, err := lz4Block([]byte{0x10, 'a', 0x05, 0x00}, 99); err == nil {
		t.Error("match offset before the start of output must be an error")
	}
}

// lz4Literals encodes data as a single literal-only LZ4 sequence.
func lz4Literals(data []byte) []byte {
	out := []byte{}
	n := len(data)
	if n < 15 {
		out = append(out, byte(n<<4))
	} else {
		out = append(out, 0xF0)
		for rest := n - 15; ; rest -= 255 {
			if rest < 255 {
				out = append(out, byte(rest))
				break
			}
			out = append(out, 255)
		}
	}
	return append(out, data...)
}

// w is a tiny builder for the binary layouts under test.
type w struct {
	bytes.Buffer
	le bool
}

func (b *w) u16(v uint16) {
	if b.le {
		binary.Write(b, binary.LittleEndian, v)
	} else {
		binary.Write(b, binary.BigEndian, v)
	}
}

func (b *w) u32(v uint32) {
	if b.le {
		binary.Write(b, binary.LittleEndian, v)
	} else {
		binary.Write(b, binary.BigEndian, v)
	}
}

func (b *w) u64(v uint64) {
	if b.le {
		binary.Write(b, binary.LittleEndian, v)
	} else {
		binary.Write(b, binary.BigEndian, v)
	}
}

func (b *w) cstr(s string) { b.WriteString(s); b.WriteByte(0) }

func (b *w) align(n int) {
	for b.Len()%n != 0 {
		b.WriteByte(0)
	}
}

func (b *w) str(s string) {
	b.u32(uint32(len(s)))
	b.WriteString(s)
	b.align(4)
}

// serializedTextAsset builds a v22 little-endian serialized file with one
// TextAsset object {m_Name, m_Script} and a type tree.
func serializedTextAsset(name, script string) []byte { return buildSerialized(name, script, true) }

// buildSerialized is serializedTextAsset with an optional type tree; player
// files such as resources.assets are built without one.
func buildSerialized(name, script string, tree bool) []byte {
	// Object data.
	obj := &w{le: true}
	obj.str(name)
	obj.str(script)

	h := &w{} // header is big-endian
	h.u32(0)
	h.u32(0)
	h.u32(22)
	h.u32(0)
	h.WriteByte(0) // little-endian body
	h.Write([]byte{0, 0, 0})
	h.u32(0)
	h.u64(0)
	dataOffPos := h.Len()
	h.u64(0) // data offset, patched below
	h.u64(0)

	m := &w{le: true}
	m.Buffer = h.Buffer
	m.cstr("6000.0.0f1")
	m.u32(19)
	if tree {
		m.WriteByte(1)
	} else {
		m.WriteByte(0)
	}
	m.u32(1)              // one type
	m.u32(ClassTextAsset) // class id
	m.WriteByte(0)        // stripped
	m.u16(0xFFFF)         // script type index -1
	m.Write(make([]byte, 16))
	if tree {
		strs := "TextAsset\x00Base\x00m_Name\x00string\x00m_Script\x00"
		m.u32(3)
		m.u32(uint32(len(strs)))
		node := func(level byte, typ, name uint32) {
			m.u16(1)
			m.WriteByte(level)
			m.WriteByte(0)
			m.u32(typ)
			m.u32(name)
			m.u32(0xFFFFFFFF)
			m.u32(0)
			m.u32(0)
			m.u64(0)
		}
		node(0, 0, 10)  // TextAsset Base
		node(1, 22, 15) // string m_Name
		node(1, 22, 29) // string m_Script
		m.WriteString(strs)
		m.u32(0) // type dependencies
	}
	m.u32(1) // one object
	m.align(4)
	m.u64(1)
	m.u64(0) // byte start within the data area
	m.u32(uint32(obj.Len()))
	m.u32(0) // type index

	dataOff := (m.Len() + 15) / 16 * 16
	for m.Len() < dataOff {
		m.WriteByte(0)
	}
	out := append(m.Bytes(), obj.Bytes()...)
	binary.BigEndian.PutUint64(out[dataOffPos:], uint64(dataOff))
	return out
}

// bundle wraps files into an unencrypted UnityFS v8 bundle whose block info
// and data are both stored with the given compression (0 none, 2 LZ4).
func bundle(file []byte, comp int) []byte {
	pack := func(b []byte) []byte {
		if comp == compLZ4 {
			return lz4Literals(b)
		}
		return b
	}
	info := &w{}
	info.Write(make([]byte, 16))
	info.u32(1)
	data := pack(file)
	info.u32(uint32(len(file)))
	info.u32(uint32(len(data)))
	info.u16(uint16(comp))
	info.u32(1)
	info.u64(0)
	info.u64(uint64(len(file)))
	info.u32(4)
	info.cstr("CAB-test")
	cinfo := pack(info.Bytes())

	b := &w{}
	b.cstr("UnityFS")
	b.u32(8)
	b.cstr("5.x.x")
	b.cstr("6000.0.0f1")
	b.u64(0)
	b.u32(uint32(len(cinfo)))
	b.u32(uint32(info.Len()))
	b.u32(uint32(comp) | 0x40)
	b.align(16)
	b.Write(cinfo)
	b.Write(data)
	return b.Bytes()
}

func TestBundleTextAsset(t *testing.T) {
	file := serializedTextAsset("Localization", `{"a": "привет"}`)
	for _, comp := range []int{compNone, compLZ4} {
		b, err := Parse(bundle(file, comp))
		if err != nil {
			t.Fatalf("compression %d: %v", comp, err)
		}
		files, err := b.Files()
		if err != nil || len(files) != 1 || len(files[0].Objects) != 1 {
			t.Fatalf("compression %d: files=%v err=%v", comp, files, err)
		}
		o := files[0].Objects[0]
		if o.ClassID != ClassTextAsset || o.PathID != 1 {
			t.Fatalf("object = class %d id %d", o.ClassID, o.PathID)
		}
		m, err := o.Read()
		if err != nil {
			t.Fatal(err)
		}
		if m["m_Name"] != "Localization" || m["m_Script"] != `{"a": "привет"}` {
			t.Fatalf("decoded %v", m)
		}
	}
}

func TestBundleRejects(t *testing.T) {
	if _, err := Parse([]byte("UnityWeb\x00")); err == nil {
		t.Error("non-UnityFS signature must be rejected")
	}
	if _, err := Parse(bundle(serializedTextAsset("n", "s"), compNone)[:60]); err == nil {
		t.Error("truncated bundle must be rejected")
	}
}

func TestReadValue(t *testing.T) {
	// struct { int n; vector<UInt8> raw; string s; bool ok } with 4-byte alignment after raw.
	arr := &TypeNode{Type: "Array", Children: []*TypeNode{{Type: "int", Name: "size"}, {Type: "UInt8", Name: "data"}}, MetaFlags: metaAlign}
	root := &TypeNode{Type: "T", Children: []*TypeNode{
		{Type: "int", Name: "n"},
		{Type: "vector", Name: "raw", Children: []*TypeNode{arr}},
		{Type: "string", Name: "s"},
		{Type: "bool", Name: "ok"},
	}}
	b := &w{le: true}
	b.u32(7)
	b.u32(3)
	b.Write([]byte{1, 2, 3})
	b.align(4)
	b.str("hey")
	b.WriteByte(1)

	r := &reader{b: b.Bytes(), le: true}
	got := readValue(r, root).(map[string]any)
	if r.err != nil {
		t.Fatal(r.err)
	}
	if got["n"] != int64(7) || !bytes.Equal(got["raw"].([]byte), []byte{1, 2, 3}) || got["s"] != "hey" || got["ok"] != true {
		t.Fatalf("got %v", got)
	}

	// An array claiming more elements than there are bytes must fail, not allocate.
	r = &reader{b: []byte{0xFF, 0xFF, 0xFF, 0x7F}, le: true}
	readValue(r, arr)
	if r.err == nil {
		t.Error("oversized array length must be an error")
	}
}

func TestCommonStrings(t *testing.T) {
	// Offsets documented by Unity's built-in table.
	for off, want := range map[uint32]string{0: "AABB", 5: "AnimationClip", 49: "Array", 76: "bool"} {
		if got := commonString(off); got != want {
			t.Errorf("commonString(%d) = %q, want %q", off, got, want)
		}
	}
}

func TestReplaceTextAssetAndMarshal(t *testing.T) {
	file := serializedTextAsset("Localization", `{"a": "привет"}`)
	b, err := Parse(bundle(file, compLZ4))
	if err != nil {
		t.Fatal(err)
	}
	files, err := b.Files()
	if err != nil {
		t.Fatal(err)
	}
	f := files[0]
	newScript := `{"a": "прывітанне", "b": "ў" }` // length not a multiple of 4
	nf, err := f.ReplaceTextAsset(f.Objects[0], []byte(newScript))
	if err != nil {
		t.Fatal(err)
	}
	if err := b.SetNode(b.Nodes[0].Path, nf); err != nil {
		t.Fatal(err)
	}

	// Re-open the written bundle from scratch and read the object back.
	b2, err := Parse(b.Marshal())
	if err != nil {
		t.Fatal(err)
	}
	if b2.Version != b.Version || b2.UnityVersion != "6000.0.0f1" || b2.PlayerVersion != "5.x.x" {
		t.Fatalf("header not preserved: %+v", b2)
	}
	files2, err := b2.Files()
	if err != nil || len(files2) != 1 || len(files2[0].Objects) != 1 {
		t.Fatalf("files=%v err=%v", files2, err)
	}
	m, err := files2[0].Objects[0].Read()
	if err != nil {
		t.Fatal(err)
	}
	if m["m_Name"] != "Localization" || m["m_Script"] != newScript {
		t.Fatalf("decoded %v", m)
	}
	if err := b.SetNode("missing", nil); err == nil {
		t.Error("unknown node must be an error")
	}
}

func TestTextAssetWithoutTypeTree(t *testing.T) {
	file := buildSerialized("Localization", `{"a": "привет"}`, false)
	f, err := ParseFile(file)
	if err != nil {
		t.Fatal(err)
	}
	o := f.Objects[0]
	if o.HasTree() {
		t.Fatal("fixture must have no type tree")
	}
	name, script, err := o.TextAsset()
	if err != nil || name != "Localization" || string(script) != `{"a": "привет"}` {
		t.Fatalf("got %q %q %v", name, script, err)
	}
	nf, err := f.ReplaceTextAsset(o, []byte(`{"a": "ў"}`))
	if err != nil {
		t.Fatal(err)
	}
	f2, err := ParseFile(nf)
	if err != nil {
		t.Fatal(err)
	}
	if _, script, err := f2.Objects[0].TextAsset(); err != nil || string(script) != `{"a": "ў"}` {
		t.Fatalf("after replace: %q %v", script, err)
	}
	// Trailing bytes after m_Script mean the layout assumption is wrong.
	padded, err := ParseFile(append(append([]byte(nil), file...), 0, 0, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	padded.Objects[0].Size += 4
	if _, _, err := padded.Objects[0].TextAsset(); err == nil {
		t.Error("extra bytes after m_Script must be an error")
	}
}

func TestWriteValueRoundTrip(t *testing.T) {
	arr := func(elem string) *TypeNode {
		return &TypeNode{Type: "Array", Children: []*TypeNode{{Type: "int", Name: "size"}, {Type: elem, Name: "data"}}}
	}
	item := &TypeNode{Type: "Item", Name: "data", Children: []*TypeNode{
		{Type: "string", Name: "title"},
		{Type: "short", Name: "n"},
		{Type: "bool", Name: "on", MetaFlags: metaAlign},
	}}
	root := &TypeNode{Type: "T", Children: []*TypeNode{
		{Type: "UInt8", Name: "b"},
		{Type: "float", Name: "f"},
		{Type: "double", Name: "d"},
		{Type: "long long", Name: "id"},
		{Type: "unsigned int", Name: "u"},
		{Type: "vector", Name: "items", Children: []*TypeNode{arr("Item")}},
		{Type: "vector", Name: "raw", Children: []*TypeNode{arr("UInt8")}},
		{Type: "string", Name: "s"},
	}}
	root.Children[5].Children[0].Children[1] = item

	b := &w{le: true}
	b.WriteByte(7)
	b.u32(0x3FC00000) // 1.5f
	b.u64(0x4005BF0A8B145769)
	b.u64(0xFFFFFFFFFFFFFFFE) // -2
	b.u32(4000000000)
	b.u32(2) // two items
	for _, s := range []string{"ab", "ы"} {
		b.str(s)
		b.u16(0xFFFF) // -1
		b.WriteByte(1)
		b.align(4)
	}
	b.u32(3)
	b.Write([]byte{9, 8, 7}) // no alignment flag: the string follows at offset 3 mod 4
	b.str("конец")

	r := &reader{b: b.Bytes(), le: true}
	v := readValue(r, root)
	if r.err != nil || r.pos != len(r.b) {
		t.Fatalf("read: err=%v pos=%d/%d", r.err, r.pos, len(r.b))
	}
	out := &writer{le: true}
	if err := writeValue(out, root, v); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.b, b.Bytes()) {
		t.Fatalf("round trip differs:\n got %x\nwant %x", out.b, b.Bytes())
	}

	// Editing a value changes only that value.
	m := v.(map[string]any)
	m["items"].([]any)[1].(map[string]any)["title"] = "новы загаловак"
	out = &writer{le: true}
	if err := writeValue(out, root, m); err != nil {
		t.Fatal(err)
	}
	r = &reader{b: out.b, le: true}
	got := readValue(r, root).(map[string]any)
	if r.err != nil || got["items"].([]any)[1].(map[string]any)["title"] != "новы загаловак" || got["s"] != "конец" {
		t.Fatalf("edited value not read back: %v err=%v", got, r.err)
	}

	// A value of the wrong type is an error, not corrupt output.
	m["b"] = "x"
	if err := writeValue(&writer{le: true}, root, m); err == nil {
		t.Error("wrong value type must be an error")
	}
}

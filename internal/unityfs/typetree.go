package unityfs

import (
	"fmt"
	"math"
	"strings"
)

// TypeNode is one field of a serialized type, as stored in the file's type
// tree. Children are the nested fields; an "Array" node has [size, data].
type TypeNode struct {
	Type      string
	Name      string
	ByteSize  int32
	MetaFlags int32
	Children  []*TypeNode
}

const metaAlign = 0x4000 // align the stream to 4 bytes after this field

// commonStrings is Unity's built-in string table. Type trees reference it by
// byte offset (high bit set) instead of repeating these names per file.
var commonStrings = func() map[uint32]string {
	const list = "AABB\x00AnimationClip\x00AnimationCurve\x00AnimationState\x00Array\x00Base\x00BitField\x00bitset\x00bool\x00char\x00ColorRGBA\x00Component\x00data\x00deque\x00double\x00dynamic_array\x00FastPropertyName\x00first\x00float\x00Font\x00GameObject\x00Generic Mono\x00GradientNEW\x00GUID\x00GUIStyle\x00int\x00list\x00long long\x00map\x00Matrix4x4f\x00MdFour\x00MonoBehaviour\x00MonoScript\x00m_ByteSize\x00m_Curve\x00m_EditorClassIdentifier\x00m_EditorHideFlags\x00m_Enabled\x00m_ExtensionPtr\x00m_GameObject\x00m_Index\x00m_IsArray\x00m_IsStatic\x00m_MetaFlag\x00m_Name\x00m_ObjectHideFlags\x00m_PrefabInternal\x00m_PrefabParentObject\x00m_Script\x00m_StaticEditorFlags\x00m_Type\x00m_Version\x00Object\x00pair\x00PPtr<Component>\x00PPtr<GameObject>\x00PPtr<Material>\x00PPtr<MonoBehaviour>\x00PPtr<MonoScript>\x00PPtr<Object>\x00PPtr<Prefab>\x00PPtr<Sprite>\x00PPtr<TextAsset>\x00PPtr<Texture>\x00PPtr<Texture2D>\x00PPtr<Transform>\x00Prefab\x00Quaternionf\x00Rectf\x00RectInt\x00RectOffset\x00second\x00set\x00short\x00size\x00SInt16\x00SInt32\x00SInt64\x00SInt8\x00staticvector\x00string\x00TextAsset\x00TextMesh\x00Texture\x00Texture2D\x00Transform\x00TypelessData\x00UInt16\x00UInt32\x00UInt64\x00UInt8\x00unsigned int\x00unsigned long long\x00unsigned short\x00vector\x00Vector2f\x00Vector3f\x00Vector4f\x00m_ScriptingClassIdentifier\x00Gradient\x00Type*\x00int2_storage\x00int3_storage\x00BoundsInt\x00m_CorrespondingSourceObject\x00m_PrefabInstance\x00m_PrefabAsset\x00FileSize\x00Hash128\x00"
	m := map[uint32]string{}
	off := 0
	for _, s := range strings.Split(strings.TrimSuffix(list, "\x00"), "\x00") {
		m[uint32(off)] = s
		off += len(s) + 1
	}
	return m
}()

func commonString(off uint32) string { return commonStrings[off] }

// maxDepth guards against cyclic or absurd trees in corrupt files.
const maxDepth = 64

func readValue(r *reader, n *TypeNode) any { return readNode(r, n, 0) }

func readNode(r *reader, n *TypeNode, depth int) any {
	if r.err != nil {
		return nil
	}
	if depth > maxDepth {
		r.err = fmt.Errorf("unityfs: type tree deeper than %d", maxDepth)
		return nil
	}
	var v any
	switch n.Type {
	case "SInt8":
		v = int64(int8(r.u8()))
	case "UInt8", "char":
		v = uint64(r.u8())
	case "bool":
		v = r.u8() != 0
	case "SInt16", "short":
		v = int64(int16(r.u16()))
	case "UInt16", "unsigned short":
		v = uint64(r.u16())
	case "SInt32", "int":
		v = int64(r.i32())
	case "UInt32", "unsigned int", "Type*":
		v = uint64(r.u32())
	case "SInt64", "long long":
		v = r.i64()
	case "UInt64", "unsigned long long", "FileSize":
		v = r.u64()
	case "float":
		v = r.f32()
	case "double":
		v = r.f64()
	case "string":
		v = string(r.take(int(r.i32())))
		r.align(4)
	case "TypelessData":
		v = append([]byte(nil), r.take(int(r.i32()))...)
	case "Array":
		v = readArray(r, n, depth)
	default:
		if len(n.Children) == 1 && n.Children[0].Type == "Array" {
			v = readArray(r, n.Children[0], depth+1)
			if n.Children[0].MetaFlags&metaAlign != 0 {
				r.align(4)
			}
			break
		}
		m := make(map[string]any, len(n.Children))
		for _, c := range n.Children {
			m[c.Name] = readNode(r, c, depth+1)
		}
		v = m
	}
	if n.MetaFlags&metaAlign != 0 {
		r.align(4)
	}
	return v
}

// readArray reads a length-prefixed array node ([size, data] children).
func readArray(r *reader, n *TypeNode, depth int) any {
	if len(n.Children) != 2 {
		r.err = fmt.Errorf("unityfs: array node %q has %d children", n.Name, len(n.Children))
		return nil
	}
	size := int(r.i32())
	if r.err != nil {
		return nil
	}
	if size < 0 || size > len(r.b)-r.pos {
		r.err = fmt.Errorf("unityfs: array size %d exceeds remaining %d bytes", size, len(r.b)-r.pos)
		return nil
	}
	elem := n.Children[1]
	if elem.Type == "UInt8" || elem.Type == "SInt8" || elem.Type == "char" {
		return append([]byte(nil), r.take(size)...)
	}
	out := make([]any, 0, size)
	for i := 0; i < size && r.err == nil; i++ {
		out = append(out, readNode(r, elem, depth+1))
	}
	return out
}

// writer is a byte builder in the file's endianness.
type writer struct {
	b  []byte
	le bool
}

func (w *writer) u8(v byte) { w.b = append(w.b, v) }

func (w *writer) uint(v uint64, n int) {
	for i := 0; i < n; i++ {
		shift := 8 * i
		if !w.le {
			shift = 8 * (n - 1 - i)
		}
		w.b = append(w.b, byte(v>>shift))
	}
}

func (w *writer) align(n int) {
	for len(w.b)%n != 0 {
		w.b = append(w.b, 0)
	}
}

// valueError reports a value that does not fit the type tree.
func valueError(n *TypeNode, v any) error {
	return fmt.Errorf("unityfs: field %q (%s): cannot write %T", n.Name, n.Type, v)
}

// writeValue is the inverse of readNode: it serialises v, a value shaped like
// the one Read produces, through the type node n. Alignment follows the same
// rules, so an unchanged value decodes and re-encodes to the same bytes.
func writeValue(w *writer, n *TypeNode, v any) error {
	switch n.Type {
	case "SInt8", "SInt16", "short", "SInt32", "int", "SInt64", "long long":
		x, ok := v.(int64)
		if !ok {
			return valueError(n, v)
		}
		w.uint(uint64(x), intSize(n.Type))
	case "UInt8", "char", "UInt16", "unsigned short", "UInt32", "unsigned int", "Type*", "UInt64", "unsigned long long", "FileSize":
		x, ok := v.(uint64)
		if !ok {
			return valueError(n, v)
		}
		w.uint(x, intSize(n.Type))
	case "bool":
		x, ok := v.(bool)
		if !ok {
			return valueError(n, v)
		}
		if x {
			w.u8(1)
		} else {
			w.u8(0)
		}
	case "float":
		x, ok := v.(float64)
		if !ok {
			return valueError(n, v)
		}
		w.uint(uint64(math.Float32bits(float32(x))), 4)
	case "double":
		x, ok := v.(float64)
		if !ok {
			return valueError(n, v)
		}
		w.uint(math.Float64bits(x), 8)
	case "string":
		s, ok := v.(string)
		if !ok {
			return valueError(n, v)
		}
		w.uint(uint64(len(s)), 4)
		w.b = append(w.b, s...)
		w.align(4)
	case "TypelessData":
		b, ok := v.([]byte)
		if !ok {
			return valueError(n, v)
		}
		w.uint(uint64(len(b)), 4)
		w.b = append(w.b, b...)
	case "Array":
		if err := writeArray(w, n, v); err != nil {
			return err
		}
	default:
		if len(n.Children) == 1 && n.Children[0].Type == "Array" {
			if err := writeArray(w, n.Children[0], v); err != nil {
				return err
			}
			if n.Children[0].MetaFlags&metaAlign != 0 {
				w.align(4)
			}
			break
		}
		m, ok := v.(map[string]any)
		if !ok {
			return valueError(n, v)
		}
		for _, c := range n.Children {
			cv, ok := m[c.Name]
			if !ok {
				return fmt.Errorf("unityfs: field %q of %q is missing", c.Name, n.Name)
			}
			if err := writeValue(w, c, cv); err != nil {
				return err
			}
		}
	}
	if n.MetaFlags&metaAlign != 0 {
		w.align(4)
	}
	return nil
}

func writeArray(w *writer, n *TypeNode, v any) error {
	if len(n.Children) != 2 {
		return fmt.Errorf("unityfs: array node %q has %d children", n.Name, len(n.Children))
	}
	elem := n.Children[1]
	switch a := v.(type) {
	case []byte:
		if elem.Type != "UInt8" && elem.Type != "SInt8" && elem.Type != "char" {
			return valueError(n, v)
		}
		w.uint(uint64(len(a)), 4)
		w.b = append(w.b, a...)
	case []any:
		w.uint(uint64(len(a)), 4)
		for _, x := range a {
			if err := writeValue(w, elem, x); err != nil {
				return err
			}
		}
	default:
		return valueError(n, v)
	}
	return nil
}

func intSize(t string) int {
	switch t {
	case "SInt8", "UInt8", "char":
		return 1
	case "SInt16", "short", "UInt16", "unsigned short":
		return 2
	case "SInt32", "int", "UInt32", "unsigned int", "Type*":
		return 4
	}
	return 8
}

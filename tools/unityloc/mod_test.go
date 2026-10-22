package main

import (
	"bytes"
	"encoding/binary"
	"reflect"
	"testing"
)

func TestSpliceTableTouchesOnlyReplacedValues(t *testing.T) {
	doc := "\xef\xbb\xbf{\n    \"English\": {\n        \"A\": \"one\"\n    },\n    \"Russian\": {\n        \"ID\": \"Russian\",\n        \"A\": \"один\",\n        \"B\"  :  \"два {0}\\n\",\n        \"C\": \"три\"\n    }\n}"
	got, n, missing, err := spliceTable([]byte(doc), "Russian", map[string]string{"A": "адзін <3", "B": "два \"{0}\"\n", "Z": "x"})
	if err != nil {
		t.Fatal(err)
	}
	want := "\xef\xbb\xbf{\n    \"English\": {\n        \"A\": \"one\"\n    },\n    \"Russian\": {\n        \"ID\": \"Russian\",\n        \"A\": \"адзін <3\",\n        \"B\"  :  \"два \\\"{0}\\\"\\n\",\n        \"C\": \"три\"\n    }\n}"
	if string(got) != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	if n != 2 || !reflect.DeepEqual(missing, []string{"Z"}) {
		t.Fatalf("n=%d missing=%v", n, missing)
	}
	if _, _, _, err := spliceTable([]byte(doc), "Klingon", nil); err == nil {
		t.Error("unknown language must be an error")
	}
}

func TestPatchCatalog(t *testing.T) {
	// a name string, then a record [hash ptr][hash text ptr][crc][size][type][provider][name]...
	cat := append([]byte("xxxxxxxx"), []byte("mybundle_assets_all_abc.bundle\x00")...)
	for _, v := range []uint32{1, 1, 0xDEADBEEF, 1234567, 1, 1, 8, 1, 1, 1} {
		cat = binary.LittleEndian.AppendUint32(cat, v)
	}
	crcAt := len(cat) - 4*10 + 8
	got, err := patchCatalog(cat, "mybundle_assets_all_abc.bundle", 1234567, 7654321)
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint32(got[crcAt:]) != 0 || binary.LittleEndian.Uint32(got[crcAt+4:]) != 7654321 {
		t.Fatalf("crc/size = %x", got[crcAt:crcAt+8])
	}
	same := append(append([]byte(nil), got[:crcAt]...), got[crcAt+8:]...)
	orig := append(append([]byte(nil), cat[:crcAt]...), cat[crcAt+8:]...)
	if !bytes.Equal(same, orig) {
		t.Error("bytes outside crc/size changed")
	}
	if _, err := patchCatalog(cat, "other.bundle", 1234567, 1); err == nil {
		t.Error("record of a different bundle must not match")
	}
	if _, err := patchCatalog(cat, "mybundle_assets_all_abc.bundle", 42, 1); err == nil {
		t.Error("unknown size must be an error")
	}
}

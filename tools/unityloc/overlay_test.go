package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Thrapis/go-game-translator/internal/xliff"
)

func writeXLF(t *testing.T, dir, name string, units []xliff.Unit) string {
	t.Helper()
	f := &xliff.File{Original: "x", SourceLang: "ru", TargetLang: "be", Units: units}
	buf := new(bytes.Buffer)
	if _, err := xliff.Write(buf, f); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestOverlayWritesOnlyTranslatedUnitsInOrder(t *testing.T) {
	dir := t.TempDir()
	ui := writeXLF(t, dir, "ui.xlf", []xliff.Unit{
		{ID: "B_KEY", Source: []xliff.Piece{{Text: "б"}}, Target: []xliff.Piece{{Text: "Каманда <b>\"так\"</b>\nрадок2"}}},
		{ID: "UNTRANSLATED", Source: []xliff.Piece{{Text: "в"}}},
		{ID: "A_KEY", Source: []xliff.Piece{{Text: "а "}, {Text: "{0}", Code: true}}, Target: []xliff.Piece{{Text: "Файл "}, {Text: "{0}", Code: true}}},
	})
	dlg := writeXLF(t, dir, "dlg.xlf", []xliff.Unit{
		{ID: "0x01", Source: []xliff.Piece{{Text: "г"}}, Target: []xliff.Piece{{Text: "Прывітанне"}}},
	})
	out := filepath.Join(dir, "sub", "translations.json")
	if err := runOverlay([]string{"-ui", ui, "-dialogue", dlg, "-out", out}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		UI       map[string]string `json:"ui"`
		Dialogue map[string]string `json:"dialogue"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, data)
	}
	want := map[string]string{"B_KEY": "Каманда <b>\"так\"</b>\nрадок2", "A_KEY": "Файл {0}"}
	if !reflect.DeepEqual(got.UI, want) || !reflect.DeepEqual(got.Dialogue, map[string]string{"0x01": "Прывітанне"}) {
		t.Fatalf("got %+v", got)
	}
	// Order follows the XLIFF, and < > stay readable.
	if i, j := bytes.Index(data, []byte(`"B_KEY"`)), bytes.Index(data, []byte(`"A_KEY"`)); i < 0 || j < i {
		t.Errorf("unit order not kept:\n%s", data)
	}
	if !bytes.Contains(data, []byte("<b>")) || bytes.Contains(data, []byte(string(rune(92))+"u003c")) {
		t.Errorf("< must stay readable, not be escaped:\n%s", data)
	}
}

func TestOverlayRejectsDuplicateIDs(t *testing.T) {
	dir := t.TempDir()
	p := writeXLF(t, dir, "d.xlf", []xliff.Unit{
		{ID: "K", Source: []xliff.Piece{{Text: "а"}}, Target: []xliff.Piece{{Text: "б"}}},
		{ID: "K", Source: []xliff.Piece{{Text: "в"}}, Target: []xliff.Piece{{Text: "г"}}},
	})
	if _, _, err := overlayTargets(p); err == nil {
		t.Error("duplicate ids must be an error")
	}
}

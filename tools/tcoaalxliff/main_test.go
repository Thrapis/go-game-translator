package main

import (
	"strings"
	"testing"

	"github.com/Thrapis/go-game-translator/internal/extract/tcoaaltxt"
	"github.com/Thrapis/go-game-translator/internal/xliff"
)

const en = "[VERSION]\r\n3.0.13 : abc\r\n\r\n" +
	"[LANGUAGE]\r\nEnglish\r\n\r\n" +
	"[MENUS]\r\nNew Game    : New Game\r\n\r\n" +
	"[Map001.json]\r\n\r\n" +
	"#a1 (Andy)\r\n: \\c[2]Hi\\c[0], \"you\"\r\n: second row\r\n\r\n" +
	"#a2 (Ash)\r\n: \r\n\r\n" +
	"[CHOICES]\r\n#c1 : Call.\r\n\r\n" +
	"[CHOICES]\r\n#c2 : Run.\r\n"

const be = "[VERSION]\r\n3.0.13 : abc\r\n\r\n" +
	"[LANGUAGE]\r\nБеларуская\r\n\r\n" +
	"[MENUS]\r\nNew Game    : Новая гульня\r\n\r\n" +
	"[Map001.json]\r\n\r\n" +
	"#a1 (Andy)\r\n: \\c[2]Прывітанне\\c[0], \"ты\"\r\n: \r\n\r\n" +
	"#a2 (Ash)\r\n: \r\n\r\n" +
	"[CHOICES]\r\n#c1 : Патэлефанаваць.\r\n\r\n" +
	"[CHOICES]\r\n#c2 : \r\n"

func parse(t *testing.T, s string) *tcoaaltxt.Doc {
	t.Helper()
	d, _, err := tcoaaltxt.ParseDoc(strings.NewReader(s))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestBuildFiles(t *testing.T) {
	files, err := buildFiles(parse(t, en), parse(t, be), "en", "be")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range files {
		names = append(names, f.Original)
	}
	if got := strings.Join(names, ","); got != "MENUS,Map001.json,CHOICES" {
		t.Fatalf("files = %s; metadata sections must be skipped and repeated sections merged", got)
	}

	m := files[1]
	if len(m.Units) != 2 { // the blank "#a2" row is not a unit
		t.Fatalf("Map001 has %d units, want 2: %+v", len(m.Units), m.Units)
	}
	u := m.Units[0]
	if u.Note != "#a1 (Andy)" || u.State != "needs-review-translation" {
		t.Errorf("unit 0 = %+v", u)
	}
	if got := xliff.Plain(u.Source); got != `\c[2]Hi\c[0], "you"` {
		t.Errorf("source = %q", got)
	}
	if !u.Source[0].Code || u.Source[1].Code {
		t.Errorf("only the \\c[n] symbols should be codes: %+v", u.Source)
	}
	if got := xliff.Plain(u.Target); got != `\c[2]Прывітанне\c[0], "ты"` {
		t.Errorf("target = %q", got)
	}
	if m.Units[1].Target != nil {
		t.Errorf("an empty translation must leave the unit untranslated")
	}

	ch := files[2]
	if len(ch.Units) != 2 || ch.Units[0].ID == ch.Units[1].ID {
		t.Errorf("merged CHOICES need unique ids: %+v", ch.Units)
	}
}

func TestBuildFilesRejectsMisalignedDocs(t *testing.T) {
	short := strings.Replace(be, "[CHOICES]\r\n#c2 : \r\n", "", 1)
	if _, err := buildFiles(parse(t, en), parse(t, short), "en", "be"); err == nil {
		t.Fatal("want an error when the two documents have different slots")
	}
}

func TestFileName(t *testing.T) {
	for in, want := range map[string]string{"Map001.json": "Map001.json.xlf", "MENUS": "MENUS.xlf"} {
		if got := fileName(in); got != want {
			t.Errorf("fileName(%q) = %q, want %q", in, got, want)
		}
	}
}

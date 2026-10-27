package wolvenkit

import (
	"bytes"
	"strings"
	"testing"
)

func TestQuote(t *testing.T) {
	cases := map[string]string{
		"Новости":          `"\u041D\u043E\u0432\u043E\u0441\u0442\u0438"`,
		`a "b" & c`:        `"a \u0022b\u0022 \u0026 c"`,
		`<Rich x='1'>+</>`: `"\u003CRich x=\u00271\u0027\u003E\u002B\u003C/\u003E"`,
		`¯\_(ツ)_/¯`:        `"\u00AF\\_(\u30C4)_/\u00AF"`,
		`line\nnext`:       `"line\\nnext"`,
		"real\nlf\ttab":    `"real\nlf\ttab"`,
		"emoji 😀":          `"emoji \uD83D\uDE00"`,
		"ctl\x01`":         `"ctl\u0001\u0060"`,
		"":                 `""`,
	}
	for in, want := range cases {
		if got := Quote(in); got != want {
			t.Errorf("Quote(%q) = %s, want %s", in, got, want)
		}
	}
}

// fixture mimics a WolvenKit export: CRLF, no trailing newline, an on-screen
// entry with an empty secondaryKey and a subtitle-style male variant.
var fixture = strings.ReplaceAll(`{
  "Header": {
    "ArchiveFileName": "E:\\x\\onscreens.json"
  },
  "Data": {
    "RootChunk": {
      "root": {
        "Data": {
          "$type": "localizationPersistenceOnScreenEntries",
          "entries": [
            {
              "$type": "localizationPersistenceOnScreenEntry",
              "femaleVariant": "\u041D\u043E\u0432\u043E\u0441\u0442\u0438",
              "maleVariant": "",
              "primaryKey": "40",
              "secondaryKey": ""
            },
            {
              "$type": "localizationPersistenceOnScreenEntry",
              "femaleVariant": "\u0413\u043E\u0442\u043E\u0432\u0430 \u003CRich\u003E",
              "maleVariant": "\u0413\u043E\u0442\u043E\u0432",
              "primaryKey": "41",
              "secondaryKey": "UI-Ready"
            }
          ]
        }
      }
    },
    "EmbeddedFiles": []
  }
}`, "\n", "\r\n")

func TestParse(t *testing.T) {
	f, err := Parse([]byte(fixture))
	if err != nil {
		t.Fatal(err)
	}
	if f.Type != "localizationPersistenceOnScreenEntries" || len(f.Entries) != 2 {
		t.Fatalf("got %+v", f)
	}
	e := f.Entries[1]
	if e.ID != "41" || e.SecondaryKey != "UI-Ready" || e.Female != "Готова <Rich>" || e.Male != "Готов" {
		t.Fatalf("entry = %+v", e)
	}
}

func TestSpliceIdentity(t *testing.T) {
	b := []byte(fixture)
	f, _ := Parse(b)

	out, st, err := Splice(b, f, nil)
	if err != nil || !bytes.Equal(out, b) {
		t.Fatalf("no-op splice changed bytes (err %v)", err)
	}
	if st.Kept != 3 || st.Replaced != 0 {
		t.Errorf("stats = %+v", st)
	}

	// Re-encoding every value with itself must also be byte-identical.
	self := map[string]string{}
	for _, e := range f.Entries {
		self[e.Key(false)], self[e.Key(true)] = e.Female, e.Male
	}
	out, _, err = Splice(b, f, self)
	if err != nil || !bytes.Equal(out, b) {
		t.Fatalf("self splice changed bytes (err %v):\n%s", err, out)
	}
}

func TestSpliceReplaces(t *testing.T) {
	b := []byte(fixture)
	f, _ := Parse(b)
	out, st, err := Splice(b, f, map[string]string{"40": "Навіны", "41@male": `Гатовы "так"`})
	if err != nil {
		t.Fatal(err)
	}
	if st.Replaced != 2 || st.Kept != 1 {
		t.Errorf("stats = %+v", st)
	}
	want := strings.Replace(fixture,
		`"femaleVariant": "\u041D\u043E\u0432\u043E\u0441\u0442\u0438",`,
		`"femaleVariant": "\u041D\u0430\u0432\u0456\u043D\u044B",`, 1)
	want = strings.Replace(want,
		`"maleVariant": "\u0413\u043E\u0442\u043E\u0432",`,
		`"maleVariant": "\u0413\u0430\u0442\u043E\u0432\u044B \u0022\u0442\u0430\u043A\u0022",`, 1)
	if string(out) != want {
		t.Fatalf("got:\n%s", out)
	}
	g, err := Parse(out)
	if err != nil || g.Entries[0].Female != "Навіны" || g.Entries[1].Male != `Гатовы "так"` {
		t.Fatalf("reparse: %v %+v", err, g)
	}
}

func TestSpliceBlanking(t *testing.T) {
	b := []byte(fixture)
	f, _ := Parse(b)
	// 41@male has text and is emptied; 40@male is already empty and counts for
	// nothing; the female variants are replaced as usual.
	out, st, err := SpliceBlanking(b, f,
		map[string]string{"40": "Навіны"},
		map[string]bool{"41@male": true, "40@male": true})
	if err != nil {
		t.Fatal(err)
	}
	if st.Replaced != 1 || st.Blanked != 1 || st.Kept != 1 {
		t.Errorf("stats = %+v", st)
	}
	g, err := Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if g.Entries[0].Female != "Навіны" || g.Entries[0].Male != "" || g.Entries[1].Male != "" || g.Entries[1].Female != "Готова <Rich>" {
		t.Fatalf("reparse: %+v", g.Entries)
	}
	want := strings.Replace(fixture,
		`"maleVariant": `+Quote("Готов")+`,`, `"maleVariant": "",`, 1)
	want = strings.Replace(want,
		`"femaleVariant": `+Quote("Новости")+`,`,
		`"femaleVariant": `+Quote("Навіны")+`,`, 1)
	if string(out) != want {
		t.Fatalf("got:\n%s", out)
	}
	// A nil blank set behaves like Splice.
	same, _, err := SpliceBlanking(b, f, nil, nil)
	if err != nil || !bytes.Equal(same, b) {
		t.Fatalf("nil blank set changed bytes (err %v)", err)
	}
}

func TestSpliceMismatch(t *testing.T) {
	b := []byte(fixture)
	f, _ := Parse(b)
	f.Entries[0].Female = "something else"
	if _, _, err := Splice(b, f, nil); err == nil {
		t.Fatal("expected literal mismatch error")
	}

	f, _ = Parse(b)
	f.Entries = f.Entries[:1]
	if _, _, err := Splice(b, f, nil); err == nil {
		t.Fatal("expected line-count error")
	}
}

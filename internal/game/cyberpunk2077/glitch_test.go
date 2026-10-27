package cyberpunk2077

import (
	"reflect"
	"testing"
)

func TestIsGlitchTextStrayControl(t *testing.T) {
	cases := map[string]bool{
		// A single stray C1 character inside real Russian text (a typo in the
		// Russian localization, absent from every other language) is not glitch.
		"«Где Джонни? Что же случилось в Арасака-тауэр\u008d?»": false,
		// Glitch effects mixed into readable Russian text: the text is still
		// translatable, only the effect is kept as it is.
		"eÌ¶Í\u0083Í\u0087нет_данных":  false,
		"данные\u0083недоступны\u0084": false,
		// No Cyrillic letters: mojibake of the glitch effect, one character is enough.
		"CÌ\u0091o": true,
		"Новости":   false,
		"":          false,
		// Nothing readable left once the effect is taken out.
		"Ì\u0083Í\u0087": true,
	}
	for in, want := range cases {
		if got := IsGlitchText(in); got != want {
			t.Errorf("IsGlitchText(%q) = %v, want %v", in, got, want)
		}
	}
}

// A glitch run inside translatable text is one code, so a translator cannot
// damage it and it reaches the game unchanged.
func TestPiecesGlitchRun(t *testing.T) {
	tail := "fÌ¸Ì¾Ì\u0083iÌ¶Ì\u008e"
	head := "eÌ¶Í\u0083Í\u0087"
	cases := []struct {
		in   string
		want []Piece
	}{
		{"По делу #87625 данные?&%4*£" + tail, []Piece{{Text: "По делу #87625 данные?&%4*"}, {Text: "£" + tail, Code: true}}},
		{head + "нет_данных", []Piece{{Text: head, Code: true}, {Text: "нет_данных"}}},
		{"данные\u0083\u0084недоступны", []Piece{{Text: "данные"}, {Text: "\u0083\u0084", Code: true}, {Text: "недоступны"}}},
		// A single stray control character is a typo, not an effect: plain text.
		{"тауэр\u008d?»", []Piece{{Text: "тауэр\u008d?»"}}},
		// «» are Latin-1 characters but no part of an effect.
		{"«Где Джонни?»", []Piece{{Text: "«Где Джонни?»"}}},
		// Nothing readable: one code, as before.
		{"CÌ\u0091o", []Piece{{Text: "CÌ\u0091o", Code: true}}},
	}
	for _, c := range cases {
		if got := Pieces(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Pieces(%q)\n got %+v\nwant %+v", c.in, got, c.want)
		}
	}
}

// The machine translator sees the glitch run as a sentinel and gets it back.
func TestMaskGlitchRun(t *testing.T) {
	in := "Данные недоступныÌ¸Ì\u0083"
	a := analyzer{}
	masked, markers := a.Mask(a.Analyze(in))
	if masked != "Данные недоступны§0§" || len(markers) != 1 || markers[0] != "Ì¸Ì\u0083" {
		t.Errorf("Mask = %q %q", masked, markers)
	}
}

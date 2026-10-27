package cyberpunk2077

import (
	"reflect"
	"testing"
)

// A speech tag whose foreign-language original holds a quoted word (\" in the
// string) is still one tag: the translatable t= text is not swallowed by it.
func TestPiecesSpeechTagEscapedQuote(t *testing.T) {
	in := `<kiroshi l="fra" o="Je dis \"Blind_n_Dead\" ok." t="Он называется «Мёртвый слепец»." b="" a=""/>`
	want := []Piece{
		{Text: `<kiroshi l="fra" o="Je dis \"Blind_n_Dead\" ok." t="`, Code: true},
		{Text: "Он называется «Мёртвый слепец»."},
		{Text: `" b="" a=""/>`, Code: true},
	}
	if got := Pieces(in); !reflect.DeepEqual(got, want) {
		t.Errorf("Pieces\n got %+v\nwant %+v", got, want)
	}
	if !HasCyrillicText(Pieces(in)) {
		t.Error("HasCyrillicText = false, want true")
	}

	// An escaped quote inside the translated text itself.
	in = `<kiroshi l="kor" o="x" t="Все зовут меня \"Гепард\"." b="" a=""/>`
	if got := Pieces(in); len(got) != 3 || got[1].Text != `Все зовут меня \"Гепард\".` {
		t.Errorf("Pieces = %+v, want the t text in the middle", got)
	}
}

// The oxygen-bar label is a JSON clipboard dump: the wrapper is locked, only
// the "value" text is translatable.
func TestPiecesClipboardJSON(t *testing.T) {
	in := "{\n  \"RootType\": \"toolsprvSingleElementClipboardHolder\",\n \"Data\": {\n  \"value\": \"КИСЛОРОД\"\n }\n}"
	want := []Piece{
		{Text: "{\n  \"RootType\": \"toolsprvSingleElementClipboardHolder\",\n \"Data\": {\n  \"value\": \"", Code: true},
		{Text: "КИСЛОРОД"},
		{Text: "\"\n }\n}", Code: true},
	}
	if got := Pieces(in); !reflect.DeepEqual(got, want) {
		t.Errorf("Pieces\n got %+v\nwant %+v", got, want)
	}
}

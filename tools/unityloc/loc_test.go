package main

import (
	"reflect"
	"testing"

	"github.com/Thrapis/go-game-translator/internal/xliff"
)

func TestParseLocKeepsOrderAndSkipsMeta(t *testing.T) {
	src := "\xef\xbb\xbf" + `{"$type":"System.Collections.Generic.Dictionary","English":{"ID":"English","ISO":"EN","string":"string","B":"two","A":"one"},"Russian":{"ID":"Russian","B":"два","A":"один"}}`
	lf, err := parseLoc([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(lf.langs, []string{"English", "Russian"}) {
		t.Fatalf("langs = %v", lf.langs)
	}
	en := lf.tables["English"]
	if !reflect.DeepEqual(en.keys, []string{"B", "A"}) || en.m["A"] != "one" {
		t.Fatalf("English = %+v", en)
	}
}

func TestPiecesLocksFormatArgsAndTags(t *testing.T) {
	got := pieces("Save {0} <b>now</b>\nok")
	want := []xliff.Piece{
		{Text: "Save "}, {Text: "{0}", Code: true}, {Text: " "},
		{Text: "<b>", Code: true}, {Text: "now"}, {Text: "</b>", Code: true}, {Text: "\nok"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
	if got := pieces("плоский текст"); len(got) != 1 || got[0].Code {
		t.Fatalf("plain text = %+v", got)
	}
}

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/Thrapis/go-game-translator/internal/xliff"
)

// fakeMT "translates" by upper-casing and prefixing, so tests can tell what
// was sent: a leading "x" lower-cases the result to exercise case repair.
type fakeMT struct{ sent []string }

func (f *fakeMT) Translate(_ context.Context, text, _, _ string) (string, error) {
	f.sent = append(f.sent, text)
	return "be:" + text, nil
}

func unit(id, src string, tgt string) xliff.Unit {
	u := xliff.Unit{ID: id, Note: "n-" + id, Source: []xliff.Piece{{Text: src}}}
	if tgt != "" {
		u.Target, u.State = []xliff.Piece{{Text: tgt}}, "translated"
	}
	return u
}

func TestUpdateUnitsKeepsValidTranslationsAndTranslatesTheRest(t *testing.T) {
	prev := &xliff.File{Original: "f", SourceLang: "ru", TargetLang: "be", Units: []xliff.Unit{
		unit("same", "Привет", "Прывітанне"),
		unit("punct", "Кстати мастер", "Дарэчы, майстар"),
		unit("changed", "Старый текст", "Стары тэкст"),
		unit("gone", "Удалено", "Выдалена"),
		unit("untranslated", "Без перевода", ""),
	}}
	cur := &xliff.File{Original: "f", SourceLang: "ru", TargetLang: "be", Units: []xliff.Unit{
		unit("new", "Новый", ""),
		unit("same", "Привет", ""),
		unit("punct", "Кстати, мастер", ""),
		unit("changed", "Новый текст", ""),
		unit("untranslated", "Без перевода", ""),
	}}
	mt := &fakeMT{}
	got, st, err := updateUnits(context.Background(), mt, prev, cur, "needs-review-translation", 2)
	if err != nil {
		t.Fatal(err)
	}
	if st.Carried != 2 || st.Retranslate != 3 || st.Dropped != 1 {
		t.Fatalf("stats = %+v", st)
	}
	var ids []string
	for _, u := range got.Units {
		ids = append(ids, u.ID)
	}
	if strings.Join(ids, ",") != "new,same,punct,changed,untranslated" {
		t.Fatalf("order = %v", ids)
	}
	want := map[string][2]string{
		"same":         {"Прывітанне", "translated"},         // kept, state kept
		"punct":        {"Дарэчы, майстар", "translated"},     // punctuation-only change keeps the translation
		"new":          {"Be:Новый", "needs-review-translation"}, // machine, first letter kept upper
		"changed":      {"Be:Новый текст", "needs-review-translation"},
		"untranslated": {"Be:Без перевода", "needs-review-translation"},
	}
	for _, u := range got.Units {
		w := want[u.ID]
		if xliff.Plain(u.Target) != w[0] || u.State != w[1] {
			t.Errorf("%s: target %q state %q, want %q %q", u.ID, xliff.Plain(u.Target), u.State, w[0], w[1])
		}
		if u.Note != "n-"+u.ID {
			t.Errorf("%s: note %q not taken from the new file", u.ID, u.Note)
		}
	}
	if len(mt.sent) != 3 {
		t.Errorf("sent %d lines, want 3: %v", len(mt.sent), mt.sent)
	}
}

func TestTranslatePiecesKeepsCodesAndLineBreaks(t *testing.T) {
	src := []xliff.Piece{{Text: "Файл "}, {Text: "{0}", Code: true}, {Text: " поврежден\n\n  \nВторая строка "}}
	mt := &fakeMT{}
	got, err := translatePieces(context.Background(), mt, [][]xliff.Piece{src}, "ru", "be", 10)
	if err != nil {
		t.Fatal(err)
	}
	if plain := xliff.Plain(got[0]); plain != "Be:Файл {0} поврежден\n\n  \nBe:Вторая строка " {
		t.Fatalf("got %q", plain)
	}
	if len(got[0]) != 3 || !got[0][1].Code || got[0][1].Text != "{0}" {
		t.Fatalf("code piece lost: %+v", got[0])
	}
	// The blank lines are not sent to the translator, the line with the code is sent masked.
	if len(mt.sent) != 2 || mt.sent[0] != "Файл §0§ поврежден" {
		t.Fatalf("sent %q", mt.sent)
	}
}

func TestTranslatePiecesFallsBackWhenSentinelsAreDamaged(t *testing.T) {
	src := []xliff.Piece{{Text: "Файл "}, {Text: "{0}", Code: true}, {Text: " поврежден"}}
	got, err := translatePieces(context.Background(), dropSentinel{}, [][]xliff.Piece{src}, "ru", "be", 10)
	if err != nil {
		t.Fatal(err)
	}
	if plain := xliff.Plain(got[0]); plain != "Be:Файл {0} be: поврежден" {
		t.Fatalf("got %q", plain)
	}
	if !got[0][1].Code {
		t.Errorf("code piece lost: %+v", got[0])
	}
}

// dropSentinel mangles every sentinel, forcing the per-piece fallback.
type dropSentinel struct{}

func (dropSentinel) Translate(_ context.Context, text, _, _ string) (string, error) {
	return "be:" + strings.ReplaceAll(text, "§", ""), nil
}

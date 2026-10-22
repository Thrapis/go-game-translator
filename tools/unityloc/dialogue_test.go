package main

import (
	"reflect"
	"testing"

	"github.com/Thrapis/go-game-translator/internal/xliff"
)

func field(title, value string) any { return map[string]any{"title": title, "value": value} }

func entry(articy, actor, base, ru, en string) any {
	return map[string]any{"fields": []any{
		field("Title", "t"), field("Actor", actor), field(fieldBase, base),
		field("ru", ru), field("en", en), field(fieldID, articy),
	}}
}

func testDB() map[string]any {
	return map[string]any{
		"actors": []any{
			map[string]any{"id": int64(9), "fields": []any{field("Name", "Old Hashimoto")}},
			map[string]any{"id": int64(2), "fields": []any{field("Name", "Demo Player")}},
		},
		"conversations": []any{
			map[string]any{"dialogueEntries": []any{
				entry("0xA1", "9", "старый", "Привет! [b]Да[/b]", "Hi! [b]Yes[/b]"),
				entry("0xA2", "2", "старый2", "Ок", "Ok"),
				entry("0xA3", "2", "", "", ""), // a group node without text
			}},
		},
	}
}

func TestDialogueLines(t *testing.T) {
	got, err := dialogueLines(testDB(), "ru", "en")
	if err != nil {
		t.Fatal(err)
	}
	want := []dialogueLine{
		{ID: "0xA1", Speaker: "Old Hashimoto", Text: map[string]string{"ru": "Привет! [b]Да[/b]", "en": "Hi! [b]Yes[/b]"}},
		{ID: "0xA2", Speaker: "Demo Player", Text: map[string]string{"ru": "Ок", "en": "Ok"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}

	db := testDB()
	dialogueEntries(db)[1]["fields"].([]any)[5].(map[string]any)["value"] = "0xA1"
	if _, err := dialogueLines(db, "ru"); err == nil {
		t.Error("duplicate articy ids must be an error")
	}
}

func TestApplyDialogueChangesOnlyTargetFields(t *testing.T) {
	db := testDB()
	n, missing := applyDialogue(db, map[string]string{"0xA1": "Прывітанне! [b]Так[/b]", "0xZZ": "x"}, "ru", fieldBase)
	if n != 1 || !reflect.DeepEqual(missing, []string{"0xZZ"}) {
		t.Fatalf("n=%d missing=%v", n, missing)
	}
	e := dialogueEntries(db)
	for title, want := range map[string]string{"ru": "Прывітанне! [b]Так[/b]", fieldBase: "Прывітанне! [b]Так[/b]", "en": "Hi! [b]Yes[/b]", fieldID: "0xA1"} {
		if got, _ := fieldValue(e[0], title); got != want {
			t.Errorf("%s = %q, want %q", title, got, want)
		}
	}
	if got, _ := fieldValue(e[1], "ru"); got != "Ок" {
		t.Errorf("untouched entry changed: %q", got)
	}
}

func TestDialoguePiecesLockTags(t *testing.T) {
	got := dialoguePieces("а [b]б[/b]\nв")
	want := []xliff.Piece{{Text: "а "}, {Text: "[b]", Code: true}, {Text: "б"}, {Text: "[/b]", Code: true}, {Text: "\nв"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
}

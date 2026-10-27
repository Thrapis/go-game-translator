package main

import (
	"reflect"
	"testing"

	"github.com/Thrapis/go-game-translator/internal/wolvenkit"
)

func TestPlaceholderGaps(t *testing.T) {
	src := &wolvenkit.File{Entries: []wolvenkit.Entry{
		entry("1", "[en_us]Hello.", "Привет."),          // female is a placeholder, male is real
		entry("2", "[en_us]Both.", "[en_us]Both m."),    // both variants are placeholders
		entry("3", "[en_us]Czech is empty", ""),         // other side has nothing
		entry("4", "[en_us]Czech is a placeholder", ""), // other side is a placeholder too
		entry("5", "Обычный текст", ""),                 // not a placeholder
		entry("6", "[en_us]No such id", ""),             // id missing on the other side
		entry("7", "[en_us]Other is glitch", ""),
	}}
	other := &wolvenkit.File{Entries: []wolvenkit.Entry{
		entry("1", "Ahoj.", ""),
		entry("2", "Oba.", "Oba m."),
		entry("3", "", ""),
		entry("4", "[en_us]still a placeholder", ""),
		entry("5", "Normální text", ""),
		entry("7", "Ã\u0082\u0083zalgo", ""),
	}}
	got := placeholderGaps(src, other)
	want := map[string]string{
		"1":      "Hello.",
		"2":      "Both.",
		"2@male": "Both m.",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("placeholderGaps = %v\nwant %v", got, want)
	}
}

func TestExportRowsWithExtras(t *testing.T) {
	f := &wolvenkit.File{Entries: []wolvenkit.Entry{
		entry("1", "[en_us]Hello.", "Привет."),
		entry("2", "[en_us]skipped", ""),
		entry("3", "[en_us][db_db]use", ""), // the text keeps a second tag that looks like a placeholder
	}}
	var sk skipped
	rows, _ := exportRowsWith(f, &sk, map[string]string{"1": "Hello.", "3": "[db_db]use"})
	var gotIDs []string
	for _, r := range rows {
		gotIDs = append(gotIDs, r.ID)
	}
	if want := []string{"1", "1@male", "3"}; !reflect.DeepEqual(gotIDs, want) {
		t.Fatalf("ids = %v, want %v", gotIDs, want)
	}
	if rows[0].Source != "Hello." {
		t.Errorf("extra row source = %q, want the placeholder without its language tag", rows[0].Source)
	}
	if sk.placeholders != 1 {
		t.Errorf("placeholders skipped = %d, want 1", sk.placeholders)
	}
}

// Readable text with a glitch effect around it is exported (the effect becomes
// a locked tag); a string that is nothing but effect is not.
func TestExportRowsMixedGlitch(t *testing.T) {
	mixed := "Данные недоступны?&%4*£fÌ¸Ì\u0083"
	f := &wolvenkit.File{Entries: []wolvenkit.Entry{
		entry("1", mixed, ""),
		entry("2", "CÌ\u0091o", ""),
	}}
	var sk skipped
	rows, _ := exportRows(f, &sk)
	if len(rows) != 1 || rows[0].ID != "1" || rows[0].Source != mixed {
		t.Errorf("rows = %+v, want only id 1 with the whole text", rows)
	}
	if sk.glitch != 1 {
		t.Errorf("glitch skipped = %d, want 1", sk.glitch)
	}
}

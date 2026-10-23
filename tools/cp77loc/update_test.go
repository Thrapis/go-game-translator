package main

import (
	"reflect"
	"testing"

	"github.com/Thrapis/go-game-translator/internal/xliff"
)

func txt(s string) []xliff.Piece { return []xliff.Piece{{Text: s}} }

func TestCarry(t *testing.T) {
	src := []xliff.Unit{
		{ID: "same", Source: txt("Файлы")},
		{ID: "samePending", Source: txt("Новости")},
		{ID: "changed", Source: txt("Новый текст")},
		{ID: "added", Source: txt("Совсем новое")},
	}
	prev := []xliff.Unit{
		{ID: "same", Source: txt("Файлы"), Target: txt("Файлы_be"), State: "translated"},
		{ID: "samePending", Source: txt("Новости")}, // never translated
		{ID: "changed", Source: txt("Старый текст"), Target: txt("Стары тэкст"), State: "translated"},
		{ID: "gone", Source: txt("Удалено"), Target: txt("Выдалена"), State: "translated"},
	}

	t.Run("drop changed", func(t *testing.T) {
		out, need, st := carry(src, prev, false)
		if got := out[0]; xliff.Plain(got.Target) != "Файлы_be" || got.State != "translated" {
			t.Errorf("unchanged unit lost its translation or state: %+v", got)
		}
		if out[2].Target != nil {
			t.Errorf("changed unit kept a stale target: %+v", out[2])
		}
		if want := []string{"samePending", "changed", "added"}; !reflect.DeepEqual(need, want) {
			t.Errorf("need = %v, want %v", need, want)
		}
		if want := (carryStats{Same: 1, SamePending: 1, Changed: 1, Added: 1, Removed: 1}); st != want {
			t.Errorf("stats = %+v, want %+v", st, want)
		}
	})

	t.Run("keep changed", func(t *testing.T) {
		out, need, _ := carry(src, prev, true)
		if got := out[2]; xliff.Plain(got.Target) != "Стары тэкст" || got.State != "needs-adaptation" {
			t.Errorf("changed unit = %+v, want old target flagged needs-adaptation", got)
		}
		if want := []string{"samePending", "added"}; !reflect.DeepEqual(need, want) {
			t.Errorf("need = %v, want %v", need, want)
		}
	})

	t.Run("source side is never taken from prev", func(t *testing.T) {
		out, _, _ := carry(src, prev, true)
		for i, u := range out {
			if !reflect.DeepEqual(u.Source, src[i].Source) {
				t.Errorf("%s: source = %v, want %v", u.ID, u.Source, src[i].Source)
			}
		}
	})
}

func TestCarryDifferentCodes(t *testing.T) {
	// Same visible text can still be a different string if the codes differ;
	// Plain keeps the markup text, so this is "changed", not "same".
	src := []xliff.Unit{{ID: "1", Source: []xliff.Piece{{Text: "a "}, {Text: "{X}", Code: true}}}}
	prev := []xliff.Unit{{ID: "1", Source: []xliff.Piece{{Text: "a "}, {Text: "{Y}", Code: true}}, Target: txt("b"), State: "translated"}}
	out, need, st := carry(src, prev, false)
	if out[0].Target != nil || len(need) != 1 || st.Changed != 1 {
		t.Errorf("out %+v need %v stats %+v", out[0], need, st)
	}
}

func TestApplyFill(t *testing.T) {
	units := []xliff.Unit{
		{ID: "a", Source: txt("Раз")},
		{ID: "b", Source: txt("Два"), Target: txt("Два_be"), State: "translated"},
		{ID: "c", Source: txt("Три")},
	}
	got := applyFill(units, map[string]string{"a": "Адзін", "b": "ignored"})
	if got != 1 {
		t.Errorf("filled %d, want 1", got)
	}
	if xliff.Plain(units[0].Target) != "Адзін" || units[0].State != "needs-review-translation" {
		t.Errorf("a = %+v", units[0])
	}
	if xliff.Plain(units[1].Target) != "Два_be" || units[1].State != "translated" {
		t.Errorf("an existing target was overwritten: %+v", units[1])
	}
	if units[2].Target != nil {
		t.Errorf("c got a target without a translation: %+v", units[2])
	}
}

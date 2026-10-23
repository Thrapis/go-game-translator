package main

import (
	"reflect"
	"testing"

	"github.com/Thrapis/go-game-translator/internal/wolvenkit"
)

func entry(id, female, male string) wolvenkit.Entry {
	return wolvenkit.Entry{ID: id, Female: female, Male: male, HasFemale: true, HasMale: true}
}

func ids(es []wolvenkit.Entry) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.ID)
	}
	return out
}

func TestPortEntries(t *testing.T) {
	tmpl := &wolvenkit.File{Entries: []wolvenkit.Entry{
		entry("1", "cz f", "cz m"),      // gendered in both
		entry("2", "cz f", "cz m"),      // gendered in the template only
		entry("3", "cz only", ""),       // not in the source
		entry("4", "cz f", ""),          // empty male variant in the template, gendered source: the male text fills it
		entry("5", "[en_us] voice", ""), // VO placeholder on the template side, real text in the source: ported
		entry("6", "cz f", ""),          // source is empty
	}}
	from := &wolvenkit.File{Entries: []wolvenkit.Entry{
		entry("1", "be f", "be m"),
		entry("2", "be one", ""),
		entry("4", "be f", "be m"),
		entry("5", "be text", ""),
		entry("6", "", ""),
		entry("7", "be extra", ""), // not in the template
	}}

	r := portEntries(tmpl, from)

	want := map[string]string{
		"1": "be f", "1@male": "be m",
		"2": "be one", // one text: the Czech male variant is removed, female serves both
		"4": "be f", "4@male": "be m",
		"5": "be text",
	}
	if !reflect.DeepEqual(r.Tr, want) {
		t.Errorf("Tr = %v\nwant %v", r.Tr, want)
	}
	if want := map[string]bool{"2@male": true}; !reflect.DeepEqual(r.Blank, want) {
		t.Errorf("Blank = %v, want %v", r.Blank, want)
	}
	if got := ids(r.OnlyTemplate); !reflect.DeepEqual(got, []string{"3"}) {
		t.Errorf("OnlyTemplate = %v, want [3]", got)
	}
	if got := ids(r.OnlyFrom); !reflect.DeepEqual(got, []string{"7"}) {
		t.Errorf("OnlyFrom = %v, want [7]", got)
	}
	// Czech text that the source cannot replace is a gap; a template
	// placeholder (5) and an id missing from the source (3) are not.
	if want := []portGap{{Entry: tmpl.Entries[5], Why: "source empty"}}; !reflect.DeepEqual(r.Gaps, want) {
		t.Errorf("Gaps = %+v, want %+v", r.Gaps, want)
	}
}

func TestPortEntriesSkipsGlitchAndPlaceholders(t *testing.T) {
	glitch := "Ã\u0082\u0083zalgo" // contains U+0083, the mark of corrupted glitch text
	tmpl := &wolvenkit.File{Entries: []wolvenkit.Entry{
		entry("1", glitch, ""),      // template text is glitch: keep it
		entry("2", "cz f", ""),      // source text is a VO placeholder: keep Czech
		entry("3", "[en_us] a", ""), // placeholder on both sides: nothing to port, nothing missing
	}}
	from := &wolvenkit.File{Entries: []wolvenkit.Entry{
		entry("1", "be", ""),
		entry("2", "[en_us] voice", ""),
		entry("3", "[en_us] a", ""),
	}}
	r := portEntries(tmpl, from)
	if len(r.Tr) != 0 {
		t.Errorf("Tr = %v, want nothing ported", r.Tr)
	}
	if want := []portGap{{Entry: tmpl.Entries[1], Why: "source VO placeholder"}}; !reflect.DeepEqual(r.Gaps, want) {
		t.Errorf("Gaps = %+v, want %+v", r.Gaps, want)
	}
}

// A male variant only counts as a gap when neither the source's male nor its
// female text can serve it.
func TestPortEntriesGapMaleVariant(t *testing.T) {
	tmpl := &wolvenkit.File{Entries: []wolvenkit.Entry{entry("1", "cz f", "cz m")}}
	from := &wolvenkit.File{Entries: []wolvenkit.Entry{entry("1", "", "")}}
	r := portEntries(tmpl, from)
	want := []portGap{{Entry: tmpl.Entries[0], Why: "source empty"}, {Entry: tmpl.Entries[0], Male: true, Why: "source empty"}}
	if !reflect.DeepEqual(r.Gaps, want) {
		t.Errorf("Gaps = %+v, want %+v", r.Gaps, want)
	}

	// Female text only: the Czech male variant is removed, not copied.
	from = &wolvenkit.File{Entries: []wolvenkit.Entry{entry("1", "be", "")}}
	r = portEntries(tmpl, from)
	if len(r.Gaps) != 0 || !reflect.DeepEqual(r.Tr, map[string]string{"1": "be"}) || !r.Blank["1@male"] {
		t.Errorf("want female ported and male removed: %+v", r)
	}

	// A male text identical to the female one is not a distinct male text.
	from = &wolvenkit.File{Entries: []wolvenkit.Entry{entry("1", "be", "be")}}
	r = portEntries(tmpl, from)
	if !reflect.DeepEqual(r.Tr, map[string]string{"1": "be"}) || !r.Blank["1@male"] {
		t.Errorf("identical male text: %+v", r)
	}

	// The source has nothing usable for the female variant: Czech stays whole.
	from = &wolvenkit.File{Entries: []wolvenkit.Entry{entry("1", "[en_us] x", "")}}
	r = portEntries(tmpl, from)
	if len(r.Tr) != 0 || len(r.Blank) != 0 || len(r.Gaps) != 2 {
		t.Errorf("unusable source: %+v", r)
	}
}

// Without a maleVariant property in the template there is nowhere to put the
// male text; Splice cannot add one.
func TestPortEntriesNoMaleProperty(t *testing.T) {
	tmpl := &wolvenkit.File{Entries: []wolvenkit.Entry{{ID: "1", Female: "cz", HasFemale: true}}}
	from := &wolvenkit.File{Entries: []wolvenkit.Entry{entry("1", "be f", "be m")}}
	if r := portEntries(tmpl, from); !reflect.DeepEqual(r.Tr, map[string]string{"1": "be f"}) {
		t.Errorf("Tr = %v, want only the female text", r.Tr)
	}
}

// A source male text identical to the female one adds nothing to an empty
// male variant.
func TestPortEntriesSameMaleText(t *testing.T) {
	tmpl := &wolvenkit.File{Entries: []wolvenkit.Entry{entry("1", "cz", "")}}
	from := &wolvenkit.File{Entries: []wolvenkit.Entry{entry("1", "be", "be")}}
	if r := portEntries(tmpl, from); !reflect.DeepEqual(r.Tr, map[string]string{"1": "be"}) {
		t.Errorf("Tr = %v, want only the female text", r.Tr)
	}
}

// A source that has text only in its male variant (the female one is empty or
// a placeholder) still has to fill the template's female variant, which is the
// universal one, or the female text of the template would stay. The male text
// then adds nothing distinct and the template's male variant is emptied.
func TestPortEntriesFemaleFromMale(t *testing.T) {
	tmpl := &wolvenkit.File{Entries: []wolvenkit.Entry{entry("1", "cz f", "cz m")}}
	for _, female := range []string{"", "[en_us] placeholder"} {
		from := &wolvenkit.File{Entries: []wolvenkit.Entry{entry("1", female, "be m")}}
		r := portEntries(tmpl, from)
		if !reflect.DeepEqual(r.Tr, map[string]string{"1": "be m"}) || !r.Blank["1@male"] || len(r.Gaps) != 0 {
			t.Errorf("female %q: %+v", female, r)
		}
	}

	// An empty template male variant stays empty.
	tmpl = &wolvenkit.File{Entries: []wolvenkit.Entry{entry("1", "cz f", "")}}
	from := &wolvenkit.File{Entries: []wolvenkit.Entry{entry("1", "", "be m")}}
	if r := portEntries(tmpl, from); !reflect.DeepEqual(r.Tr, map[string]string{"1": "be m"}) || len(r.Blank) != 0 {
		t.Errorf("empty template male: %+v", r)
	}

	// A template female placeholder is replaced as well.
	tmpl = &wolvenkit.File{Entries: []wolvenkit.Entry{entry("1", "[en_us] voice", "")}}
	if r := portEntries(tmpl, from); r.Tr["1"] != "be m" {
		t.Errorf("template placeholder: %+v", r)
	}

	// A male-only line (both the template and the source have an empty female
	// variant) stays male-only: the male text goes to the male variant.
	tmpl = &wolvenkit.File{Entries: []wolvenkit.Entry{entry("1", "", "cz m")}}
	from = &wolvenkit.File{Entries: []wolvenkit.Entry{entry("1", "", "be m")}}
	if r := portEntries(tmpl, from); !reflect.DeepEqual(r.Tr, map[string]string{"1@male": "be m"}) || len(r.Blank) != 0 {
		t.Errorf("male-only line: %+v", r)
	}

	// No male text either: still a gap, Czech stays.
	tmpl = &wolvenkit.File{Entries: []wolvenkit.Entry{entry("1", "cz f", "cz m")}}
	from = &wolvenkit.File{Entries: []wolvenkit.Entry{entry("1", "", "")}}
	if r := portEntries(tmpl, from); len(r.Tr) != 0 || len(r.Gaps) != 2 {
		t.Errorf("nothing to port: %+v", r)
	}
}

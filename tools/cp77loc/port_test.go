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
	// Every slot mirrors the source: no male text there empties the male
	// variant, no text at all empties the female one (6).
	if want := map[string]bool{"2@male": true, "6": true}; !reflect.DeepEqual(r.Blank, want) {
		t.Errorf("Blank = %v, want %v", r.Blank, want)
	}
	if got := ids(r.OnlyTemplate); !reflect.DeepEqual(got, []string{"3"}) {
		t.Errorf("OnlyTemplate = %v, want [3]", got)
	}
	if got := ids(r.OnlyFrom); !reflect.DeepEqual(got, []string{"7"}) {
		t.Errorf("OnlyFrom = %v, want [7]", got)
	}
	// Only a slot emptied because the source has no usable text is reported; a
	// template placeholder (5) and an id missing from the source (3) are not.
	if want := []portGap{{Entry: tmpl.Entries[5], Why: "source empty"}}; !reflect.DeepEqual(r.Emptied, want) {
		t.Errorf("Emptied = %+v, want %+v", r.Emptied, want)
	}
}

func TestPortEntriesSkipsGlitchAndPlaceholders(t *testing.T) {
	glitch := "Ã\u0082\u0083zalgo" // contains U+0083, the mark of corrupted glitch text
	tmpl := &wolvenkit.File{Entries: []wolvenkit.Entry{
		entry("1", glitch, ""),      // template text is glitch: keep it
		entry("2", "cz f", ""),      // source text is a VO placeholder: nothing to mirror, emptied
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
	if want := map[string]bool{"2": true}; !reflect.DeepEqual(r.Blank, want) {
		t.Errorf("Blank = %v, want %v", r.Blank, want)
	}
	if want := []portGap{{Entry: tmpl.Entries[1], Why: "source VO placeholder"}}; !reflect.DeepEqual(r.Emptied, want) {
		t.Errorf("Emptied = %+v, want %+v", r.Emptied, want)
	}
}

// A slot the source cannot fill is emptied, whichever variant it is.
func TestPortEntriesEmptiesUnfilledSlots(t *testing.T) {
	tmpl := &wolvenkit.File{Entries: []wolvenkit.Entry{entry("1", "cz f", "cz m")}}
	from := &wolvenkit.File{Entries: []wolvenkit.Entry{entry("1", "", "")}}
	r := portEntries(tmpl, from)
	want := []portGap{{Entry: tmpl.Entries[0], Why: "source empty"}, {Entry: tmpl.Entries[0], Male: true, Why: "source empty"}}
	if !reflect.DeepEqual(r.Emptied, want) || !reflect.DeepEqual(r.Blank, map[string]bool{"1": true, "1@male": true}) || len(r.Tr) != 0 {
		t.Errorf("empty source: %+v", r)
	}

	// Female text only: the Czech male variant is removed, not copied.
	from = &wolvenkit.File{Entries: []wolvenkit.Entry{entry("1", "be", "")}}
	r = portEntries(tmpl, from)
	if len(r.Emptied) != 0 || !reflect.DeepEqual(r.Tr, map[string]string{"1": "be"}) || !reflect.DeepEqual(r.Blank, map[string]bool{"1@male": true}) {
		t.Errorf("female only: %+v", r)
	}

	// A male text identical to the female one is not a distinct male text.
	from = &wolvenkit.File{Entries: []wolvenkit.Entry{entry("1", "be", "be")}}
	r = portEntries(tmpl, from)
	if !reflect.DeepEqual(r.Tr, map[string]string{"1": "be"}) || !r.Blank["1@male"] {
		t.Errorf("identical male text: %+v", r)
	}

	// A VO placeholder is no text to mirror either: both slots are emptied.
	from = &wolvenkit.File{Entries: []wolvenkit.Entry{entry("1", "[en_us] x", "")}}
	r = portEntries(tmpl, from)
	if len(r.Tr) != 0 || len(r.Blank) != 2 || len(r.Emptied) != 2 || r.Emptied[1].Why != "source VO placeholder" {
		t.Errorf("placeholder source: %+v", r)
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

// The game takes the male text for male V and falls back to the female one, and
// some ids have a male text but no female one. Each slot therefore mirrors the
// same slot of the source: its text, or empty when the source has none. A male
// text is never copied into the female slot.
func TestPortEntriesMirrorsSlots(t *testing.T) {
	tmpl := &wolvenkit.File{Entries: []wolvenkit.Entry{entry("1", "cz f", "cz m")}}
	for _, female := range []string{"", "[en_us] placeholder"} {
		from := &wolvenkit.File{Entries: []wolvenkit.Entry{entry("1", female, "be m")}}
		r := portEntries(tmpl, from)
		why := unusableWhy(female)
		if !reflect.DeepEqual(r.Tr, map[string]string{"1@male": "be m"}) || !reflect.DeepEqual(r.Blank, map[string]bool{"1": true}) ||
			!reflect.DeepEqual(r.Emptied, []portGap{{Entry: tmpl.Entries[0], Why: why}}) {
			t.Errorf("female %q: %+v", female, r)
		}
	}

	// An empty template male variant is filled from the source's male text.
	tmpl = &wolvenkit.File{Entries: []wolvenkit.Entry{entry("1", "cz f", "")}}
	from := &wolvenkit.File{Entries: []wolvenkit.Entry{entry("1", "", "be m")}}
	if r := portEntries(tmpl, from); !reflect.DeepEqual(r.Tr, map[string]string{"1@male": "be m"}) || !r.Blank["1"] {
		t.Errorf("empty template male: %+v", r)
	}

	// A template placeholder is a stub, not text, but when the id has real text
	// in another slot of the source it is emptied like any other leftover.
	tmpl = &wolvenkit.File{Entries: []wolvenkit.Entry{entry("1", "[en_us][pl_pl]stub", "")}}
	if r := portEntries(tmpl, from); !reflect.DeepEqual(r.Tr, map[string]string{"1@male": "be m"}) || !reflect.DeepEqual(r.Blank, map[string]bool{"1": true}) {
		t.Errorf("template female stub next to a male text: %+v", r)
	}
	tmpl = &wolvenkit.File{Entries: []wolvenkit.Entry{entry("1", "cz f", "[en_us]Woah!")}}
	from = &wolvenkit.File{Entries: []wolvenkit.Entry{entry("1", "be f", "")}}
	if r := portEntries(tmpl, from); !reflect.DeepEqual(r.Tr, map[string]string{"1": "be f"}) || !reflect.DeepEqual(r.Blank, map[string]bool{"1@male": true}) {
		t.Errorf("template male stub next to a female text: %+v", r)
	}
	// A stub on both sides stays: there is no text to mirror and nothing to empty.
	tmpl = &wolvenkit.File{Entries: []wolvenkit.Entry{entry("1", "[en_us] voice", "")}}
	from = &wolvenkit.File{Entries: []wolvenkit.Entry{entry("1", "[en_us] voice", "")}}
	if r := portEntries(tmpl, from); len(r.Tr) != 0 || len(r.Blank) != 0 || len(r.Emptied) != 0 {
		t.Errorf("stub on both sides: %+v", r)
	}

	// A male-only line stays male-only.
	tmpl = &wolvenkit.File{Entries: []wolvenkit.Entry{entry("1", "", "cz m")}}
	from = &wolvenkit.File{Entries: []wolvenkit.Entry{entry("1", "", "be m")}}
	if r := portEntries(tmpl, from); !reflect.DeepEqual(r.Tr, map[string]string{"1@male": "be m"}) || len(r.Blank) != 0 || len(r.Emptied) != 0 {
		t.Errorf("male-only line: %+v", r)
	}

	// A Czech male-only line whose source has a female text gets that text.
	from = &wolvenkit.File{Entries: []wolvenkit.Entry{entry("1", "be f", "be m")}}
	if r := portEntries(tmpl, from); !reflect.DeepEqual(r.Tr, map[string]string{"1": "be f", "1@male": "be m"}) {
		t.Errorf("female added: %+v", r)
	}
}

// A template string that is glitch effect plus Czech text is replaced by a
// source string of the same kind (readable text around the effect); the effect
// itself comes with the source text. A plain source text does not replace a
// template glitch string.
func TestPortEntriesMixedGlitch(t *testing.T) {
	fx := "Ì¶Í\u0083Í\u0087"
	tmpl := &wolvenkit.File{Entries: []wolvenkit.Entry{
		entry("1", "e"+fx+"no_data", ""),
		entry("2", "Ve spojitosti"+fx, ""),
		entry("3", "e"+fx+"no_data", ""),
	}}
	from := &wolvenkit.File{Entries: []wolvenkit.Entry{
		entry("1", "e"+fx+"няма_дадзеных", ""),
		entry("2", "Па справе"+fx, ""),
		entry("3", "няма", ""),
	}}
	r := portEntries(tmpl, from)
	want := map[string]string{"1": "e" + fx + "няма_дадзеных", "2": "Па справе" + fx}
	if !reflect.DeepEqual(r.Tr, want) {
		t.Errorf("Tr = %v\nwant %v", r.Tr, want)
	}
}

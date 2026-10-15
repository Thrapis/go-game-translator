package tcoaal

import (
	"strings"
	"testing"

	"github.com/Thrapis/go-game-translator/internal/markup"
)

func TestAnalyzeRoundTrip(t *testing.T) {
	a := analyzer{}
	cases := []string{
		"Just plain text.",
		`\c[2]Ashley\c[0]: hello there`,
		`He said "get out" and left.`,
		"Wait... what?!",
		"............................I see you found the batteries.",
		`\fiItalic\fr and (parenthetical) text`,
		"",
	}
	for _, in := range cases {
		if got := a.Render(a.Analyze(in)); got != in {
			t.Errorf("round trip mismatch\n in: %q\nout: %q", in, got)
		}
	}
}

func TestMaskUnmaskRoundTrip(t *testing.T) {
	a := analyzer{}
	cases := []string{
		"Just plain text.",
		`\c[2]Ashley\c[0]: hello there`,
		`He said "get out" and left.`,
		"Wait... what?!",
		`\fiItalic\fr and (parenthetical) text`,
		"",
	}
	for _, in := range cases {
		ps := a.Analyze(in)
		masked, markers := a.Mask(ps)
		if strings.Contains(masked, in) && strings.ContainsAny(in, `\"()`) {
			t.Errorf("Mask did not remove markup from %q: %q", in, masked)
		}
		if got := markup.Unmask(masked, markers); got != a.Render(ps) {
			t.Errorf("Unmask(Mask(%q)) = %q, want %q", in, got, a.Render(ps))
		}
	}
}

func TestMaskSplicesTranslation(t *testing.T) {
	a := analyzer{}
	ps := a.Analyze(`\c[2]Andy\c[0]: "hello world"`)
	masked, markers := a.Mask(ps)
	if masked != `§0§Andy§1§: §2§hello world§3§` {
		t.Fatalf("masked = %q", masked)
	}
	got := markup.Unmask("§0§Эндзі§1§: §2§прывітанне§3§", markers)
	if got != `\c[2]Эндзі\c[0]: "прывітанне"` {
		t.Errorf("spliced = %q", got)
	}
}

func TestTranslatableOnlyText(t *testing.T) {
	a := analyzer{}
	ps := a.Analyze(`\c[2]Andy\c[0]: "Leave me alone."`)
	got := a.Translatable(ps)
	if len(got) == 0 {
		t.Fatal("expected at least one translatable part")
	}
	for _, p := range got {
		if p.Type != typeString {
			t.Errorf("translatable part has non-string type %d (%q)", p.Type, p.Value)
		}
	}
}

func TestTranslateSubstitutionKeepsSymbols(t *testing.T) {
	a := analyzer{}
	ps := a.Analyze(`\c[2]Andy\c[0]: hello world`)
	for _, p := range a.Translatable(ps) {
		p.Value = strings.Replace(p.Value, strings.TrimSpace(p.Value), "XXX", 1)
	}
	got := a.Render(ps)
	for _, sym := range []string{`\c[2]`, `\c[0]`, "XXX"} {
		if !strings.Contains(got, sym) {
			t.Errorf("expected %q to survive translation, got %q", sym, got)
		}
	}
	if strings.Contains(got, "hello") {
		t.Errorf("source text leaked into output: %q", got)
	}
}

func TestPiecesLockOnlyEngineCodes(t *testing.T) {
	got := Pieces(`\c[2]Ashley\c[0]: "hello"... \fiyes\fr (ok)`)
	want := []Piece{
		{Text: `\c[2]`, Code: true},
		{Text: `Ashley`},
		{Text: `\c[0]`, Code: true},
		{Text: `: "hello"... `},
		{Text: `\fi`, Code: true},
		{Text: `yes`},
		{Text: `\fr`, Code: true},
		{Text: ` (ok)`},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d pieces %v, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("piece %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	var joined string
	for _, p := range got {
		joined += p.Text
	}
	if joined != `\c[2]Ashley\c[0]: "hello"... \fiyes\fr (ok)` {
		t.Errorf("pieces do not concatenate back to the input: %q", joined)
	}
}

package cyberpunk2077

import (
	"strings"
	"testing"

	"github.com/Thrapis/go-game-translator/internal/markup"
)

// Real strings from the game's ru-ru onscreens/subtitles.
var samples = []string{
	`Новости`,
	`<kiroshi l="jpn" o="フユツキは玄人向けのテックよ。" t="«Фуюцуки» — машина для ценителей." b="" a=""/>`,
	`<mothertongue l="mex" m="pez gordo" b="Привет, Ви. Мне все говорят, ты теперь " a="? Большая шишка?"/>`,
	`Вы сможете пользоваться этим предметом, когда показатель <Rich color="{statColor}">«{statName}»</> достигнет {value, number}.`,
	`Кровотечение <Image id="UIIcon.wounded_disabled_icon" align="bottom" width="75" height="75"></> медленно понижает уровень здоровья`,
	`Зажмите <Input actionName="UseCombatGadget" color="Tutorial.InputHint">Пробел</>, чтобы подготовить гранату.\nНажмите <Input actionName="UseCombatGadget" hold="Hide"></> один раз.`,
	`**ПОЛЬЗОВАТЕЛЬСКОЕ СОГЛАШЕНИЕ CD PROJEKT RED**\n\nверсия от 17 июля 2025\n\n## **Сначала – о главном**`,
	`Ой, прости. Я просто… забыла про тебя ¯\_(ツ)_/¯`,
	`<…> Перелом в её карьере наступил в 2069 году.`,
	"Первая строка\nвторая\tстрока\r\nтретья",
	`[en_us][db_db]Good evening, Night City. We begin with uh, uh...`,
	`KI113R`,
	`Mr. Kipper` + " " + `— Rolla`,
	`ДАННЫЕ ЗАШИФРОВАНЫ\nДЛЯ ПРОЧТЕНИЯ ВВЕДИТЕ КЛЮЧ\n\n#^*$^&@***@^TDG&^G@`,
	`<kiroshi l="jpn" o="x" t="Первая\nвторая" b="" a=""/>`,
	`<kiroshi l="creo" o="Mwen pa konnen." t="Не знаю, он упёрся как баран." b="" a="/>`,
	"\u00cc\u0091\u00cc\u00bc Файл повреждён \u00cd\u008e", // real glitch shape (onscreens 42696)
	``,
}

func TestRoundTrip(t *testing.T) {
	a := analyzer{}
	for _, s := range samples {
		ps := a.Analyze(s)
		if got := a.Render(ps); got != s {
			t.Errorf("Render(Analyze(%q)) = %q", s, got)
		}
		masked, markers := a.Mask(ps)
		if got := markup.Unmask(masked, markers); got != s {
			t.Errorf("Unmask(Mask(%q)) = %q (masked %q)", s, got, masked)
		}
		segs, seps := a.Segment(s)
		if len(seps) != len(segs)-1 {
			t.Errorf("Segment(%q): %d segs, %d seps", s, len(segs), len(seps))
			continue
		}
		var b strings.Builder
		for i, seg := range segs {
			b.WriteString(seg)
			if i < len(seps) {
				b.WriteString(seps[i])
			}
		}
		if b.String() != s {
			t.Errorf("Segment join = %q, want %q", b.String(), s)
		}
	}
}

func translatable(s string) []string {
	a := analyzer{}
	var out []string
	for _, p := range a.Translatable(a.Analyze(s)) {
		out = append(out, p.Value)
	}
	return out
}

func TestTranslatable(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{samples[1], []string{"«Фуюцуки» ", " машина для ценителей."}}, // — is masked
		{samples[2], []string{"Привет, Ви. Мне все говорят, ты теперь ", "? Большая шишка?"}},
		{samples[3], []string{"Вы сможете пользоваться этим предметом, когда показатель ", " достигнет "}},
		{samples[5], []string{"Зажмите ", "Пробел", ", чтобы подготовить гранату.", "Нажмите ", " один раз."}},
		{samples[10], nil}, // VO placeholder
		{samples[11], nil}, // no Cyrillic
		{samples[12], nil},
		{samples[15], nil}, // malformed tag (a="/>): kept whole as markup, never corrupted further
		{samples[16], []string{" Файл повреждён "}}, // readable text between glitch runs: only the runs are kept
	}
	for _, c := range cases {
		got := translatable(c.in)
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("Translatable(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

func TestMask(t *testing.T) {
	a := analyzer{}
	masked, markers := a.Mask(a.Analyze(samples[3]))
	want := "Вы сможете пользоваться этим предметом, когда показатель §0§ достигнет §1§"
	if masked != want {
		t.Errorf("masked = %q, want %q", masked, want)
	}
	if len(markers) != 2 || markers[0] != `<Rich color="{statColor}">«{statName}»</>` {
		t.Errorf("markers = %q", markers)
	}
}

// Characters the model mangles are masked; the symbols it keeps stay as text.
func TestMaskUnsafeSymbols(t *testing.T) {
	a := analyzer{}
	cases := []struct{ in, masked string }{
		{"архитектурной катастрофой 20–30-х годов", "архитектурной катастрофой 20§0§30-х годов"},
		{"Стань королём джунглей (ʘ‿ʘ) сегодня", "Стань королём джунглей (§0§) сегодня"},
		{"Цена: 5 €, № 7, «Лучшее» ®°©·×", "Цена: 5 €, № 7, «Лучшее» ®°©·×"},
		{"Цена — ничто. Имидж — всё…", "Цена §0§ ничто. Имидж §1§ всё§2§"},
		{"тип: ледокол )ю*ƥΏX Gуҿ", "тип: ледокол )ю*§0§X Gу§1§"},
		{"Параграф § 3", "Параграф §0§"}, // " 3" has no Cyrillic: kept with the §
	}
	for _, c := range cases {
		ps := a.Analyze(c.in)
		masked, markers := a.Mask(ps)
		if masked != c.masked {
			t.Errorf("Mask(%q) = %q, want %q", c.in, masked, c.masked)
		}
		if got := markup.Unmask(masked, markers); got != c.in {
			t.Errorf("Unmask round-trip = %q", got)
		}
	}
}

func TestSegment(t *testing.T) {
	a := analyzer{}
	segs, seps := a.Segment(samples[6])
	if len(segs) != 3 || seps[0] != `\n\n` {
		t.Fatalf("segs %q seps %q", segs, seps)
	}
	// A break inside a kiroshi attribute is not a boundary.
	if segs, _ := a.Segment(samples[14]); len(segs) != 1 {
		t.Errorf("kiroshi split into %q", segs)
	}
	if segs, _ := a.Segment("Первая строка\nвторая\tстрока\r\nтретья"); len(segs) != 3 {
		t.Errorf("real newlines: %q", segs)
	}
}

func TestIsGlitchText(t *testing.T) {
	if !IsGlitchText("C\u00cc\u0091o") || IsGlitchText("Новости «Корп-бад»\u00a0— 12") {
		t.Error("IsGlitchText misclassified")
	}
}

func TestPieces(t *testing.T) {
	got := Pieces(`Цена — ничто.\n<Rich color="MainColors.Gold">Жми</> {int_0} раз…`)
	want := []Piece{
		{`Цена — ничто.`, false},
		{`\n<Rich color="MainColors.Gold">`, true},
		{`Жми`, false},
		{`</>`, true},
		{` `, false}, // spacing between codes stays editable
		{`{int_0}`, true},
		{` раз…`, false},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("piece %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	k := Pieces(samples[1]) // kiroshi: only the t="…" text is editable
	if len(k) != 3 || !k[0].Code || k[1].Text != "«Фуюцуки» — машина для ценителей." || !k[2].Code {
		t.Errorf("kiroshi pieces = %+v", k)
	}
	if HasCyrillicText(Pieces("KI113R")) || !HasCyrillicText(Pieces("Новости")) {
		t.Error("HasCyrillicText misclassified")
	}
}

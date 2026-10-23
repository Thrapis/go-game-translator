package cyberpunk2077

import "testing"

func TestIsGlitchTextStrayControl(t *testing.T) {
	cases := map[string]bool{
		// A single stray C1 character inside real Russian text (a typo in the
		// Russian localization, absent from every other language) is not glitch.
		"«Где Джонни? Что же случилось в Арасака-тауэр\u008d?»": false,
		// Deliberate glitch effects inside Russian text carry several.
		"eÌ¶Í\u0083Í\u0087нет_данных":  true,
		"данные\u0083недоступны\u0084": true,
		// No Cyrillic letters: mojibake of the glitch effect, one character is enough.
		"CÌ\u0091o": true,
		"Новости":   false,
		"":          false,
	}
	for in, want := range cases {
		if got := IsGlitchText(in); got != want {
			t.Errorf("IsGlitchText(%q) = %v, want %v", in, got, want)
		}
	}
}

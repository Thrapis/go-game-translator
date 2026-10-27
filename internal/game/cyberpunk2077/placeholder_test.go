package cyberpunk2077

import "testing"

func TestStripVOPlaceholder(t *testing.T) {
	cases := map[string]string{
		"[en_us]Hey. How's it goin'?": "Hey. How's it goin'?",
		"[en_us][db_db]use":           "use", // every leading [xx_yy] tag goes
		"[en_us]":                     "",
		"Привет":                      "Привет",
		"[GT]Quickstart":              "[GT]Quickstart", // not a language tag
		"":                            "",
	}
	for in, want := range cases {
		if got := StripVOPlaceholder(in); got != want {
			t.Errorf("StripVOPlaceholder(%q) = %q, want %q", in, got, want)
		}
	}
}

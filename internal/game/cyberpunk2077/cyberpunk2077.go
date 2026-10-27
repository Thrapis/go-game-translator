// Package cyberpunk2077 supports Cyberpunk 2077 (REDengine 4) localization.
// The game's WolvenKit JSON exports are flattened to Crowdin CSV by
// tools/cp77loc before translation and rebuilt from it afterwards, so the
// pipeline itself only ever sees "crowdin-csv" files.
package cyberpunk2077

import (
	"regexp"

	_ "github.com/Thrapis/go-game-translator/internal/extract/crowdincsv" // register "crowdin-csv"
	"github.com/Thrapis/go-game-translator/internal/game"
	"github.com/Thrapis/go-game-translator/internal/markup"
)

func init() { game.Register(New()) }

// Game implements game.Game. Every row is independent: no replica grouping,
// parasitizing or verbatim rows.
type Game struct{}

// New returns a ready-to-register Cyberpunk 2077 game.
func New() *Game { return &Game{} }

func (*Game) Name() string              { return "cyberpunk2077" }
func (*Game) DefaultFormat() string     { return "crowdin-csv" }
func (*Game) DefaultDelimiter() string  { return "," }
func (*Game) Analyzer() markup.Analyzer { return analyzer{} }

var voPlaceholderRe = regexp.MustCompile(`^\[[A-Za-z]{2}_[A-Za-z]{2}\]`)

// IsVOPlaceholder reports whether s is an untranslated voice-over stub such as
// "[en_us][db_db]Good evening, Night City." - English text the game keeps as a
// placeholder in every language. Such strings are never translated.
func IsVOPlaceholder(s string) bool { return voPlaceholderRe.MatchString(s) }

// StripVOPlaceholder drops the leading tags of a VO placeholder, the language
// tag and any dev tags like [db_db] after it ("[en_us][db_db]use" -> "use");
// other strings are returned unchanged.
func StripVOPlaceholder(s string) string {
	for voPlaceholderRe.MatchString(s) {
		s = voPlaceholderRe.ReplaceAllString(s, "")
	}
	return s
}

// glitchRuns returns the [start, end) byte ranges of the Zalgo mojibake in s:
// runs of Latin-1 characters (U+0080-U+00FF), with the ASCII letters and
// punctuation the effect interleaves between them, that hold at least two C1
// control characters (U+0080-U+009F). Real Russian/Belarusian text never
// contains those; « » and nbsp, which are Latin-1 too, do not make a run, and
// neither does a single stray control character (a typo, see IsGlitchText).
func glitchRuns(s string) [][2]int {
	type rn struct {
		r   rune
		off int
	}
	var rs []rn
	for i, r := range s {
		rs = append(rs, rn{r, i})
	}
	rs = append(rs, rn{0, len(s)})
	n := len(rs) - 1
	high := func(i int) bool { return i < n && rs[i].r >= 0x80 && rs[i].r <= 0xFF }
	filler := func(i int) bool {
		r := rs[i].r
		return i < n && (r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r == ',' || r == '.')
	}
	var out [][2]int
	for i := 0; i < n; {
		if !high(i) && !(filler(i) && high(i+1)) {
			i++
			continue
		}
		start, c1, j := i, 0, i
		for {
			if high(j) {
				if rs[j].r <= 0x9F {
					c1++
				}
				j++
			} else if filler(j) && high(j+1) {
				j++
			} else {
				break
			}
		}
		if c1 >= 2 {
			out = append(out, [2]int{rs[start].off, rs[j].off})
		}
		i = j
	}
	return out
}

// firstGlitchRun is glitchRuns' first range, or nil.
func firstGlitchRun(s string) []int {
	if runs := glitchRuns(s); len(runs) > 0 {
		return runs[0][:]
	}
	return nil
}

// IsGlitchText reports whether s is one of the game's deliberately "corrupted"
// strings (glitched shards/e-mails): Zalgo text whose UTF-8 bytes were stored
// as Latin-1, which leaves C1 control characters in it, with nothing readable
// around it. Such strings are never translated.
//
// Readable Cyrillic text next to such a run (an e-mail whose end is corrupted,
// a title like "<zalgo>нет_данных") is translatable: Pieces locks the run as
// a code. A single stray control character inside real text is a typo in one
// localization rather than an effect: it is no run and stays plain text.
func IsGlitchText(s string) bool {
	runs := glitchRuns(s)
	if len(runs) == 0 {
		for _, r := range s {
			if r >= 0x80 && r <= 0x9F {
				return !cyrillic.MatchString(s)
			}
		}
		return false
	}
	rest, prev := make([]byte, 0, len(s)), 0
	for _, r := range runs {
		rest = append(rest, s[prev:r[0]]...)
		prev = r[1]
	}
	rest = append(rest, s[prev:]...)
	return !cyrillic.Match(rest)
}

// HasGlitchRun reports whether s contains a glitch effect, whatever else it
// holds: a string for which this is true but IsGlitchText is false is readable
// text with an effect in it.
func HasGlitchRun(s string) bool { return len(glitchRuns(s)) > 0 }

// Untranslatable reports whether s must be kept exactly as the source.
func Untranslatable(s string) bool { return IsVOPlaceholder(s) || IsGlitchText(s) }

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

var c1ControlRe = regexp.MustCompile(`[\x{80}-\x{9F}]`)

// IsGlitchText reports whether s is one of the game's deliberately "corrupted"
// strings (glitched shards/e-mails): Zalgo text whose UTF-8 bytes were stored
// as Latin-1, which leaves C1 control characters (U+0080-U+009F) in it. Real
// Russian/Belarusian text never contains those. Such strings are never
// translated.
//
// A single stray control character inside real Cyrillic text is not a glitch
// effect but a typo in one localization (the same line is clean in the others),
// so such text is translatable; the deliberate effects inside Cyrillic text
// carry several.
func IsGlitchText(s string) bool {
	n := len(c1ControlRe.FindAllStringIndex(s, -1))
	return n > 0 && (n > 1 || !cyrillic.MatchString(s))
}

// Untranslatable reports whether s must be kept exactly as the source.
func Untranslatable(s string) bool { return IsVOPlaceholder(s) || IsGlitchText(s) }

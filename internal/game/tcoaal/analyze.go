package tcoaal

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/Thrapis/go-game-translator/internal/markup"
)

// Part types produced by this analyzer.
const (
	typeString = iota
	typeStyleSymbol
	typeFontSizeSymbol
	typeColorSymbol
	typeQuotesSymbol
	typeBracketSymbol
	typeSpamSymbol
)

const (
	styleSymbolPattern    = `(\\f[ibr]{1})`
	fontSizeSymbolPattern = `(\\[\{\}]{1})`
	colorSymbolPattern    = `(\\c\[[0-9]{1}\])`
	quotesSymbolPattern   = `([\"]{1})`
	bracketSymbolPattern  = `(([\(]|[\)]){1})`
	spamSymbolPattern     = `(([\.]|[\?]|[\!]|(\.\s){1,}){2,})`
)

type patternType struct {
	re  *regexp.Regexp
	typ int
}

// symbolPatterns is the ordered list of non-text constructs. It is shared by
// segmentation (Analyze) and classification (detectPart).
var symbolPatterns = []patternType{
	{regexp.MustCompile(styleSymbolPattern), typeStyleSymbol},
	{regexp.MustCompile(fontSizeSymbolPattern), typeFontSizeSymbol},
	{regexp.MustCompile(colorSymbolPattern), typeColorSymbol},
	{regexp.MustCompile(quotesSymbolPattern), typeQuotesSymbol},
	{regexp.MustCompile(bracketSymbolPattern), typeBracketSymbol},
	{regexp.MustCompile(spamSymbolPattern), typeSpamSymbol},
}

type analyzer struct{}

func (analyzer) Analyze(text string) *markup.PartialString {
	result := &markup.PartialString{Parts: make([]*markup.StringPart, 0)}

	focus := text
	for len(focus) > 0 {
		firstIndex := len(focus)
		lastIndex := len(focus)

		for _, pt := range symbolPatterns {
			loc := pt.re.FindStringIndex(focus)
			if loc != nil && loc[0] < firstIndex {
				firstIndex, lastIndex = loc[0], loc[1]
			}
		}

		if firstIndex == len(focus) {
			result.Parts = append(result.Parts, detectPart(focus))
			break
		}

		before := focus[:firstIndex]
		target := focus[firstIndex:lastIndex]
		after := focus[lastIndex:]

		if len(before) > 0 {
			result.Parts = append(result.Parts, detectPart(before))
		}
		result.Parts = append(result.Parts, detectPart(target))

		if len(after) == 0 {
			break
		}
		focus = after
	}

	return result
}

func detectPart(text string) *markup.StringPart {
	for _, pt := range symbolPatterns {
		if pt.re.MatchString(text) {
			return &markup.StringPart{Type: pt.typ, Value: pt.re.FindStringSubmatch(text)[1]}
		}
	}
	return &markup.StringPart{Type: typeString, Value: text}
}

func (analyzer) Translatable(ps *markup.PartialString) []*markup.StringPart {
	out := make([]*markup.StringPart, 0, len(ps.Parts))
	for _, p := range ps.Parts {
		if p.Type == typeString {
			out = append(out, p)
		}
	}
	return out
}

func (analyzer) Render(ps *markup.PartialString) string {
	var b []byte
	for _, p := range ps.Parts {
		b = append(b, p.Value...)
	}
	return string(b)
}

// Mask implements markup.Masker: text parts stay as-is, every symbol part
// becomes a §i§ sentinel. Parts are flat here, so this is a single pass.
func (analyzer) Mask(ps *markup.PartialString) (string, []string) {
	var b strings.Builder
	var markers []string
	for _, p := range ps.Parts {
		if p.Type == typeString {
			b.WriteString(p.Value)
			continue
		}
		fmt.Fprintf(&b, "§%d§", len(markers))
		markers = append(markers, p.Value)
	}
	return b.String(), markers
}

// Piece is a run of a string as shown to a human translator: free text, or a
// Code - engine control symbol that must reach the game unchanged.
type Piece struct {
	Text string
	Code bool
}

// Pieces splits s for human translation (e.g. XLIFF export). Only the engine
// control symbols (\fi \fr \fb, \{ \}, \c[n]) are codes; quotes, brackets and
// dot runs are masked from the machine translator but are ordinary text to a
// person. Adjacent pieces of the same kind are merged.
func Pieces(s string) []Piece {
	var out []Piece
	for _, p := range (analyzer{}).Analyze(s).Parts {
		code := false
		switch p.Type {
		case typeStyleSymbol, typeFontSizeSymbol, typeColorSymbol:
			code = true
		}
		if n := len(out); n > 0 && out[n-1].Code == code && !code {
			out[n-1].Text += p.Value
		} else {
			out = append(out, Piece{Text: p.Value, Code: code})
		}
	}
	return out
}

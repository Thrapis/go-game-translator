package cyberpunk2077

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/Thrapis/go-game-translator/internal/markup"
)

// Part types produced by this analyzer. Parts are flat: a kiroshi/mothertongue
// tag is emitted as markup pieces around the text of its translatable
// attributes.
const (
	typeString     = iota // translatable text
	typeTag               // <Rich …>, </>, <Input …>, <nbsp/>, <…>, non-text pieces of attribute tags
	typeVariable          // {int_0}, {value, number}
	typeBreak             // hard line break (literal \n or LF): a segment boundary
	typeInnerBreak        // line break inside a tag attribute: not a boundary
	typeControl           // TAB, stray CR
	typeSpam              // symbol runs: **bold**, #^*$^&@
	typeVerbatim          // text never sent to the model: VO placeholders, glitch text, no Cyrillic
	typeSymbol            // characters the ru->be model mangles: — … –, rare Cyrillic, kaomoji, §
	typeGlitch            // a run of Zalgo mojibake inside readable text
)

var (
	// attrTagRe matches a whole speech tag whose attributes carry text:
	//   <kiroshi l="jpn" o="original" t="translation" b="before" a="after"/>
	//   <mothertongue l="mex" m="pez gordo" b="before " a=" after"/>
	// o and m hold the foreign-language original and are kept as markup.
	//
	// A value may hold escaped quotes (o="… \"word\" …"): the foreign-language
	// original is copied into the string as it was written.
	attrTagRe = regexp.MustCompile(`<(?:kiroshi|mothertongue)(?:\s+[A-Za-z_]+="(?:[^"\\]|\\.)*")*\s*/?>`)
	attrRe    = regexp.MustCompile(`\s+([A-Za-z_]+)="((?:[^"\\]|\\.)*)"`)
	tagRe     = regexp.MustCompile(`</?[A-Za-z][^<>]*>|</>|<…>`)
	// clipHeadRe and clipTailRe frame a string that is a JSON clipboard dump of
	// the editor, {"RootType": "…", "Data": {"value": "TEXT"}}, which the game
	// shows as TEXT (the oxygen bar label): only the value is text.
	clipHeadRe = regexp.MustCompile(`^\{\s*"RootType":\s*"[^"]*",\s*"Data":\s*\{\s*"value":\s*"`)
	clipTailRe = regexp.MustCompile(`"\s*\}\s*\}$`)
	varRe      = regexp.MustCompile(`\{[^{}]*\}`)
	breakRe    = regexp.MustCompile(`\\n|\r?\n`)
	controlRe  = regexp.MustCompile(`[\t\r]`)
	spamRe     = regexp.MustCompile(`[*#$%^&@~|_=+]{2,}`)
	// symbolRe matches runs of characters the ru->be model does not reproduce
	// (measured on the full base onscreens run), which are therefore kept
	// verbatim. Kept as text: Russian/Belarusian letters, printable ASCII,
	// whitespace and « » № € „ “ ” ‘ ’ ® ° © · ×. Masked, among others: — (became
	// "-" in 100% of rows), … ("..." in 97%), – ("우" in 93%), rare Cyrillic
	// such as ҿ Ѡ (" ⁇ "), kaomoji and box drawing, and § (sentinel syntax).
	symbolRe = regexp.MustCompile(`[^А-яЁёІіЎў\x20-\x7E\s«»№€„“”‘’®°©·×]+`)
	cyrillic = regexp.MustCompile(`\p{Cyrillic}`)
)

// textAttrs are the attribute-tag attributes whose value is translated.
var textAttrs = map[string]bool{"t": true, "b": true, "a": true}

type pattern struct {
	re  *regexp.Regexp
	typ int
	// find replaces re when set: it returns the [start, end) of the first match.
	find func(string) []int
}

func (p pattern) locate(s string) []int {
	if p.find != nil {
		return p.find(s)
	}
	return p.re.FindStringIndex(s)
}

// topLevel is tried in order; on equal start the earlier pattern wins, so a
// speech tag is expanded before the generic tag rule can swallow it.
var topLevel = []pattern{
	{re: attrTagRe, typ: -1},
	{re: clipHeadRe, typ: typeTag},
	{re: clipTailRe, typ: typeTag},
	{re: tagRe, typ: typeTag},
	{re: varRe, typ: typeVariable},
	{re: breakRe, typ: typeBreak},
	{re: controlRe, typ: typeControl},
	{re: spamRe, typ: typeSpam},
	{find: firstGlitchRun, typ: typeGlitch},
	{re: symbolRe, typ: typeSymbol},
}

// inAttr applies inside a translatable attribute value (markup tags such as
// <Rich …> may be nested in it).
var inAttr = []pattern{
	{re: tagRe, typ: typeTag},
	{re: varRe, typ: typeVariable},
	{re: breakRe, typ: typeInnerBreak},
	{re: controlRe, typ: typeControl},
	{re: spamRe, typ: typeSpam},
	{find: firstGlitchRun, typ: typeGlitch},
	{re: symbolRe, typ: typeSymbol},
}

type analyzer struct{}

func (analyzer) Analyze(text string) *markup.PartialString {
	if Untranslatable(text) {
		return &markup.PartialString{Parts: []*markup.StringPart{{Type: typeVerbatim, Value: text}}}
	}
	return &markup.PartialString{Parts: tokenize(text, topLevel)}
}

func tokenize(s string, pats []pattern) []*markup.StringPart {
	var parts []*markup.StringPart
	for len(s) > 0 {
		first, last, typ := len(s), len(s), 0
		for _, p := range pats {
			if loc := p.locate(s); loc != nil && loc[0] < first {
				first, last, typ = loc[0], loc[1], p.typ
			}
		}
		if first > 0 {
			parts = append(parts, textPart(s[:first]))
		}
		if first == len(s) {
			break
		}
		if typ == -1 {
			parts = append(parts, expandAttrTag(s[first:last])...)
		} else {
			parts = append(parts, &markup.StringPart{Type: typ, Value: s[first:last]})
		}
		s = s[last:]
	}
	return parts
}

// textPart classifies free text: only text with a Cyrillic letter is worth
// sending to a ru->be model; Latin names, codes and punctuation stay verbatim.
func textPart(s string) *markup.StringPart {
	if cyrillic.MatchString(s) {
		return &markup.StringPart{Type: typeString, Value: s}
	}
	return &markup.StringPart{Type: typeVerbatim, Value: s}
}

// expandAttrTag splits a matched speech tag into markup pieces and the
// tokenized values of its text attributes.
func expandAttrTag(tag string) []*markup.StringPart {
	var parts []*markup.StringPart
	mark := 0 // start of the markup not yet emitted
	for _, l := range attrRe.FindAllStringSubmatchIndex(tag, -1) {
		name, vs, ve := tag[l[2]:l[3]], l[4], l[5]
		if !textAttrs[name] || vs == ve {
			continue
		}
		parts = append(parts, &markup.StringPart{Type: typeTag, Value: tag[mark:vs]})
		parts = append(parts, tokenize(tag[vs:ve], inAttr)...)
		mark = ve
	}
	return append(parts, &markup.StringPart{Type: typeTag, Value: tag[mark:]})
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
	var b strings.Builder
	for _, p := range ps.Parts {
		b.WriteString(p.Value)
	}
	return b.String()
}

// Mask implements markup.Masker. Consecutive non-text parts collapse into one
// §i§ sentinel: fewer sentinels means fewer for the model to lose or move.
func (analyzer) Mask(ps *markup.PartialString) (string, []string) {
	var b, pending strings.Builder
	var markers []string
	flush := func() {
		if pending.Len() == 0 {
			return
		}
		fmt.Fprintf(&b, "§%d§", len(markers))
		markers = append(markers, pending.String())
		pending.Reset()
	}
	for _, p := range ps.Parts {
		if p.Type == typeString {
			flush()
			b.WriteString(p.Value)
		} else {
			pending.WriteString(p.Value)
		}
	}
	flush()
	return b.String(), markers
}

// Segment implements markup.Segmenter: it splits at top-level line breaks
// (runs of them form one separator). Breaks inside a tag attribute are not
// boundaries, so no segment ever holds half a tag.
func (a analyzer) Segment(s string) (segs, seps []string) {
	var cur, sep strings.Builder
	for _, p := range a.Analyze(s).Parts {
		if p.Type == typeBreak {
			sep.WriteString(p.Value)
			continue
		}
		if sep.Len() > 0 {
			segs, seps = append(segs, cur.String()), append(seps, sep.String())
			cur.Reset()
			sep.Reset()
		}
		cur.WriteString(p.Value)
	}
	if sep.Len() > 0 {
		segs, seps = append(segs, cur.String()), append(seps, sep.String())
		cur.Reset()
	}
	return append(segs, cur.String()), seps
}

// Piece is a run of a string as shown to a human translator: free text, or a
// Code - engine markup that must reach the game unchanged.
type Piece struct {
	Text string
	Code bool
}

// Pieces splits s for human translation (e.g. XLIFF export). Tags, {vars},
// line breaks, control characters and the non-text parts of speech tags are
// codes; everything else is text - including the symbols (— … –) and Latin
// runs that are masked from the machine translator only because the model
// mangles them. Adjacent pieces of the same kind are merged. A string that is
// never translated (VO placeholder, glitch text) is one code.
func Pieces(s string) []Piece {
	if Untranslatable(s) {
		return []Piece{{Text: s, Code: true}}
	}
	var out []Piece
	for _, p := range (analyzer{}).Analyze(s).Parts {
		code := false
		switch p.Type {
		case typeTag, typeVariable, typeBreak, typeInnerBreak, typeControl, typeGlitch:
			code = true
		}
		if n := len(out); n > 0 && out[n-1].Code == code {
			out[n-1].Text += p.Value
		} else {
			out = append(out, Piece{Text: p.Value, Code: code})
		}
	}
	return out
}

// HasCyrillicText reports whether any text piece holds a Cyrillic letter,
// i.e. whether the string has anything for a ru->be translator to do.
func HasCyrillicText(ps []Piece) bool {
	for _, p := range ps {
		if !p.Code && cyrillic.MatchString(p.Text) {
			return true
		}
	}
	return false
}

// Package wolvenkit reads the JSON that WolvenKit exports for Cyberpunk 2077
// localization resources (onscreens, subtitles) and writes translated text
// back into it.
//
// Writing never re-serialises the document. Splice edits only the string
// literals of femaleVariant / maleVariant in the original bytes, so every other
// byte - header, CRLF, indentation, empty keys - round-trips exactly, and the
// replaced literals are encoded the way WolvenKit (System.Text.Json) encodes
// them.
package wolvenkit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// MaleSuffix marks the male-variant key of an entry in a translation map; the
// female (default) variant uses the bare id.
const MaleSuffix = "@male"

// Entry is one localized string of a localization resource.
type Entry struct {
	// ID is primaryKey for on-screen entries and stringId for subtitles.
	ID           string
	SecondaryKey string
	Female, Male string
	// HasFemale/HasMale report whether the variant property exists at all
	// (subtitle-map entries have neither).
	HasFemale, HasMale bool
}

// Key returns the translation-map key of one variant.
func (e Entry) Key(male bool) string {
	if male {
		return e.ID + MaleSuffix
	}
	return e.ID
}

// File is the parsed text content of one exported resource.
type File struct {
	// Type is the root data $type, e.g. localizationPersistenceOnScreenEntries.
	Type    string
	Entries []Entry
}

type rawEntry struct {
	FemaleVariant *string `json:"femaleVariant"`
	MaleVariant   *string `json:"maleVariant"`
	PrimaryKey    string  `json:"primaryKey"`
	SecondaryKey  string  `json:"secondaryKey"`
	StringID      string  `json:"stringId"`
}

type rawFile struct {
	Data struct {
		RootChunk struct {
			Root struct {
				Data struct {
					Type    string     `json:"$type"`
					Entries []rawEntry `json:"entries"`
				} `json:"Data"`
			} `json:"root"`
		} `json:"RootChunk"`
	} `json:"Data"`
}

// Parse decodes an exported resource.
func Parse(b []byte) (*File, error) {
	var rf rawFile
	if err := json.Unmarshal(bytes.TrimPrefix(b, []byte("\xEF\xBB\xBF")), &rf); err != nil {
		return nil, fmt.Errorf("wolvenkit: %w", err)
	}
	d := rf.Data.RootChunk.Root.Data
	f := &File{Type: d.Type, Entries: make([]Entry, len(d.Entries))}
	for i, re := range d.Entries {
		e := Entry{ID: re.PrimaryKey, SecondaryKey: re.SecondaryKey}
		if e.ID == "" {
			e.ID = re.StringID
		}
		if re.FemaleVariant != nil {
			e.Female, e.HasFemale = *re.FemaleVariant, true
		}
		if re.MaleVariant != nil {
			e.Male, e.HasMale = *re.MaleVariant, true
		}
		f.Entries[i] = e
	}
	return f, nil
}

// Stats counts what Splice did to the variant literals it visited.
type Stats struct {
	Replaced int // literal rewritten with a translation
	Blanked  int // non-empty literal emptied (SpliceBlanking)
	Kept     int // non-empty literal left as it was
}

const (
	femaleProp = `"femaleVariant": `
	maleProp   = `"maleVariant": `
)

// Splice returns b with each variant literal replaced by tr[entry.Key(male)]
// when that value is non-empty. f must be Parse(b): the n-th femaleVariant
// (maleVariant) line is matched to the n-th entry that has the property, and
// its decoded literal must equal the parsed value, else Splice fails rather
// than write text into the wrong entry.
func Splice(b []byte, f *File, tr map[string]string) ([]byte, Stats, error) {
	return SpliceBlanking(b, f, tr, nil)
}

// SpliceBlanking is Splice that also empties every non-empty variant whose key
// is in blank (and has no translation in tr). An empty variant makes the game
// fall back to the female text, so this is how a male variant is removed.
func SpliceBlanking(b []byte, f *File, tr map[string]string, blank map[string]bool) ([]byte, Stats, error) {
	var fem, male []int // entry indices, in file order, that carry each property
	for i, e := range f.Entries {
		if e.HasFemale {
			fem = append(fem, i)
		}
		if e.HasMale {
			male = append(male, i)
		}
	}

	var st Stats
	var out bytes.Buffer
	out.Grow(len(b))
	nf, nm := 0, 0

	for rest := b; len(rest) > 0; {
		var line []byte
		if i := bytes.IndexByte(rest, '\n'); i >= 0 {
			line, rest = rest[:i+1], rest[i+1:]
		} else {
			line, rest = rest, nil
		}

		trimmed := bytes.TrimLeft(line, " ")
		isMale := bytes.HasPrefix(trimmed, []byte(maleProp))
		if !isMale && !bytes.HasPrefix(trimmed, []byte(femaleProp)) {
			out.Write(line)
			continue
		}

		var ei int
		if isMale {
			if nm >= len(male) {
				return nil, st, fmt.Errorf("wolvenkit: more maleVariant lines than entries")
			}
			ei, nm = male[nm], nm+1
		} else {
			if nf >= len(fem) {
				return nil, st, fmt.Errorf("wolvenkit: more femaleVariant lines than entries")
			}
			ei, nf = fem[nf], nf+1
		}
		e := f.Entries[ei]
		want := e.Female
		prop := femaleProp
		if isMale {
			want, prop = e.Male, maleProp
		}

		// line = indent + prop + literal + [","] + ["\r"] + "\n"
		head := len(line) - len(trimmed) + len(prop)
		tail := len(line)
		for tail > head && (line[tail-1] == '\n' || line[tail-1] == '\r' || line[tail-1] == ',') {
			tail--
		}
		var got string
		if err := json.Unmarshal(line[head:tail], &got); err != nil {
			return nil, st, fmt.Errorf("wolvenkit: entry %s: bad literal: %w", e.ID, err)
		}
		if got != want {
			return nil, st, fmt.Errorf("wolvenkit: entry %s: literal does not match parsed value", e.ID)
		}

		v := tr[e.Key(isMale)]
		if v == "" {
			switch {
			case want == "":
			case blank[e.Key(isMale)]:
				out.Write(line[:head])
				out.WriteString(`""`)
				out.Write(line[tail:])
				st.Blanked++
				continue
			default:
				st.Kept++
			}
			out.Write(line)
			continue
		}
		out.Write(line[:head])
		out.WriteString(Quote(v))
		out.Write(line[tail:])
		st.Replaced++
	}

	if nf != len(fem) || nm != len(male) {
		return nil, st, fmt.Errorf("wolvenkit: found %d/%d femaleVariant and %d/%d maleVariant lines",
			nf, len(fem), nm, len(male))
	}
	return out.Bytes(), st, nil
}

// Quote returns s as a JSON string literal encoded like System.Text.Json's
// default encoder, which WolvenKit uses: pure ASCII output, every non-ASCII
// rune and the HTML-sensitive " & ' + < > ` as uppercase \uXXXX, backslash and
// \b \f \n \r \t as short escapes.
func Quote(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\b':
			b.WriteString(`\b`)
		case r == '\f':
			b.WriteString(`\f`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || r == 0x7F || strings.ContainsRune("\"&'+<>`", r):
			fmt.Fprintf(&b, `\u%04X`, r)
		case r < utf8.RuneSelf:
			b.WriteRune(r)
		case r > 0xFFFF:
			r -= 0x10000
			fmt.Fprintf(&b, `\u%04X\u%04X`, 0xD800+(r>>10), 0xDC00+(r&0x3FF))
		default:
			fmt.Fprintf(&b, `\u%04X`, r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

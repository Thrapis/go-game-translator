// Package tcoaaltxt implements the "The Coffin of Andy and Leyley" combined
// export TXT (dialogue.txt). It registers as "tcoaal-txt".
//
// The file is UTF-8, CRLF, organised as "[SECTION]" blocks. VERSION / LANGUAGE
// / FONT / CREDITS are metadata and pass through untouched. LABELS / MENUS /
// SPEAKERS / ITEMS are "key : value" lines. DESCRIPTIONS, CHOICES and every
// "[MapNNN.json]" section use block form: a "#id (Speaker)" header followed by
// one or more ": value" lines (consecutive value lines under one id form a
// multi-row replica). Only the value after " : " / ": " is translated; keys,
// ids, headers, blank lines and metadata are emitted verbatim.
package tcoaaltxt

import (
	"fmt"
	"io"
	"strings"

	"github.com/Thrapis/go-game-translator/internal/extract"
)

const (
	crlf    = "\r\n"
	utf8BOM = "\xef\xbb\xbf"
)

func init() {
	extract.Register("tcoaal-txt", func() extract.Format { return format{} })
}

type sectionKind int

const (
	kindMeta  sectionKind = iota // VERSION (kept from source)
	kindBare                     // LANGUAGE (one bare value line)
	kindKV                       // FONT / CREDITS / LABELS / MENUS / SPEAKERS / ITEMS
	kindBlock                    // DESCRIPTIONS / CHOICES / *.json
)

func kindOf(name string) sectionKind {
	switch name {
	case "VERSION":
		return kindMeta
	case "LANGUAGE":
		return kindBare
	case "FONT", "CREDITS", "LABELS", "MENUS", "SPEAKERS", "ITEMS":
		return kindKV
	default:
		return kindBlock
	}
}

// Doc is a parsed dialogue.txt: every line verbatim, plus the translatable slots.
type Doc struct {
	lines []string
	slots []slot
}

type slot struct {
	line   int
	prefix string // value == lines[line][len(prefix):]
	tag    string // replica id, for grouping consecutive value lines
	// section and header locate the slot for tools; they do not affect Compose.
	section string // "[SECTION]" name the slot sits in
	header  string // "#id (Speaker)" block header line, "" outside blocks
}

// ParseDoc reads a combined dialogue.txt and reports the newline it uses.
func ParseDoc(r io.Reader) (*Doc, string, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, "", err
	}
	text := strings.TrimPrefix(string(raw), utf8BOM)
	nl := "\n"
	if strings.Contains(text, crlf) {
		nl = crlf
	}

	rawLines := strings.Split(text, "\n")
	d := &Doc{lines: make([]string, len(rawLines))}

	kind := kindMeta
	section := ""
	blockID := ""
	blockHeader := ""

	for i, rl := range rawLines {
		line := strings.TrimSuffix(rl, "\r")
		d.lines[i] = line

		if name, ok := bracketName(line); ok {
			kind, section, blockID, blockHeader = kindOf(name), name, "", ""
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}

		switch kind {
		case kindBare:
			// LANGUAGE: the whole line is the value, keyed by the section name.
			d.slots = append(d.slots, slot{line: i, prefix: "", tag: section, section: section})
		case kindKV:
			if p := kvPrefix(line); p != "" {
				tag := ""
				if strings.HasPrefix(line, "#") { // SPEAKERS / ITEMS: id-keyed
					tag = blockHeaderID(strings.TrimSuffix(p, " : "))
				} else { // FONT / CREDITS / LABELS / MENUS: keyed by section + key text
					tag = section + "/" + strings.TrimRight(strings.TrimSuffix(p, " : "), " ")
				}
				d.slots = append(d.slots, slot{line: i, prefix: p, tag: tag, section: section})
			}
		case kindBlock:
			switch {
			case strings.HasPrefix(line, "#"):
				if p, id := inlineEntry(line); p != "" {
					d.slots = append(d.slots, slot{line: i, prefix: p, tag: id, section: section})
				} else {
					blockID, blockHeader = blockHeaderID(line), line
				}
			case strings.HasPrefix(line, ":"):
				p := ":"
				if strings.HasPrefix(line, ": ") {
					p = ": "
				}
				d.slots = append(d.slots, slot{line: i, prefix: p, tag: blockID, section: section, header: blockHeader})
			}
		}
	}
	return d, nl, nil
}

func bracketName(line string) (string, bool) {
	if len(line) >= 2 && line[0] == '[' && line[len(line)-1] == ']' {
		return line[1 : len(line)-1], true
	}
	return "", false
}

// kvPrefix returns the text up to and including the first " : " separator.
func kvPrefix(line string) string {
	if i := strings.Index(line, " : "); i >= 0 {
		return line[:i+3]
	}
	return ""
}

// inlineEntry handles a "#id : value" line (a choice): it returns prefix
// "#id : " and "#id". A "#id (Speaker)" block header returns "".
func inlineEntry(line string) (prefix, id string) {
	i := strings.Index(line, " : ")
	if i < 0 || strings.Contains(line[:i], " (") {
		return "", ""
	}
	return line[:i+3], line[:i]
}

// blockHeaderID extracts "#id" from a "#id (Speaker)" header line.
func blockHeaderID(line string) string {
	if i := strings.IndexByte(line, ' '); i >= 0 {
		return line[:i]
	}
	return line
}

func (d *Doc) lineData() []extract.DataLine {
	out := make([]extract.DataLine, len(d.slots))
	for i, sl := range d.slots {
		out[i] = extract.DataLine{
			Key:   fmt.Sprintf("%d", i),
			Value: d.lines[sl.line][len(sl.prefix):],
			Tag:   sl.tag,
		}
	}
	return out
}

// Entry is one translatable slot, for tools that need more than extract.DataLine.
type Entry struct {
	Index   int    // position among the slots; equals the DataLine Key
	Section string // "[SECTION]" name, e.g. "MENUS" or "Map001.json"
	Header  string // "#id (Speaker)" block header, "" outside blocks
	Tag     string // replica id, as in extract.DataLine.Tag
	Value   string // the translatable text
}

// Entries lists every translatable slot in document order.
func (d *Doc) Entries() []Entry {
	out := make([]Entry, len(d.slots))
	for i, sl := range d.slots {
		out[i] = Entry{Index: i, Section: sl.section, Header: sl.header, Tag: sl.tag,
			Value: d.lines[sl.line][len(sl.prefix):]}
	}
	return out
}

// Write serialises the document with the given newline.
func (d *Doc) Write(w io.Writer, nl string) error {
	_, err := io.WriteString(w, strings.Join(d.lines, nl))
	return err
}

type format struct{}

func (format) Extract(r io.Reader, _ string) ([]extract.DataLine, *extract.Settings, error) {
	d, nl, err := ParseDoc(r)
	if err != nil {
		return nil, nil, err
	}
	return d.lineData(), &extract.Settings{LineDelimiter: nl, Extra: d}, nil
}

func (format) Compose(w io.Writer, lines []extract.DataLine, s *extract.Settings, _ string) error {
	d, ok := s.Extra.(*Doc)
	if !ok || d == nil {
		return fmt.Errorf("tcoaal txt: compose without a parsed document")
	}
	if len(lines) != len(d.slots) {
		return fmt.Errorf("tcoaal txt: got %d lines, document has %d translatable slots", len(lines), len(d.slots))
	}
	nl := s.LineDelimiter
	if nl == "" {
		nl = crlf
	}
	for i, l := range lines {
		sl := d.slots[i]
		d.lines[sl.line] = sl.prefix + l.Value
	}
	return d.Write(w, nl)
}

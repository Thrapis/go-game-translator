// Command tcoaalxliff turns a translated TCOAAL dialogue.txt into XLIFF 1.2
// for review in a CAT tool: one .xlf per "[SECTION]" of the file, with the
// original-language dialogue.txt as source and the translated one as target.
//
//	tcoaalxliff -source "TCoAaL 3.0.13 - EN - TXT/dialogue.txt" -target "BE - TXT/dialogue.txt" -out "BE - XLIFF"
//
// Both files must have the same layout (the translated one is produced from
// the source by the pipeline, so it does). Unit ids are the slot index, which
// is unique across the document. Engine control symbols (\fi \fr, \{ \}, \c[n])
// become locked <ph> tags; the "#id (Speaker)" header is the note. A filled
// translation is a target in state needs-review-translation. VERSION,
// LANGUAGE, FONT and CREDITS are not translated text and are left out; rows
// with an empty source are left out. Repeated sections (the many [CHOICES])
// are merged into one file.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Thrapis/go-game-translator/internal/extract/tcoaaltxt"
	"github.com/Thrapis/go-game-translator/internal/game/tcoaal"
	"github.com/Thrapis/go-game-translator/internal/xliff"
)

// skipped sections hold identity/metadata, not dialogue.
var skipped = map[string]bool{"VERSION": true, "LANGUAGE": true, "FONT": true, "CREDITS": true}

func main() {
	source := flag.String("source", "", "source-language dialogue.txt (required)")
	target := flag.String("target", "", "translated dialogue.txt (required)")
	out := flag.String("out", "", "folder to write the .xlf files into (required)")
	srcLang := flag.String("source-lang", "en", "source language code")
	tgtLang := flag.String("target-lang", "be", "target language code")
	flag.Parse()
	if *source == "" || *target == "" || *out == "" {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(*source, *target, *out, *srcLang, *tgtLang); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(sourcePath, targetPath, outDir, srcLang, tgtLang string) error {
	src, err := load(sourcePath)
	if err != nil {
		return err
	}
	tgt, err := load(targetPath)
	if err != nil {
		return err
	}
	files, err := buildFiles(src, tgt, srcLang, tgtLang)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	var units, targets, dropped int
	for _, f := range files {
		var buf bytes.Buffer
		lost, err := xliff.Write(&buf, f)
		if err != nil {
			return err
		}
		if len(lost) > 0 {
			fmt.Fprintf(os.Stderr, "warn: %s: tags of %d translations do not match the source, written without target: %v\n", f.Original, len(lost), lost)
		}
		if err := os.WriteFile(filepath.Join(outDir, fileName(f.Original)), buf.Bytes(), 0o644); err != nil {
			return err
		}
		units += len(f.Units)
		dropped += len(lost)
		for _, u := range f.Units {
			if u.Target != nil {
				targets++
			}
		}
	}
	fmt.Printf("wrote %d units (%d with a translation to review, %d dropped for tag mismatch) into %d files -> %s\n",
		units, targets-dropped, dropped, len(files), outDir)
	return nil
}

func load(path string) (*tcoaaltxt.Doc, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	d, _, err := tcoaaltxt.ParseDoc(fh)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return d, nil
}

// buildFiles pairs the slots of two documents with the same layout into one
// xliff.File per distinct section, in order of first appearance.
func buildFiles(src, tgt *tcoaaltxt.Doc, srcLang, tgtLang string) ([]*xliff.File, error) {
	se, te := src.Entries(), tgt.Entries()
	if len(se) != len(te) {
		return nil, fmt.Errorf("source has %d translatable rows, target %d: not the same document", len(se), len(te))
	}
	var files []*xliff.File
	bySection := map[string]*xliff.File{}
	for i, s := range se {
		t := te[i]
		if s.Section != t.Section || s.Tag != t.Tag {
			return nil, fmt.Errorf("row %d: source is %s %s, target is %s %s: not the same document", i, s.Section, s.Tag, t.Section, t.Tag)
		}
		if skipped[s.Section] || strings.TrimSpace(s.Value) == "" {
			continue
		}
		f := bySection[s.Section]
		if f == nil {
			f = &xliff.File{Original: s.Section, SourceLang: srcLang, TargetLang: tgtLang}
			bySection[s.Section] = f
			files = append(files, f)
		}
		note := s.Header
		if note == "" {
			note = s.Tag
		}
		u := xliff.Unit{ID: strconv.Itoa(s.Index), Note: note, Source: toXLIFF(tcoaal.Pieces(s.Value))}
		if strings.TrimSpace(t.Value) != "" {
			u.Target = toXLIFF(tcoaal.Pieces(t.Value))
			u.State = "needs-review-translation"
		}
		f.Units = append(f.Units, u)
	}
	return files, nil
}

func toXLIFF(ps []tcoaal.Piece) []xliff.Piece {
	out := make([]xliff.Piece, len(ps))
	for i, p := range ps {
		out[i] = xliff.Piece{Text: p.Text, Code: p.Code}
	}
	return out
}

func fileName(section string) string { return section + ".xlf" }

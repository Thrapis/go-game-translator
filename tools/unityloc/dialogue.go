package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Thrapis/go-game-translator/internal/unityfs"
	"github.com/Thrapis/go-game-translator/internal/xliff"
)

// The game's dialogue lives in a Pixel Crushers Dialogue System database, a
// MonoBehaviour ("Restory Story"). Every dialogue entry has a list of
// {title, value} fields: "Dialogue Text" (the first, outdated Russian), one
// field per language code ("ru", "en", ...), and the articy id that names the
// entry. The game shows the field of the selected language, so "ru" is what a
// Russian player reads and what gets replaced.
const (
	fieldID   = "Articy Id"
	fieldBase = "Dialogue Text"
)

// dialogueCodeRe matches BBCode-style tags that must survive translation.
var dialogueCodeRe = regexp.MustCompile(`\[/?(?:b|i|u|color(?:=[^\]]*)?|size(?:=[^\]]*)?)\]`)

// dialogueLine is one entry that has text in the source language.
type dialogueLine struct {
	ID      string
	Speaker string
	Text    map[string]string // language field -> text, for the fields asked for
}

func asList(v any) []any         { l, _ := v.([]any); return l }
func asMap(v any) map[string]any { m, _ := v.(map[string]any); return m }

// fieldValue returns the value of the entry field with the given title.
func fieldValue(e map[string]any, title string) (string, bool) {
	for _, f := range asList(e["fields"]) {
		fm := asMap(f)
		if fm["title"] == title {
			s, _ := fm["value"].(string)
			return s, true
		}
	}
	return "", false
}

// dialogueEntries lists every dialogue entry of the database in file order.
func dialogueEntries(db map[string]any) []map[string]any {
	var out []map[string]any
	for _, c := range asList(db["conversations"]) {
		for _, e := range asList(asMap(c)["dialogueEntries"]) {
			out = append(out, asMap(e))
		}
	}
	return out
}

// actorNames maps actor id to its Name field.
func actorNames(db map[string]any) map[string]string {
	names := map[string]string{}
	for _, a := range asList(db["actors"]) {
		am := asMap(a)
		id, ok := am["id"].(int64)
		if !ok {
			continue
		}
		if n, ok := fieldValue(am, "Name"); ok {
			names[strconv.FormatInt(id, 10)] = n
		}
	}
	return names
}

// dialogueLines returns the entries that have text in the source field, with
// the text of every language in want. Entry ids must be unique.
func dialogueLines(db map[string]any, source string, want ...string) ([]dialogueLine, error) {
	names := actorNames(db)
	seen := map[string]bool{}
	var out []dialogueLine
	for _, e := range dialogueEntries(db) {
		src, _ := fieldValue(e, source)
		if strings.TrimSpace(src) == "" {
			continue
		}
		id, ok := fieldValue(e, fieldID)
		if !ok || id == "" {
			return nil, fmt.Errorf("a dialogue entry with %q text has no %q", source, fieldID)
		}
		if seen[id] {
			return nil, fmt.Errorf("duplicate %s %s", fieldID, id)
		}
		seen[id] = true
		l := dialogueLine{ID: id, Text: map[string]string{source: src}}
		for _, w := range want {
			if v, ok := fieldValue(e, w); ok {
				l.Text[w] = v
			}
		}
		actor, _ := fieldValue(e, "Actor")
		l.Speaker = names[actor]
		out = append(out, l)
	}
	return out, nil
}

func dialoguePieces(s string) []xliff.Piece {
	var out []xliff.Piece
	last := 0
	for _, loc := range dialogueCodeRe.FindAllStringIndex(s, -1) {
		if loc[0] > last {
			out = append(out, xliff.Piece{Text: s[last:loc[0]]})
		}
		out = append(out, xliff.Piece{Text: s[loc[0]:loc[1]], Code: true})
		last = loc[1]
	}
	if last < len(s) {
		out = append(out, xliff.Piece{Text: s[last:]})
	}
	return out
}

// locateObject finds the MonoBehaviour with the given m_Name in the bundles
// under aa and returns it with its decoded value.
func locateObject(aa, name string) (path string, b *unityfs.Bundle, f *unityfs.File, o *unityfs.Object, v map[string]any, err error) {
	paths, err := findBundles(aa, "")
	if err != nil {
		return "", nil, nil, nil, nil, err
	}
	for _, p := range paths {
		bu, err := unityfs.Open(p)
		if err != nil {
			continue
		}
		files, err := bu.Files()
		if err != nil {
			continue
		}
		for _, file := range files {
			for _, obj := range file.Objects {
				if obj.ClassID != unityfs.ClassMonoBehavior || !obj.HasTree() || obj.Size < 64<<10 {
					continue
				}
				m, err := obj.Read()
				if err == nil && m["m_Name"] == name && len(asList(m["conversations"])) > 0 {
					return p, bu, file, obj, m, nil
				}
			}
		}
	}
	return "", nil, nil, nil, nil, fmt.Errorf("no dialogue database named %q under %s", name, aa)
}

// runStoryXLIFF writes the dialogue as an XLIFF 1.2 file: the source language
// field as <source> and, optionally, another language field as <target>.
func runStoryXLIFF(args []string) error {
	fl := flag.NewFlagSet("story-xliff", flag.ExitOnError)
	game := fl.String("game", "", "game root folder (required)")
	out := fl.String("out", "", "output .xlf file (required)")
	name := fl.String("db", "Restory Story", "name of the dialogue database object")
	source := fl.String("source", "ru", "language field used as source")
	target := fl.String("target", "", "language field used as target (empty = no targets)")
	srcLang := fl.String("source-lang", "ru", "source language code")
	tgtLang := fl.String("target-lang", "be", "target language code")
	state := fl.String("state", "needs-review-translation", "target state for filled targets (final for an official reference)")
	loose := fl.Bool("loose", false, "reference file: keep targets whose tags do not match the source")
	fl.Parse(args)
	if *game == "" || *out == "" {
		fl.Usage()
		os.Exit(2)
	}
	aa, _, err := gameAA(*game)
	if err != nil {
		return err
	}
	_, _, _, _, db, err := locateObject(aa, *name)
	if err != nil {
		return err
	}
	var want []string
	if *target != "" {
		want = append(want, *target)
	}
	lines, err := dialogueLines(db, *source, want...)
	if err != nil {
		return err
	}
	f := &xliff.File{Original: "RestoryStory", SourceLang: *srcLang, TargetLang: *tgtLang, LooseTargetCodes: *loose}
	var targets int
	for _, l := range lines {
		u := xliff.Unit{ID: l.ID, Source: dialoguePieces(l.Text[*source])}
		if l.Speaker != "" {
			u.Note = "Speaker: " + l.Speaker
		}
		if t := l.Text[*target]; *target != "" && strings.TrimSpace(t) != "" {
			u.Target, u.State = dialoguePieces(t), *state
			targets++
		}
		f.Units = append(f.Units, u)
	}
	var buf bytes.Buffer
	dropped, err := xliff.Write(&buf, f)
	if err != nil {
		return err
	}
	if len(dropped) > 0 {
		fmt.Fprintf(os.Stderr, "warn: tags of %d targets do not match the source, written without target: %v\n", len(dropped), dropped)
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(*out, buf.Bytes(), 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %d dialogue lines (%d with a target, %d dropped for tag mismatch) -> %s\n", len(f.Units), targets-len(dropped), len(dropped), *out)
	return nil
}

// gameAA resolves <game>/<*_Data>/StreamingAssets/aa and its path relative
// to the game root.
func gameAA(game string) (aa, aaRel string, err error) {
	datas, _ := filepath.Glob(filepath.Join(game, "*_Data"))
	if len(datas) != 1 {
		return "", "", fmt.Errorf("expected one *_Data folder in %s, found %d", game, len(datas))
	}
	aaRel = filepath.Join(filepath.Base(datas[0]), "StreamingAssets", "aa")
	return filepath.Join(game, aaRel), aaRel, nil
}

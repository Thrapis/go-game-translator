package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Thrapis/go-game-translator/internal/unityfs"
	"github.com/Thrapis/go-game-translator/internal/xliff"
)

// metaKeys are bookkeeping entries inside every language table, not text.
var metaKeys = map[string]bool{"ID": true, "ISO": true, "string": true, "$type": true}

// table is one language: its string keys in file order.
type table struct {
	keys []string
	m    map[string]string
}

// locFile is the Localization asset: language name -> table, in file order.
type locFile struct {
	langs  []string
	tables map[string]*table
}

// parseLoc reads the {"<Language>": {"<KEY>": "<text>", ...}, ...} document
// keeping key order, which a Go map would lose.
func parseLoc(data []byte) (*locFile, error) {
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))))
	if err := expectDelim(dec, '{'); err != nil {
		return nil, err
	}
	lf := &locFile{tables: map[string]*table{}}
	for dec.More() {
		name, err := keyToken(dec)
		if err != nil {
			return nil, err
		}
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		if d, ok := tok.(json.Delim); !ok || d != '{' { // e.g. "$type": "System..."
			continue
		}
		t := &table{m: map[string]string{}}
		for dec.More() {
			k, err := keyToken(dec)
			if err != nil {
				return nil, err
			}
			var v string
			if err := dec.Decode(&v); err != nil {
				return nil, fmt.Errorf("%s/%s: %w", name, k, err)
			}
			if metaKeys[k] {
				continue
			}
			if _, dup := t.m[k]; !dup {
				t.keys = append(t.keys, k)
			}
			t.m[k] = v
		}
		if err := expectDelim(dec, '}'); err != nil {
			return nil, err
		}
		lf.langs = append(lf.langs, name)
		lf.tables[name] = t
	}
	return lf, nil
}

func expectDelim(dec *json.Decoder, want json.Delim) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != want {
		return fmt.Errorf("expected %q, got %v", want, tok)
	}
	return nil
}

func keyToken(dec *json.Decoder) (string, error) {
	tok, err := dec.Token()
	if err != nil {
		return "", err
	}
	s, ok := tok.(string)
	if !ok {
		return "", fmt.Errorf("expected a key, got %v", tok)
	}
	return s, nil
}

// findTextAsset returns the contents of the first TextAsset named name in the
// bundles under in.
func findTextAsset(in, bundleFilter, name string) ([]byte, string, error) {
	paths, err := findBundles(in, bundleFilter)
	if err != nil {
		return nil, "", err
	}
	for _, p := range paths {
		b, err := unityfs.Open(p)
		if err != nil {
			continue
		}
		files, err := b.Files()
		if err != nil {
			continue
		}
		for _, f := range files {
			for _, o := range f.Objects {
				if o.ClassID != unityfs.ClassTextAsset || !o.HasTree() {
					continue
				}
				m, err := o.Read()
				if err != nil || m["m_Name"] != name {
					continue
				}
				script, _ := m["m_Script"].(string)
				return []byte(script), filepath.Base(p), nil
			}
		}
	}
	return nil, "", fmt.Errorf("no TextAsset named %q under %s", name, in)
}

// runExport writes the game's Localization asset verbatim, plus one flat
// <Language>.json (key -> text, file order) per language.
func runExport(args []string) error {
	fl := flag.NewFlagSet("export", flag.ExitOnError)
	in := fl.String("in", "", "bundle folder, e.g. <game>/<Game>_Data/StreamingAssets/aa (required)")
	out := fl.String("out", "", "output folder (required)")
	bundle := fl.String("bundle", "data", "only bundles whose file name contains this")
	asset := fl.String("asset", "Localization", "name of the TextAsset holding all languages")
	fl.Parse(args)
	if *in == "" || *out == "" {
		fl.Usage()
		os.Exit(2)
	}
	raw, from, err := findTextAsset(*in, *bundle, *asset)
	if err != nil {
		return err
	}
	lf, err := parseLoc(raw)
	if err != nil {
		return fmt.Errorf("%s: %w", *asset, err)
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(*out, *asset+".json"), raw, 0o644); err != nil {
		return err
	}
	for _, lang := range lf.langs {
		t := lf.tables[lang]
		var buf bytes.Buffer
		buf.WriteString("{\n")
		for i, k := range t.keys {
			kb, _ := marshalNoEscape(k)
			vb, _ := marshalNoEscape(t.m[k])
			sep := ","
			if i == len(t.keys)-1 {
				sep = ""
			}
			fmt.Fprintf(&buf, "  %s: %s%s\n", kb, vb, sep)
		}
		buf.WriteString("}\n")
		if err := os.WriteFile(filepath.Join(*out, lang+".json"), buf.Bytes(), 0o644); err != nil {
			return err
		}
		fmt.Printf("%-18s %d strings\n", lang, len(t.keys))
	}
	fmt.Printf("extracted %q from %s -> %s\n", *asset, from, *out)
	return nil
}

// marshalNoEscape is json.Marshal without HTML escaping, so < > & stay readable.
func marshalNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// codeRe matches the markup that must reach the game unchanged: {0} format
// arguments and <tag> rich-text tags.
var codeRe = regexp.MustCompile(`\{[0-9]+[^{}]*\}|<[^<>\n]+>`)

func pieces(s string) []xliff.Piece {
	var out []xliff.Piece
	last := 0
	for _, loc := range codeRe.FindAllStringIndex(s, -1) {
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

// runXLIFF turns the extracted languages into one XLIFF 1.2 file: source
// language as <source>, an optional second language as <target>.
func runXLIFF(args []string) error {
	fl := flag.NewFlagSet("xliff", flag.ExitOnError)
	in := fl.String("in", "", "Localization.json written by export (required)")
	out := fl.String("out", "", "output .xlf file (required)")
	source := fl.String("source", "Russian", "language table used as source")
	target := fl.String("target", "", "language table(s) used as target, comma-separated, first one that has the key wins (empty = no targets)")
	targetJSON := fl.String("target-json", "", "flat key->text JSON (e.g. a translated Belarusian) used as target instead of -target")
	context := fl.String("context", "", "language table(s) shown to the translator as a note, comma-separated, first that has the key wins")
	srcLang := fl.String("source-lang", "ru", "source language code")
	tgtLang := fl.String("target-lang", "en", "target language code")
	state := fl.String("state", "needs-review-translation", "target state for filled targets (use final for an official reference)")
	loose := fl.Bool("loose", false, "reference file: keep targets whose tags do not match the source")
	fl.Parse(args)
	if *in == "" || *out == "" {
		fl.Usage()
		os.Exit(2)
	}
	raw, err := os.ReadFile(*in)
	if err != nil {
		return err
	}
	lf, err := parseLoc(raw)
	if err != nil {
		return err
	}
	src := lf.tables[*source]
	if src == nil {
		return fmt.Errorf("no language %q in %s (have %v)", *source, *in, lf.langs)
	}
	lookup := func(langs, key string) (string, bool, error) {
		for _, l := range strings.Split(langs, ",") {
			l = strings.TrimSpace(l)
			if l == "" {
				continue
			}
			t := lf.tables[l]
			if t == nil {
				return "", false, fmt.Errorf("no language %q in %s (have %v)", l, *in, lf.langs)
			}
			if v, ok := t.m[key]; ok && strings.TrimSpace(v) != "" {
				return v, true, nil
			}
		}
		return "", false, nil
	}
	var flat map[string]string
	if *targetJSON != "" {
		if flat, err = readFlat(*targetJSON); err != nil {
			return err
		}
	}

	f := &xliff.File{Original: "Localization.json", SourceLang: *srcLang, TargetLang: *tgtLang, LooseTargetCodes: *loose}
	var targets int
	for _, k := range src.keys {
		text := src.m[k]
		if strings.TrimSpace(text) == "" {
			continue
		}
		u := xliff.Unit{ID: k, Source: pieces(text)}
		if note, ok, err := lookup(*context, k); err != nil {
			return err
		} else if ok {
			u.Note = note
		}
		var tv string
		var has bool
		if flat != nil {
			tv, has = flat[k]
			has = has && strings.TrimSpace(tv) != ""
		} else if tv, has, err = lookup(*target, k); err != nil {
			return err
		}
		if has {
			u.Target, u.State = pieces(tv), *state
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
	fmt.Printf("wrote %d units (%d with a target, %d dropped for tag mismatch) -> %s\n", len(f.Units), targets-len(dropped), len(dropped), *out)
	return nil
}

func readFlat(path string) (map[string]string, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	data, err := io.ReadAll(fh)
	if err != nil {
		return nil, err
	}
	var m map[string]string
	if err := json.Unmarshal(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")), &m); err != nil {
		return nil, errors.New(path + ": " + err.Error())
	}
	return m, nil
}
